package config

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/liosmarf/kubeanvil/internal/catalog"
)

var (
	dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Validate checks what a schema cannot: addresses inside the subnet, no overlaps, etcd
// quorum, and component choices that fit the rest of the cluster. All problems are
// reported at once.
func Validate(c *Cluster, cat *catalog.Catalog) error {
	v := &validator{}
	s := &c.Spec

	v.check(c.APIVersion == APIVersion, "apiVersion must be %s", APIVersion)
	v.check(c.Kind == Kind, "kind must be %s", Kind)
	v.check(dnsLabel.MatchString(c.Metadata.Name), "metadata.name %q must be a lowercase DNS label", c.Metadata.Name)

	switch s.Provider.Type {
	case "proxmox":
		v.proxmox(s.Provider.Proxmox)
	case "vsphere", "azure":
		v.fail("provider %q is planned, not supported yet", s.Provider.Type)
	default:
		v.fail("provider.type must be proxmox")
	}

	v.check(strings.HasPrefix(s.Image.URL, "https://"), "image.url must be an https URL")
	v.check(sha256Re.MatchString(s.Image.SHA256), "image.sha256 must be 64 lowercase hex characters")

	cp, w := s.Nodes.ControlPlane, s.Nodes.Workers
	v.check(cp.Count == 1 || cp.Count == 3 || cp.Count == 5, "nodes.controlPlane.count must be 1, 3 or 5 (etcd needs an odd number)")
	v.check(w.Count >= 0, "nodes.workers.count cannot be negative")
	v.pool("controlPlane", cp, 2, 4096)
	if w.Count > 0 {
		v.pool("workers", w, 2, 4096)
	}

	v.network(c)

	sel := cat.Selection(s.Components)
	for _, err := range cat.Check(s.Components) {
		v.errs = append(v.errs, err)
	}
	if sel["storage"] == "longhorn" {
		v.check(w.Count >= 1, "storage longhorn needs at least one worker")
		v.check(w.DataDiskGiB > 0, "storage longhorn needs nodes.workers.dataDiskGiB > 0")
	}

	v.check(s.Kubernetes.Distribution == "k3s", "kubernetes.distribution must be k3s")
	v.check(strings.HasPrefix(s.Kubernetes.Version, "v"), "kubernetes.version must look like v1.36.5+k3s1")
	v.check(s.Access.AdminUser != "" && s.Access.AdminUser != "root", "access.adminUser must be set and not root")
	v.check(len(s.Access.SSHPublicKeys) > 0, "access.sshPublicKeys needs at least one key")

	v.check(strings.Count(s.DNS.BaseDomain, ".") >= 1, "dns.baseDomain must be a domain such as k8s.example.com")
	switch s.DNS.Provider {
	case "cloudflare", "none":
	case "rfc2136":
		v.check(s.DNS.RFC2136 != nil && s.DNS.RFC2136.Server != "" && s.DNS.RFC2136.Zone != "",
			"dns.rfc2136.server and zone are required for provider rfc2136")
	default:
		v.fail("dns.provider must be cloudflare, rfc2136 or none")
	}
	switch s.TLS.Issuer {
	case "internal-ca":
	case "letsencrypt":
		v.check(s.DNS.Provider != "none", "tls.issuer letsencrypt needs a dns.provider for the DNS-01 challenge")
		v.check(strings.Contains(s.TLS.ACMEEmail, "@"), "tls.acmeEmail is required for letsencrypt")
	default:
		v.fail("tls.issuer must be letsencrypt or internal-ca")
	}

	v.check(strings.HasPrefix(s.GitOps.RepoURL, "https://") || strings.HasPrefix(s.GitOps.RepoURL, "ssh://") ||
		strings.HasPrefix(s.GitOps.RepoURL, "git@"), "gitops.repoUrl must be an https or ssh Git URL")
	if b := s.Backup.S3; b != nil {
		v.check(b.Endpoint != "" && b.Bucket != "", "backup.s3 needs endpoint and bucket")
	}
	return errors.Join(v.errs...)
}

type validator struct{ errs []error }

func (v *validator) fail(format string, a ...any) { v.errs = append(v.errs, fmt.Errorf(format, a...)) }

func (v *validator) check(ok bool, format string, a ...any) {
	if !ok {
		v.fail(format, a...)
	}
}

func (v *validator) proxmox(p *Proxmox) {
	if p == nil {
		v.fail("provider.proxmox is required for provider proxmox")
		return
	}
	v.check(strings.HasPrefix(p.Endpoint, "https://"), "provider.proxmox.endpoint must be an https URL")
	v.check(p.Node != "", "provider.proxmox.node is required")
	v.check(p.VMIDBase >= 100 && p.VMIDBase%10 == 0, "provider.proxmox.vmIdBase must be a multiple of 10, at least 100")
	v.check(p.Datastores.Disks != "", "provider.proxmox.datastores.disks is required")
	v.check(p.Bridge != "", "provider.proxmox.bridge is required")
}

func (v *validator) pool(name string, p Pool, minCPU, minMiB int) {
	v.check(p.CPU >= minCPU, "nodes.%s.cpu must be at least %d", name, minCPU)
	v.check(p.MemoryMiB >= minMiB, "nodes.%s.memoryMiB must be at least %d", name, minMiB)
	v.check(p.OSDiskGiB >= 20, "nodes.%s.osDiskGiB must be at least 20", name)
}

func (v *validator) network(c *Cluster) {
	n := c.Spec.Network
	prefix, err := netip.ParsePrefix(n.CIDR)
	if err != nil {
		v.fail("network.cidr: %v", err)
		return
	}
	prefix = prefix.Masked()

	used := map[netip.Addr]string{}
	claim := func(field, s string) (netip.Addr, bool) {
		ip, err := netip.ParseAddr(s)
		switch {
		case err != nil:
			v.fail("%s: %v", field, err)
			return ip, false
		case !prefix.Contains(ip):
			v.fail("%s %s is outside network.cidr %s", field, ip, prefix)
			return ip, false
		case used[ip] != "":
			v.fail("%s %s is already used by %s", field, ip, used[ip])
			return ip, false
		}
		used[ip] = field
		return ip, true
	}

	claim("network.gateway", n.Gateway)
	claim("network.apiVip", n.APIVIP)

	nodes, err := c.Nodes()
	if err != nil {
		v.fail("nodes: %v", err)
	}
	for _, node := range nodes {
		claim("node "+node.Name, node.IP.String())
	}

	start, okStart := claim("network.loadBalancerRange.start", n.LoadBalancerRange.Start)
	end, errEnd := netip.ParseAddr(n.LoadBalancerRange.End)
	v.check(errEnd == nil, "network.loadBalancerRange.end: %v", errEnd)
	if okStart && errEnd == nil {
		v.check(prefix.Contains(end), "network.loadBalancerRange.end %s is outside network.cidr", end)
		v.check(start.Compare(end) <= 0, "network.loadBalancerRange: start must not be after end")
		for ip := start.Next(); ip.Compare(end) <= 0 && ip.IsValid(); ip = ip.Next() {
			if owner := used[ip]; owner != "" {
				v.fail("network.loadBalancerRange includes %s, used by %s", ip, owner)
			}
		}
	}

	for _, d := range n.DNSServers {
		_, err := netip.ParseAddr(d)
		v.check(err == nil, "network.dnsServers: %q is not an IP address", d)
	}
	for field, s := range map[string]string{"network.podCidr": n.PodCIDR, "network.serviceCidr": n.ServiceCIDR} {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			v.fail("%s: %v", field, err)
			continue
		}
		v.check(!p.Overlaps(prefix), "%s %s overlaps network.cidr %s", field, p, prefix)
	}
}
