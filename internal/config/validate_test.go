package config

import (
	"strings"
	"testing"

	"github.com/liosmarf/kubeanvil/internal/catalog"
)

func example(t *testing.T) (*Cluster, *catalog.Catalog) {
	t.Helper()
	c, err := Load("../../examples/cluster.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	return c, cat
}

func TestExampleIsValid(t *testing.T) {
	c, cat := example(t)
	if err := Validate(c, cat); err != nil {
		t.Fatalf("examples/cluster.yaml should be valid:\n%v", err)
	}
}

func TestValidateCatchesMistakes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Cluster)
		want   string
	}{
		{"even control planes", func(c *Cluster) { c.Spec.Nodes.ControlPlane.Count = 2 }, "odd number"},
		{"VIP outside subnet", func(c *Cluster) { c.Spec.Network.APIVIP = "10.0.0.5" }, "outside network.cidr"},
		{"VIP on a node", func(c *Cluster) { c.Spec.Network.APIVIP = "192.168.50.92" }, "already used"},
		{"LB range over nodes", func(c *Cluster) { c.Spec.Network.LoadBalancerRange.End = "192.168.50.95" }, "loadBalancerRange includes"},
		{"nodes past subnet end", func(c *Cluster) { c.Spec.Nodes.Workers.FirstIP = "192.168.50.254" }, "outside network.cidr"},
		{"pod CIDR overlaps LAN", func(c *Cluster) { c.Spec.Network.PodCIDR = "192.168.0.0/16" }, "overlaps"},
		{"planned component", func(c *Cluster) { c.Spec.Components["secrets"] = "openbao" }, "planned"},
		{"unknown component", func(c *Cluster) { c.Spec.Components["cni"] = "weave" }, "unknown option"},
		{"unknown category", func(c *Cluster) { c.Spec.Components["mesh"] = "istio" }, "unknown category"},
		{"Longhorn without workers", func(c *Cluster) { c.Spec.Nodes.Workers.Count = 0 }, "at least one worker"},
		{"Let's Encrypt without DNS", func(c *Cluster) { c.Spec.DNS.Provider = "none" }, "DNS-01"},
		{"root as admin", func(c *Cluster) { c.Spec.Access.AdminUser = "root" }, "not root"},
		{"bad image checksum", func(c *Cluster) { c.Spec.Image.SHA256 = "abc" }, "sha256"},
		{"planned provider", func(c *Cluster) { c.Spec.Provider.Type = "azure" }, "planned"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, cat := example(t)
			tt.mutate(c)
			err := Validate(c, cat)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want an error containing %q, got: %v", tt.want, err)
			}
		})
	}
}

func TestNodes(t *testing.T) {
	c, _ := example(t)
	nodes, err := c.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 6 {
		t.Fatalf("want 6 nodes, got %d", len(nodes))
	}
	first, last := nodes[0], nodes[5]
	if first.Name != "lab-cp-01" || first.IP.String() != "192.168.50.91" || first.VMID != 201 || first.Role != Server {
		t.Errorf("unexpected first node: %+v", first)
	}
	if last.Name != "lab-wk-03" || last.IP.String() != "192.168.50.96" || last.VMID != 213 || last.Role != Agent {
		t.Errorf("unexpected last node: %+v", last)
	}
}
