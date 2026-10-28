package wizard

import (
	"bytes"
	"cmp"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/liosmarf/kubeanvil/internal/config"
	"github.com/liosmarf/kubeanvil/internal/sshkey"
)

// Proxmox discovery. Two read-only commands, over SSH or locally when kubeanvil runs on the
// Proxmox host: pvesh reports the node, storages, bridges, DNS, time zone, memory and used
// VMIDs; a ping sweep from the host finds addresses already taken on the subnet. The wizard
// proposes answers from them instead of asking.

const discoverScript = `n=$(hostname)
printf '{"node":"%s","storage":%s,"network":%s,"dns":%s,"time":%s,"status":%s,"vms":%s}\n' "$n" \
  "$(pvesh get /nodes/$n/storage --output-format json)" \
  "$(pvesh get /nodes/$n/network --type any_bridge --output-format json)" \
  "$(pvesh get /nodes/$n/dns --output-format json)" \
  "$(pvesh get /nodes/$n/time --output-format json)" \
  "$(pvesh get /nodes/$n/status --output-format json)" \
  "$(pvesh get /cluster/resources --type vm --output-format json)"`

type proxmoxHost struct {
	user, addr string // SSH target; addr is empty when running on the host itself
	inUse      map[netip.Addr]bool
	others     []string // further Proxmox hosts whose VM configs were read for addresses
	notes      []string // things the user should know or fix, shown in the review

	Node    string
	Storage []storage
	Network []bridge
	DNS     struct{ DNS1, DNS2, DNS3, Search string }
	Time    struct{ Timezone string }
	Status  struct {
		Memory struct{ Total, Used, Available int64 }
	}
	VMs []struct {
		VMID int
		Node string
	}
}

type storage struct {
	Storage, Type, Content string
	Active, Shared         int
	Avail                  int64
}

type bridge struct {
	Iface, CIDR, Gateway string
	Active               int
}

// onProxmox reports whether kubeanvil runs on a Proxmox host, where no SSH is needed.
func onProxmox() bool {
	_, err := exec.LookPath("pvesh")
	return err == nil
}

// discover reads a Proxmox host; target is [user@]host, or "" for the local machine.
func discover(target string) (*proxmoxHost, error) {
	h := &proxmoxHost{user: "root", addr: target}
	if u, a, ok := strings.Cut(target, "@"); ok {
		h.user, h.addr = u, a
	}
	out, err := h.run(discoverScript)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(out, h); err != nil {
		return nil, fmt.Errorf("unexpected answer from pvesh: %w", err)
	}
	return h, nil
}

func (h *proxmoxHost) run(script string) ([]byte, error) {
	cmd := exec.Command("sh", "-c", script)
	if h.addr != "" {
		args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=accept-new"}
		for _, key := range sshkey.Private() {
			args = append(args, "-i", key)
		}
		cmd = exec.Command("ssh", append(args, h.user+"@"+h.addr, script)...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return out, nil
}

// configIPs prints the static addresses in the VM and container configs of a Proxmox
// cluster, which covers machines that are switched off.
const configIPs = `grep -ho 'ip=[0-9.]*' /etc/pve/nodes/*/qemu-server/*.conf /etc/pve/nodes/*/lxc/*.conf 2>/dev/null | cut -d= -f2`

// sweep finds the addresses in use on the subnet: it pings the first 254 from the host, adds
// every neighbour that answered ARP (machines that drop pings) and the static addresses in
// the VM and container configs of the Proxmox cluster (machines that are switched off).
func (h *proxmoxHost) sweep(prefix netip.Prefix) map[netip.Addr]bool {
	fmt.Fprintf(os.Stderr, "Checking which addresses on %s are in use…\n", prefix.Masked())
	var ips []string
	for ip := prefix.Masked().Addr().Next(); prefix.Contains(ip) && len(ips) < 254; ip = ip.Next() {
		ips = append(ips, ip.String())
	}
	script := "for ip in " + strings.Join(ips, " ") + "; do (ping -c1 -W1 $ip >/dev/null 2>&1 && echo $ip) & done; wait\n" +
		`ip -4 neigh show | awk '$NF != "FAILED" && $NF != "INCOMPLETE" {print $1}'` + "\n" + configIPs
	used := map[netip.Addr]bool{}
	out, err := h.run(script)
	if err != nil {
		h.notes = append(h.notes, "Could not check which addresses are in use: "+err.Error())
		return used
	}
	for _, f := range strings.Fields(string(out)) {
		if ip, err := netip.ParseAddr(f); err == nil && prefix.Contains(ip) {
			used[ip] = true
		}
	}
	return used
}

// addOthers marks the static addresses of VMs on other Proxmox hosts as taken: a host
// outside this one's cluster cannot see them, and they come back when those VMs start.
func (h *proxmoxHost) addOthers(others []string) {
	for _, target := range others {
		o := &proxmoxHost{user: h.user, addr: target}
		if u, a, ok := strings.Cut(target, "@"); ok {
			o.user, o.addr = u, a
		}
		out, err := o.run(configIPs)
		if err != nil {
			h.notes = append(h.notes, fmt.Sprintf("Could not read the VM configs on %s: %v", o.addr, err))
			continue
		}
		h.others = append(h.others, o.addr)
		for _, f := range strings.Fields(string(out)) {
			if ip, err := netip.ParseAddr(f); err == nil {
				h.inUse[ip] = true
			}
		}
	}
}

// propose fills the Proxmox and network settings of c from what the host reported, also
// avoiding the addresses of VMs on the other hosts.
func (h *proxmoxHost) propose(c *config.Cluster, others []string) {
	p, n := c.Spec.Provider.Proxmox, &c.Spec.Network
	p.Node, p.SSHUser = h.Node, h.user
	p.Datastores.Disks = h.pickStorage("images", "VM disks", p.Datastores.Disks)
	p.Datastores.Images = h.pickStorage("import", "the cloud image", p.Datastores.Images)
	p.Datastores.Snippets = h.pickStorage("snippets", "cloud-init snippets", p.Datastores.Snippets)
	p.VMIDBase = h.freeVMIDBase()

	b, ok := h.bridge()
	if !ok {
		h.notes = append(h.notes, "No active bridge with an IPv4 address on "+h.Node+": set the network by hand.")
		return
	}
	p.Bridge = b.Iface
	addr := h.addr
	if prefix, err := netip.ParsePrefix(b.CIDR); err == nil {
		if addr == "" {
			addr = prefix.Addr().String()
		}
		setNetwork(c, prefix.Masked().String())
		if b.Gateway != "" {
			n.Gateway = b.Gateway
		}
		h.inUse = h.sweep(prefix)
		h.inUse[prefix.Addr()] = true
		h.addOthers(others)
	}
	p.Endpoint = "https://" + addr + ":8006/"
	p.InsecureTLS = !trustedTLS(addr)
	if dns := slices.DeleteFunc([]string{h.DNS.DNS1, h.DNS.DNS2, h.DNS.DNS3}, func(s string) bool { return s == "" }); len(dns) > 0 {
		n.DNSServers = dns
	}
	if h.DNS.Search != "" {
		n.SearchDomain = h.DNS.Search
	}
	if h.Time.Timezone != "" {
		c.Spec.Access.Timezone = h.Time.Timezone
	}
	layoutIPs(c, h.inUse)
}

// storages lists the active storages that accept a content type, local ones first and
// then by free space: VM disks on a shared NFS store are rarely what you want.
func (h *proxmoxHost) storages(content string) []storage {
	var out []storage
	for _, s := range h.Storage {
		if s.Active == 1 && slices.Contains(strings.Split(s.Content, ","), content) {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b storage) int {
		if a.Shared != b.Shared {
			return a.Shared - b.Shared
		}
		return cmp.Compare(b.Avail, a.Avail)
	})
	return out
}

func (h *proxmoxHost) pickStorage(content, purpose, fallback string) string {
	if s := h.storages(content); len(s) > 0 {
		return s[0].Storage
	}
	h.notes = append(h.notes, fmt.Sprintf("No storage on %s allows %q content for %s. Enable it, e.g.: pvesm set %s --content <current>,%s",
		h.Node, content, purpose, fallback, content))
	return fallback
}

// bridge returns the bridge that carries the default route, or any bridge with an address.
func (h *proxmoxHost) bridge() (bridge, bool) {
	var found bridge
	for _, b := range h.Network {
		if b.Active != 1 || b.CIDR == "" {
			continue
		}
		if b.Gateway != "" {
			return b, true
		}
		if found.Iface == "" {
			found = b
		}
	}
	return found, found.Iface != ""
}

// freeVMIDBase finds a base with 20 free VMIDs above it, preferring the hundred this node's
// VMs already use (a common convention: 1xx on pve1, 2xx on pve2).
func (h *proxmoxHost) freeVMIDBase() int {
	used := map[int]bool{}
	perHundred := map[int]int{}
	for _, vm := range h.VMs {
		used[vm.VMID] = true
		if vm.Node == h.Node {
			perHundred[vm.VMID/100]++
		}
	}
	free := func(base int) bool {
		for id := base; id <= base+20; id++ { // up to 5 control planes and 10 workers
			if used[id] {
				return false
			}
		}
		return true
	}
	var candidates []int
	if len(perHundred) > 0 {
		top := 0
		for hundred, count := range perHundred {
			if count > perHundred[top] || (count == perHundred[top] && hundred < top) {
				top = hundred
			}
		}
		for base := top * 100; base < top*100+100; base += 10 {
			candidates = append(candidates, base)
		}
	}
	for base := 100; base < 10000; base += 10 {
		candidates = append(candidates, base)
	}
	for _, base := range candidates {
		if base >= 100 && free(base) {
			return base
		}
	}
	return 9000
}

// freeMiB is the memory left for new VMs.
func (h *proxmoxHost) freeMiB() int {
	m := h.Status.Memory
	if m.Available > 0 {
		return int(m.Available >> 20)
	}
	return int((m.Total - m.Used) >> 20)
}

// trustedTLS reports whether the Proxmox API presents a certificate this machine trusts;
// the default self-signed one does not, and Terraform then needs insecure TLS.
func trustedTLS(addr string) bool {
	for range 2 { // one retry: a dropped connection is not an untrusted certificate
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", net.JoinHostPort(addr, "8006"), &tls.Config{ServerName: addr})
		if err == nil {
			conn.Close()
			return true
		}
		var unknown x509.UnknownAuthorityError
		var invalid x509.HostnameError
		if errors.As(err, &unknown) || errors.As(err, &invalid) {
			return false
		}
	}
	return false
}
