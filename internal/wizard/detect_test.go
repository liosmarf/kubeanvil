package wizard

import (
	"encoding/json"
	"net/netip"
	"testing"
)

// A trimmed answer from a real Proxmox host (pvesh output combined by discoverScript).
const pve2 = `{"node":"pve2",
 "storage":[
  {"storage":"adrive","type":"nfs","content":"backup,rootdir,snippets,import,images","active":1,"shared":1,"avail":2080424198144},
  {"storage":"local","type":"dir","content":"vztmpl,import,snippets,backup,iso","active":1,"shared":0,"avail":48792387584},
  {"storage":"nvme1","type":"zfspool","content":"rootdir,images","active":1,"shared":0,"avail":914543984640},
  {"storage":"local-lvm","type":"lvmthin","content":"images,rootdir","active":1,"shared":0,"avail":438720527546}],
 "network":[
  {"iface":"vmbr1","cidr":"192.168.50.32/24","active":1},
  {"iface":"vmbr0","cidr":"192.168.50.22/24","gateway":"192.168.50.1","active":1}],
 "dns":{"dns1":"192.168.50.10","search":"example.net"},
 "time":{"timezone":"America/Chicago"},
 "status":{"memory":{"total":134217728000,"used":68719476736,"available":0}},
 "vms":[{"vmid":200,"node":"pve2"},{"vmid":201,"node":"pve2"},{"vmid":215,"node":"pve2"},{"vmid":101,"node":"pve1"}]}`

func testHost(t *testing.T) *proxmoxHost {
	t.Helper()
	h := &proxmoxHost{user: "root", addr: "pve2.example.net"}
	if err := json.Unmarshal([]byte(pve2), h); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestStoragesPreferLocalThenFreeSpace(t *testing.T) {
	h := testHost(t)
	var got []string
	for _, s := range h.storages("images") {
		got = append(got, s.Storage)
	}
	want := []string{"nvme1", "local-lvm", "adrive"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("storages(images) = %v, want %v", got, want)
	}
	if h.pickStorage("snippets", "snippets", "local") != "local" {
		t.Error("snippets should go to the local dir storage, not the shared NFS one")
	}
}

func TestFreeVMIDBaseStaysInTheNodesHundred(t *testing.T) {
	// 200, 201 and 215 are taken: 200-220 and 210-230 include them, so 220 is the first fit.
	if got := testHost(t).freeVMIDBase(); got != 220 {
		t.Errorf("freeVMIDBase = %d, want 220", got)
	}
}

func TestBridgeAndMemory(t *testing.T) {
	h := testHost(t)
	if b, _ := h.bridge(); b.Iface != "vmbr0" {
		t.Errorf("bridge = %s, want vmbr0 (it has the gateway)", b.Iface)
	}
	if got := h.freeMiB(); got != 62464 {
		t.Errorf("freeMiB = %d, want 62464 (total - used)", got)
	}
}

func TestLayoutSkipsAddressesInUse(t *testing.T) {
	c := newCluster() // production preset: VIP + 6 nodes
	setNetwork(c, "192.168.50.0/24")
	layoutIPs(c, map[netip.Addr]bool{netip.MustParseAddr("192.168.50.92"): true, netip.MustParseAddr("192.168.50.47"): true})
	n := c.Spec.Network
	// .92 answers, so the 7-address block moves to .93; .47 pushes the LoadBalancer range to .48.
	got := []string{n.APIVIP, c.Spec.Nodes.ControlPlane.FirstIP, c.Spec.Nodes.Workers.FirstIP, n.LoadBalancerRange.Start, n.LoadBalancerRange.End}
	want := []string{"192.168.50.93", "192.168.50.94", "192.168.50.97", "192.168.50.48", "192.168.50.53"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("layout = %v, want %v", got, want)
		}
	}
}
