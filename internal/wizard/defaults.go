package wizard

import (
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/liosmarf/kubeanvil/internal/config"
)

// Pinned defaults for new clusters. Renovate keeps them current.
const (
	defaultK3sVersion = "v1.36.5+k3s1"
	defaultImageURL   = "https://cloud-images.ubuntu.com/releases/noble/release-20261017/ubuntu-24.04-server-cloudimg-amd64.img"
	defaultImageSHA   = "04e8f2461a16a44e78a370284471f29616cdd9baf1893df25807b71f985b6c94"
)

// Preset is a starting point for sizing; every value can still be changed.
type Preset struct {
	ID, Title, Description string
	ControlPlane, Workers  config.Pool
}

var presets = []Preset{
	{
		ID: "lab", Title: "Lab", Description: "learning and testing, no high availability",
		ControlPlane: config.Pool{Count: 1, CPU: 2, MemoryMiB: 4096, OSDiskGiB: 30},
		Workers:      config.Pool{Count: 1, CPU: 4, MemoryMiB: 16384, OSDiskGiB: 40, DataDiskGiB: 50},
	},
	{
		ID: "small-ha", Title: "Small HA", Description: "survives the loss of any one node",
		ControlPlane: config.Pool{Count: 3, CPU: 2, MemoryMiB: 4096, OSDiskGiB: 30},
		Workers:      config.Pool{Count: 2, CPU: 4, MemoryMiB: 12288, OSDiskGiB: 60, DataDiskGiB: 100},
	},
	{
		ID: "production", Title: "Production-like", Description: "three copies of every volume",
		ControlPlane: config.Pool{Count: 3, CPU: 4, MemoryMiB: 8192, OSDiskGiB: 40},
		Workers:      config.Pool{Count: 3, CPU: 4, MemoryMiB: 12288, OSDiskGiB: 60, DataDiskGiB: 100},
	},
}

// newCluster returns a complete cluster with sensible defaults, used when no
// cluster.yaml exists yet.
func newCluster() *config.Cluster {
	p := presets[2]
	c := &config.Cluster{
		APIVersion: config.APIVersion,
		Kind:       config.Kind,
		Metadata:   config.Metadata{Name: "lab"},
	}
	s := &c.Spec
	s.Provider = config.Provider{Type: "proxmox", Proxmox: &config.Proxmox{
		Endpoint: "https://pve.example.lan:8006/", Node: "pve", VMIDBase: 200, Bridge: "vmbr0", SSHUser: "root",
		Datastores: config.Datastores{Disks: "local-lvm", Images: "local", Snippets: "local"},
	}}
	s.Image = config.Image{URL: defaultImageURL, SHA256: defaultImageSHA}
	// home.arpa is reserved for home networks (RFC 8375).
	s.Network = config.Network{SearchDomain: "home.arpa", PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16"}
	setNetwork(c, "192.168.50.0/24")
	s.Nodes = config.Nodes{ControlPlane: p.ControlPlane, Workers: p.Workers}
	layoutIPs(c, nil)
	s.Kubernetes = config.Kubernetes{Distribution: "k3s", Version: defaultK3sVersion}
	s.Access = config.Access{AdminUser: "ops", Timezone: localTimezone()}
	s.DNS = config.DNS{BaseDomain: "k8s.example.com", Provider: "none"}
	s.TLS = config.TLS{Issuer: "internal-ca"}
	s.GitOps = config.GitOps{RepoURL: "https://github.com/liosmarf/kubeanvil.git", Revision: "main"}
	s.Components = map[string]string{}
	return c
}

// setNetwork derives gateway and DNS from the subnet: the first host address.
func setNetwork(c *config.Cluster, cidr string) {
	n := &c.Spec.Network
	n.CIDR = cidr
	n.Gateway = host(cidr, 1)
	n.DNSServers = []string{host(cidr, 1)}
}

// layoutIPs proposes a tidy address plan inside the subnet that avoids addresses in use:
// six LoadBalancer addresses from .45, then one block from .90 with the API VIP followed
// by the control planes and the workers. A busy range moves the block to the next gap.
func layoutIPs(c *config.Cluster, inUse map[netip.Addr]bool) {
	n, nodes := &c.Spec.Network, &c.Spec.Nodes
	prefix, err := netip.ParsePrefix(n.CIDR)
	if err != nil {
		return
	}
	taken := map[netip.Addr]bool{}
	for ip := range inUse {
		taken[ip] = true
	}
	for _, s := range append([]string{n.Gateway}, n.DNSServers...) {
		if ip, err := netip.ParseAddr(s); err == nil {
			taken[ip] = true
		}
	}
	block := freeRun(prefix, 90, 1+nodes.ControlPlane.Count+nodes.Workers.Count, taken)
	lb := freeRun(prefix, 45, 6, taken)

	n.APIVIP = host(n.CIDR, block)
	nodes.ControlPlane.FirstIP = host(n.CIDR, block+1)
	nodes.Workers.FirstIP = host(n.CIDR, block+1+nodes.ControlPlane.Count)
	n.LoadBalancerRange = config.Range{Start: host(n.CIDR, lb), End: host(n.CIDR, lb+5)}
}

// freeRun returns the offset of the first run of size free host addresses, searching from
// offset `from` upwards and then below it, and marks the run as taken. It falls back to
// `from` when the subnet has no such gap, so validation reports the clash.
func freeRun(prefix netip.Prefix, from, size int, taken map[netip.Addr]bool) int {
	hosts := 1<<(32-prefix.Bits()) - 2 // without network and broadcast address
	hosts = min(hosts, 4094)
	fits := func(start int) bool {
		for i := start; i < start+size; i++ {
			ip, err := netip.ParseAddr(host(prefix.String(), i))
			if i > hosts || err != nil || taken[ip] {
				return false
			}
		}
		return true
	}
	starts := make([]int, 0, hosts)
	for i := from; i <= hosts; i++ {
		starts = append(starts, i)
	}
	for i := 2; i < from && i <= hosts; i++ {
		starts = append(starts, i)
	}
	run := from
	for _, s := range starts {
		if fits(s) {
			run = s
			break
		}
	}
	for i := run; i < run+size; i++ {
		if ip, err := netip.ParseAddr(host(prefix.String(), i)); err == nil {
			taken[ip] = true
		}
	}
	return run
}

// presetMiB is the memory all VMs of a preset need on the hypervisor.
func presetMiB(p Preset) int {
	return p.ControlPlane.Count*p.ControlPlane.MemoryMiB + p.Workers.Count*p.Workers.MemoryMiB
}

// localTimezone is this machine's IANA zone, read from the /etc/localtime link.
func localTimezone() string {
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(link, "zoneinfo/"); ok {
			return zone
		}
	}
	return "UTC"
}

// host returns the n-th address of a subnet, or "" if the subnet is invalid or too small.
func host(cidr string, n int) string {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return ""
	}
	ip := p.Masked().Addr()
	for range n {
		ip = ip.Next()
	}
	if !p.Contains(ip) {
		return ""
	}
	return ip.String()
}

func mib(v int) string { return fmt.Sprintf("%.1f GiB", float64(v)/1024) }
