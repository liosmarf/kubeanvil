package wizard

import (
	"fmt"
	"net/netip"

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
		ID: "lab", Title: "Lab", Description: "1 control plane + 1 worker. Learning and testing; no high availability.",
		ControlPlane: config.Pool{Count: 1, CPU: 2, MemoryMiB: 4096, OSDiskGiB: 30},
		Workers:      config.Pool{Count: 1, CPU: 4, MemoryMiB: 8192, OSDiskGiB: 40, DataDiskGiB: 50},
	},
	{
		ID: "small-ha", Title: "Small HA", Description: "3 control planes + 2 workers. Survives the loss of any one node.",
		ControlPlane: config.Pool{Count: 3, CPU: 2, MemoryMiB: 4096, OSDiskGiB: 30},
		Workers:      config.Pool{Count: 2, CPU: 4, MemoryMiB: 12288, OSDiskGiB: 60, DataDiskGiB: 100},
	},
	{
		ID: "production", Title: "Production-like", Description: "3 control planes + 3 workers. Full replicas for storage and the platform.",
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
	layoutIPs(c)
	s.Kubernetes = config.Kubernetes{Distribution: "k3s", Version: defaultK3sVersion}
	s.Access = config.Access{AdminUser: "ops", Timezone: "UTC"}
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

// layoutIPs proposes a tidy, non-overlapping address plan inside the subnet:
// LoadBalancer range at .45-.50, the API VIP at .90, nodes from .91 upwards.
func layoutIPs(c *config.Cluster) {
	cidr := c.Spec.Network.CIDR
	n := &c.Spec.Network
	n.LoadBalancerRange = config.Range{Start: host(cidr, 45), End: host(cidr, 50)}
	n.APIVIP = host(cidr, 90)
	c.Spec.Nodes.ControlPlane.FirstIP = host(cidr, 91)
	c.Spec.Nodes.Workers.FirstIP = host(cidr, 91+c.Spec.Nodes.ControlPlane.Count)
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
