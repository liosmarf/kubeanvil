package wizard

import (
	"strings"
	"testing"

	"github.com/liosmarf/kubeanvil/internal/catalog"
	"github.com/liosmarf/kubeanvil/internal/config"
)

// A brand-new cluster must be valid before the user answers anything, so pressing Enter
// through the whole wizard always works.
func TestDefaultsAreValid(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := newCluster()
	c.Spec.Access.SSHPublicKeys = []string{"ssh-ed25519 AAAA test"}
	if err := config.Validate(c, cat); err != nil {
		t.Fatalf("defaults are not valid:\n%v", err)
	}
	for _, p := range presets {
		c.Spec.Nodes.ControlPlane, c.Spec.Nodes.Workers = p.ControlPlane, p.Workers
		layoutIPs(c, nil)
		if err := config.Validate(c, cat); err != nil {
			t.Errorf("preset %s is not valid:\n%v", p.ID, err)
		}
	}
}

func TestLayoutFollowsTheSubnet(t *testing.T) {
	c := newCluster()
	setNetwork(c, "10.20.0.0/24")
	layoutIPs(c, nil)
	n := c.Spec.Network
	got := []string{n.Gateway, n.APIVIP, n.LoadBalancerRange.Start, c.Spec.Nodes.ControlPlane.FirstIP, c.Spec.Nodes.Workers.FirstIP}
	want := []string{"10.20.0.1", "10.20.0.90", "10.20.0.45", "10.20.0.91", "10.20.0.94"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("layout = %v, want %v", got, want)
	}
}

func TestSummaryWarnsWhenWorkersAreTooSmall(t *testing.T) {
	cat, _ := catalog.Load()
	c := newCluster()
	c.Spec.Nodes.Workers.Count, c.Spec.Nodes.Workers.MemoryMiB = 2, 4096
	if !strings.Contains(Summary(c, cat), "Little room") {
		t.Error("expected a memory warning: ~8 GiB of platform on 2 × 4 GiB workers")
	}
}
