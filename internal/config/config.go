// Package config models cluster.yaml, the single description of a cluster (ADR 0001).
package config

import (
	"bytes"
	"fmt"
	"net/netip"
	"os"

	"go.yaml.in/yaml/v3"
)

const (
	APIVersion = "kubeanvil.io/v1alpha1"
	Kind       = "Cluster"
)

type Cluster struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

type Spec struct {
	Provider   Provider          `yaml:"provider"`
	Image      Image             `yaml:"image"`
	Network    Network           `yaml:"network"`
	Nodes      Nodes             `yaml:"nodes"`
	Kubernetes Kubernetes        `yaml:"kubernetes"`
	Access     Access            `yaml:"access"`
	DNS        DNS               `yaml:"dns"`
	TLS        TLS               `yaml:"tls"`
	GitOps     GitOps            `yaml:"gitops"`
	Backup     Backup            `yaml:"backup,omitempty"`
	Components map[string]string `yaml:"components"`
}

type Provider struct {
	Type    string   `yaml:"type"`
	Proxmox *Proxmox `yaml:"proxmox,omitempty"`
}

type Proxmox struct {
	Endpoint    string     `yaml:"endpoint"`
	Node        string     `yaml:"node"`
	InsecureTLS bool       `yaml:"insecureTLS"`
	VMIDBase    int        `yaml:"vmIdBase"`
	Datastores  Datastores `yaml:"datastores"`
	Bridge      string     `yaml:"bridge"`
	SSHUser     string     `yaml:"sshUser"`
}

type Datastores struct {
	Disks    string `yaml:"disks"`
	Images   string `yaml:"images"`
	Snippets string `yaml:"snippets"`
}

type Image struct {
	URL    string `yaml:"url"`
	SHA256 string `yaml:"sha256"`
}

type Network struct {
	CIDR              string   `yaml:"cidr"`
	Gateway           string   `yaml:"gateway"`
	DNSServers        []string `yaml:"dnsServers"`
	SearchDomain      string   `yaml:"searchDomain"`
	APIVIP            string   `yaml:"apiVip"`
	LoadBalancerRange Range    `yaml:"loadBalancerRange"`
	PodCIDR           string   `yaml:"podCidr"`
	ServiceCIDR       string   `yaml:"serviceCidr"`
}

type Range struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

type Nodes struct {
	ControlPlane Pool `yaml:"controlPlane"`
	Workers      Pool `yaml:"workers"`
}

// Pool is a group of identical nodes with consecutive IP addresses.
type Pool struct {
	Count       int    `yaml:"count"`
	FirstIP     string `yaml:"firstIp"`
	CPU         int    `yaml:"cpu"`
	MemoryMiB   int    `yaml:"memoryMiB"`
	OSDiskGiB   int    `yaml:"osDiskGiB"`
	DataDiskGiB int    `yaml:"dataDiskGiB,omitempty"`
}

type Kubernetes struct {
	Distribution string `yaml:"distribution"`
	Version      string `yaml:"version"`
}

type Access struct {
	AdminUser     string   `yaml:"adminUser"`
	SSHPublicKeys []string `yaml:"sshPublicKeys"`
	Timezone      string   `yaml:"timezone,omitempty"`
}

type DNS struct {
	BaseDomain string   `yaml:"baseDomain"`
	Provider   string   `yaml:"provider"`
	RFC2136    *RFC2136 `yaml:"rfc2136,omitempty"`
}

type RFC2136 struct {
	Server string `yaml:"server"`
	Zone   string `yaml:"zone"`
}

type TLS struct {
	Issuer    string `yaml:"issuer"`
	ACMEEmail string `yaml:"acmeEmail,omitempty"`
}

type GitOps struct {
	RepoURL  string `yaml:"repoUrl"`
	Revision string `yaml:"revision"`
}

type Backup struct {
	S3 *S3 `yaml:"s3,omitempty"`
}

type S3 struct {
	Endpoint string `yaml:"endpoint"`
	Region   string `yaml:"region"`
	Bucket   string `yaml:"bucket"`
}

// Load reads cluster.yaml, rejecting unknown fields so typos fail loudly.
func Load(path string) (*Cluster, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Cluster
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Save writes the cluster with a header pointing editors at the schema.
func (c *Cluster) Save(path string) error {
	var buf bytes.Buffer
	buf.WriteString("# kubeanvil cluster definition. Edit by hand or re-run `kubeanvil init`.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// Role is the Kubernetes role of a node.
type Role string

const (
	Server Role = "server" // control plane + etcd
	Agent  Role = "agent"  // worker
)

// Node is one machine, derived from a Pool.
type Node struct {
	Name string
	Role Role
	IP   netip.Addr
	VMID int
	Pool Pool
}

// Nodes expands the pools into concrete nodes: <name>-cp-01..., <name>-wk-01...
// Control planes get VMIDs base+1..., workers base+11...
func (c *Cluster) Nodes() ([]Node, error) {
	var nodes []Node
	for _, p := range []struct {
		pool   Pool
		role   Role
		prefix string
		offset int
	}{
		{c.Spec.Nodes.ControlPlane, Server, "cp", 1},
		{c.Spec.Nodes.Workers, Agent, "wk", 11},
	} {
		if p.pool.Count == 0 {
			continue
		}
		ip, err := netip.ParseAddr(p.pool.FirstIP)
		if err != nil {
			return nil, fmt.Errorf("%s firstIp: %w", p.prefix, err)
		}
		for i := range p.pool.Count {
			nodes = append(nodes, Node{
				Name: fmt.Sprintf("%s-%s-%02d", c.Metadata.Name, p.prefix, i+1),
				Role: p.role,
				IP:   ip,
				VMID: c.vmIDBase() + p.offset + i,
				Pool: p.pool,
			})
			ip = ip.Next()
		}
	}
	return nodes, nil
}

func (c *Cluster) vmIDBase() int {
	if c.Spec.Provider.Proxmox != nil {
		return c.Spec.Provider.Proxmox.VMIDBase
	}
	return 0
}
