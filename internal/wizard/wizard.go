// Package wizard asks the questions that produce a cluster.yaml (kubeanvil init).
//
// A new cluster takes four answers: a name, the Proxmox host, a size and whether the
// recommended components are fine. Everything else is read from the host or derived, then
// shown on a review screen where any section can be changed. One question per screen.
package wizard

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/liosmarf/kubeanvil/internal/catalog"
	"github.com/liosmarf/kubeanvil/internal/config"
	"github.com/liosmarf/kubeanvil/internal/sshkey"
)

// Options tune how the wizard runs.
type Options struct {
	Accessible bool // plain line-by-line prompts (screen readers, dumb terminals, scripted input)
}

// Run builds a cluster from a few answers, or opens the review of an existing one, and
// returns it once it passes config.Validate. The caller decides where to save it.
func Run(existing *config.Cluster, cat *catalog.Catalog, opts Options) (*config.Cluster, error) {
	w := &wizard{c: existing, cat: cat, opts: opts}
	if w.c == nil {
		w.c = newCluster()
		if err := w.quickStart(); err != nil {
			return nil, err
		}
	}
	w.step = 0
	if err := w.review(); err != nil {
		return nil, err
	}
	return w.c, nil
}

// ErrCancelled means the user left the wizard on purpose; nothing was written.
var ErrCancelled = errors.New("cancelled, nothing written")

const quickStartSteps = 4

// title numbers the questions of the quick start.
func (w *wizard) title(t string) string {
	if w.step == 0 {
		return t
	}
	return fmt.Sprintf("%s (%d of %d)", t, w.step, quickStartSteps)
}

type wizard struct {
	c    *config.Cluster
	cat  *catalog.Catalog
	opts Options
	host *proxmoxHost // nil when the Proxmox host could not be read
	step int          // current quick-start question, 0 outside the quick start
	// Inputs of the current form. Accessible prompts return "" for a bare Enter; these
	// remember the proposed value so Enter keeps it, as it does in the visual form.
	texts []*textField
	ints  []*intField
}

// quickStart asks only what cannot be discovered.
func (w *wizard) quickStart() error {
	c := w.c
	w.step++
	if err := w.ask(w.input(w.title("Cluster name"), "Lowercase letters, digits and dashes. VMs are named <name>-cp-01, <name>-wk-01…",
		&c.Metadata.Name, validName)); err != nil {
		return err
	}
	if err := w.connect(); err != nil {
		return err
	}
	c.Spec.DNS.BaseDomain = c.Metadata.Name + "." + c.Spec.Network.SearchDomain
	if len(c.Spec.Access.SSHPublicKeys) == 0 {
		if key := sshkey.LocalPublic(); key != "" {
			c.Spec.Access.SSHPublicKeys = []string{key}
		}
	}
	if w.host != nil {
		w.applyPreset(w.fittingPreset())
	}
	w.step++
	if err := w.editSize(); err != nil {
		return err
	}
	w.step++
	components := "recommended"
	if err := w.ask(huh.NewSelect[string]().Title(w.title("Platform components")).
		Description("Storage, monitoring, logs, backups, policies… one proven choice in each category.").
		Options(huh.NewOption("Use the recommended set", "recommended"), huh.NewOption("Choose each one myself", "choose")).
		Value(&components)); err != nil {
		return err
	}
	if components == "choose" {
		return w.editComponents()
	}
	return nil
}

// connect reads the Proxmox host and proposes settings from it. When it cannot be read,
// the user may retry or type the settings in.
func (w *wizard) connect() error {
	target, local := "", onProxmox()
	w.step++
	for {
		if !local {
			if err := w.ask(w.input(w.title("Proxmox host"), "Name or IP of the Proxmox node for the VMs, as in `ssh root@<host>`. kubeanvil only reads from it.\n"+
				"Other Proxmox hosts on this LAN outside its cluster? Add them after a comma to avoid their VMs' addresses.",
				&target, nonEmpty)); err != nil {
				return err
			}
		}
		hosts := splitList(target)
		others := []string{}
		if len(hosts) > 0 {
			target, others = hosts[0], hosts[1:]
		}
		where := target
		if local {
			where = "this Proxmox host"
		}
		fmt.Fprintf(os.Stderr, "Reading %s…\n", where)
		h, err := discover(target)
		if err == nil {
			h.propose(w.c, others)
			w.host = h
			return nil
		}

		next := "retry"
		if err := w.ask(huh.NewSelect[string]().Title(fmt.Sprintf("Could not read %s: %v", where, err)).
			Description("kubeanvil logs in as root with a key from ~/.ssh. Check that `ssh root@"+target+"` works without a password.").
			Options(huh.NewOption("Try again", "retry"), huh.NewOption("Enter the Proxmox details myself", "manual")).
			Value(&next)); err != nil {
			return err
		}
		if next == "manual" {
			p := w.c.Spec.Provider.Proxmox
			if _, addr, ok := strings.Cut(target, "@"); ok {
				target = addr
			}
			if target != "" {
				p.Endpoint = "https://" + target + ":8006/"
				if _, err := netip.ParseAddr(target); err != nil {
					p.Node, _, _ = strings.Cut(target, ".")
				}
			}
			if err := w.editProxmox(); err != nil {
				return err
			}
			return w.editNetwork()
		}
		local = false
	}
}

// review shows what will be written and lets the user change any section first.
func (w *wizard) review() error {
	sections := []struct {
		id, title string
		edit      func() error
	}{
		{"size", "node count and sizes", w.editSize},
		{"components", "platform components", w.editComponents},
		{"proxmox", "Proxmox settings", w.editProxmox},
		{"network", "network and addresses", w.editNetwork},
		{"domain", "domain and certificates", w.editDomain},
		{"access", "access and GitOps repository", w.editAccess},
		{"backup", "backups", w.editBackup},
	}
	for {
		problems := config.Validate(w.c, w.cat)
		if problems != nil && w.opts.Accessible && stdinClosed() {
			return fmt.Errorf("cluster definition is not valid:\n%w", problems)
		}
		title, desc, choice := "Review", w.summary(), "write"
		var opts []huh.Option[string]
		if problems == nil {
			opts = append(opts, huh.NewOption("Write cluster.yaml", "write"))
		} else {
			title, choice = "Some settings need fixing", sections[0].id
			desc += "\n\n" + problems.Error()
		}
		for _, s := range sections {
			opts = append(opts, huh.NewOption("Change "+s.title, s.id))
		}
		opts = append(opts, huh.NewOption("Quit without writing", "quit"))
		if err := w.form(huh.NewGroup(
			huh.NewNote().Title(title).Description(desc),
			huh.NewSelect[string]().Title("What next?").Options(opts...).Value(&choice),
		)); err != nil {
			return err
		}
		switch choice {
		case "write":
			return nil
		case "quit":
			return ErrCancelled
		}
		for _, s := range sections {
			if s.id == choice {
				if err := s.edit(); err != nil {
					return err
				}
			}
		}
	}
}

// ask runs a form with one question per screen.
func (w *wizard) ask(fields ...huh.Field) error {
	groups := make([]*huh.Group, len(fields))
	for i, f := range fields {
		groups[i] = huh.NewGroup(f)
	}
	return w.form(groups...)
}

// form runs one form and copies numeric answers back into the cluster.
func (w *wizard) form(groups ...*huh.Group) error {
	if w.opts.Accessible && stdinClosed() {
		return errors.New("input ended before all questions were answered")
	}
	form := huh.NewForm(groups...).WithAccessible(w.opts.Accessible)
	if w.opts.Accessible {
		// Only here: the visual form must read the terminal itself to switch it to raw mode,
		// otherwise arrow keys and Enter never reach it.
		form = form.WithInput(stdin)
	}
	err := form.Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrCancelled
	}
	if err != nil {
		return err
	}
	for _, f := range w.texts {
		if strings.TrimSpace(*f.value) == "" {
			*f.value = f.proposed
		}
	}
	for _, f := range w.ints {
		if n, err := strconv.Atoi(strings.TrimSpace(f.text)); err == nil {
			*f.dst = n * f.scale
		}
	}
	w.texts, w.ints = nil, nil
	return nil
}

// --- sections ------------------------------------------------------------------------------

// editSize offers the presets, measured against the host's free memory, or every value.
func (w *wizard) editSize() error {
	nodes := &w.c.Spec.Nodes
	choice := "custom"
	for _, p := range presets {
		if samePool(p.ControlPlane, nodes.ControlPlane) && samePool(p.Workers, nodes.Workers) {
			choice = p.ID
		}
	}
	var opts []huh.Option[string]
	for _, p := range presets {
		label := fmt.Sprintf("%-16s %s + %s · %s · %s", p.Title, count(p.ControlPlane.Count, "control plane"),
			count(p.Workers.Count, "worker"), mib(presetMiB(p)), p.Description)
		opts = append(opts, huh.NewOption(label, p.ID))
	}
	opts = append(opts, huh.NewOption(fmt.Sprintf("%-16s %s", "Custom", "set the count, CPU, memory and disks yourself"), "custom"))
	desc := "Memory is what the VMs take on the hypervisor."
	if w.host != nil {
		desc = fmt.Sprintf("%s has %s of memory free.", w.host.Node, mib(w.host.freeMiB()))
	}
	if err := w.ask(huh.NewSelect[string]().Title(w.title("Cluster size")).Description(desc).Options(opts...).Value(&choice)); err != nil {
		return err
	}
	for _, p := range presets {
		if p.ID == choice {
			w.applyPreset(p)
			return nil
		}
	}
	return w.editNodeValues()
}

func (w *wizard) editNodeValues() error {
	cp, wk := &w.c.Spec.Nodes.ControlPlane, &w.c.Spec.Nodes.Workers
	block := cp.Count + wk.Count
	cpCount := strconv.Itoa(cp.Count)
	err := w.ask(
		huh.NewSelect[string]().Title("Control planes").Description("They run Kubernetes itself and etcd, which needs an odd number.").
			Options(huh.NewOption("1 — no high availability", "1"), huh.NewOption("3 — survives one failure", "3"),
				huh.NewOption("5 — survives two failures", "5")).Value(&cpCount),
		w.intInput("vCPUs per control plane", "", &cp.CPU),
		w.gibInput("Memory per control plane (GiB)", "4 is enough for K3s and etcd; 8 leaves headroom.", &cp.MemoryMiB),
		w.intInput("Disk per control plane (GiB)", "", &cp.OSDiskGiB),
		w.intInput("Workers", "Your applications and the platform run here. 0 runs everything on the control planes.", &wk.Count),
	)
	cp.Count, _ = strconv.Atoi(cpCount)
	if err == nil && wk.Count > 0 {
		err = w.ask(
			w.intInput("vCPUs per worker", "", &wk.CPU),
			w.gibInput("Memory per worker (GiB)", "", &wk.MemoryMiB),
			w.intInput("System disk per worker (GiB)", "", &wk.OSDiskGiB),
			w.intInput("Data disk per worker for Longhorn (GiB)", "0 for none.", &wk.DataDiskGiB),
		)
	}
	if cp.Count+wk.Count != block {
		layoutIPs(w.c, w.inUse())
	}
	return err
}

func (w *wizard) editProxmox() error {
	p := w.c.Spec.Provider.Proxmox
	return w.ask(
		w.input("Proxmox API endpoint", "", &p.Endpoint, prefixed("https://")),
		w.input("Proxmox node", "The node that hosts the VMs.", &p.Node, nonEmpty),
		w.storageField("Storage for VM disks", "images", &p.Datastores.Disks),
		w.storageField("Storage for the cloud image", "import", &p.Datastores.Images),
		w.storageField("Storage for cloud-init snippets", "snippets", &p.Datastores.Snippets),
		w.bridgeField(&p.Bridge),
		w.intInput("First VMID", "Control planes take the next IDs; workers start 11 above it.", &p.VMIDBase),
	)
}

func (w *wizard) editNetwork() error {
	n := &w.c.Spec.Network
	cidr := n.CIDR
	if err := w.ask(w.input("Node subnet", "The LAN the VMs join, e.g. 192.168.50.0/24.", &cidr, validPrefix)); err != nil {
		return err
	}
	if prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr)); err == nil && prefix.Masked().String() != n.CIDR {
		setNetwork(w.c, prefix.Masked().String())
		if w.host != nil {
			w.host.inUse = w.host.sweep(prefix)
		}
		layoutIPs(w.c, w.inUse())
	}
	dns, vip := strings.Join(n.DNSServers, ", "), n.APIVIP
	err := w.ask(
		w.input("Gateway", "", &n.Gateway, validAddr),
		w.input("DNS servers", "Comma separated.", &dns, nonEmpty),
		w.input("Search domain", "Nodes are named <node>.<search domain>.", &n.SearchDomain, nonEmpty),
		w.input("Kubernetes API address", "A free address that moves between control planes. The nodes take the addresses right after it.", &n.APIVIP, validAddr),
		w.input("First LoadBalancer address", "Free addresses for Ingress and LoadBalancer Services.", &n.LoadBalancerRange.Start, validAddr),
		w.input("Last LoadBalancer address", "", &n.LoadBalancerRange.End, validAddr),
	)
	n.DNSServers = splitList(dns)
	if ip, ok := parseAddr(n.APIVIP); ok && n.APIVIP != vip {
		nodes := &w.c.Spec.Nodes
		nodes.ControlPlane.FirstIP = ip.Next().String()
		for range nodes.ControlPlane.Count {
			ip = ip.Next()
		}
		nodes.Workers.FirstIP = ip.Next().String()
	}
	return err
}

func (w *wizard) editDomain() error {
	d, t := &w.c.Spec.DNS, &w.c.Spec.TLS
	if err := w.ask(
		w.input("Base domain", "Apps are published as <app>.<domain>, e.g. grafana."+d.BaseDomain+".", &d.BaseDomain, nonEmpty),
		huh.NewSelect[string]().Title("DNS records").Description("Who creates the DNS records for new apps.").
			Options(huh.NewOption("None — I manage DNS myself", "none"),
				huh.NewOption("Cloudflare (API token)", "cloudflare"),
				huh.NewOption("RFC 2136 dynamic updates (BIND, Technitium, PowerDNS)", "rfc2136")).Value(&d.Provider),
		huh.NewSelect[string]().Title("Certificates").
			Options(huh.NewOption("Internal CA — works offline, clients must trust the CA", "internal-ca"),
				huh.NewOption("Let's Encrypt — trusted everywhere, needs a DNS provider", "letsencrypt")).Value(&t.Issuer),
	); err != nil {
		return err
	}

	d.RFC2136 = nil
	if d.Provider == "rfc2136" {
		r := &config.RFC2136{Server: w.c.Spec.Network.DNSServers[0], Zone: d.BaseDomain}
		if err := w.ask(
			w.input("DNS server for updates", "", &r.Server, validAddr),
			w.input("Zone", "", &r.Zone, nonEmpty),
		); err != nil {
			return err
		}
		d.RFC2136 = r
	}
	if t.Issuer != "letsencrypt" {
		t.ACMEEmail = ""
		return nil
	}
	return w.ask(w.input("E-mail for Let's Encrypt", "Expiry warnings go here.", &t.ACMEEmail, nonEmpty))
}

func (w *wizard) editComponents() error {
	sel := w.cat.Selection(w.c.Spec.Components)
	choices := map[string]*string{}
	var groups []*huh.Group
	for i := range w.cat.Categories {
		cat := &w.cat.Categories[i]
		choice := sel[cat.ID]
		choices[cat.ID] = &choice
		var opts []huh.Option[string]
		for _, o := range cat.Options {
			opts = append(opts, huh.NewOption(optionLabel(o, cat.Default), o.ID))
		}
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().Title(fmt.Sprintf("%s (%d of %d)", cat.Title, i+1, len(w.cat.Categories))).
				Description(cat.Question).Options(opts...).Value(&choice).
				Validate(func(v string) error {
					if o, ok := cat.Option(v); ok && o.Status != catalog.Supported {
						return fmt.Errorf("%s is planned, not supported yet", o.Name)
					}
					return nil
				}),
			huh.NewNote().DescriptionFunc(func() string { return optionDetails(cat, choice) }, &choice),
		))
	}
	if err := w.form(groups...); err != nil {
		return err
	}
	w.c.Spec.Components = map[string]string{}
	for id, v := range choices {
		w.c.Spec.Components[id] = *v
	}
	return nil
}

func (w *wizard) editAccess() error {
	s := &w.c.Spec
	keys := strings.Join(s.Access.SSHPublicKeys, ", ")
	if keys == "" {
		keys = sshkey.LocalPublic()
	}
	err := w.ask(
		w.input("Admin user on the nodes", "Created on every node with sudo; root login is disabled.", &s.Access.AdminUser, nonEmpty),
		w.input("SSH public keys", "Comma separated. Password login is disabled.", &keys, nonEmpty),
		w.input("Time zone", "", &s.Access.Timezone, nil),
		w.input("GitOps repository", "Argo CD reads the platform from here; a fork of kubeanvil works.", &s.GitOps.RepoURL, nonEmpty),
		w.input("Branch", "", &s.GitOps.Revision, nonEmpty),
	)
	s.Access.SSHPublicKeys = splitList(keys)
	return err
}

func (w *wizard) editBackup() error {
	s := &w.c.Spec
	enabled := s.Backup.S3 != nil
	if err := w.ask(huh.NewConfirm().Title("Back up the cluster to S3-compatible storage (Velero)?").Value(&enabled)); err != nil {
		return err
	}
	if !enabled {
		s.Backup.S3 = nil
		return nil
	}
	b := s.Backup.S3
	if b == nil {
		b = &config.S3{Region: "us-east-1"}
	}
	if err := w.ask(
		w.input("S3 endpoint", "MinIO, RustFS, Garage or a cloud provider.", &b.Endpoint, prefixed("http")),
		w.input("Region", "", &b.Region, nil),
		w.input("Bucket", "", &b.Bucket, nonEmpty),
	); err != nil {
		return err
	}
	s.Backup.S3 = b
	return nil
}

// --- helpers for the sections ----------------------------------------------------------------

func (w *wizard) inUse() map[netip.Addr]bool {
	if w.host == nil {
		return nil
	}
	return w.host.inUse
}

// fittingPreset is the largest preset that fits in three quarters of the host's free
// memory, leaving room for other VMs, else the smallest.
func (w *wizard) fittingPreset() Preset {
	best := presets[0]
	for _, p := range presets {
		if presetMiB(p) <= w.host.freeMiB()*3/4 {
			best = p
		}
	}
	return best
}

func (w *wizard) applyPreset(p Preset) {
	nodes := &w.c.Spec.Nodes
	block := nodes.ControlPlane.Count + nodes.Workers.Count
	cpIP, wkIP := nodes.ControlPlane.FirstIP, nodes.Workers.FirstIP
	nodes.ControlPlane, nodes.Workers = p.ControlPlane, p.Workers
	nodes.ControlPlane.FirstIP, nodes.Workers.FirstIP = cpIP, wkIP
	if nodes.ControlPlane.Count+nodes.Workers.Count != block || nodes.ControlPlane.FirstIP == "" {
		layoutIPs(w.c, w.inUse())
	}
}

// storageField picks from the host's storages when known, otherwise asks for a name.
func (w *wizard) storageField(title, content string, dst *string) huh.Field {
	if w.host != nil {
		if list := w.host.storages(content); len(list) > 0 {
			var opts []huh.Option[string]
			for _, s := range list {
				label := fmt.Sprintf("%-14s %-8s %s free", s.Storage, s.Type, mib(int(s.Avail>>20)))
				if s.Shared == 1 {
					label += " · shared"
				}
				opts = append(opts, huh.NewOption(label, s.Storage))
			}
			return huh.NewSelect[string]().Title(title).Options(opts...).Value(dst)
		}
	}
	return w.input(title, fmt.Sprintf("A storage with the %q content type.", content), dst, nonEmpty)
}

func (w *wizard) bridgeField(dst *string) huh.Field {
	if w.host != nil && len(w.host.Network) > 0 {
		var opts []huh.Option[string]
		for _, b := range w.host.Network {
			label := fmt.Sprintf("%-8s %s", b.Iface, b.CIDR)
			if b.Gateway != "" {
				label += " · gateway " + b.Gateway
			}
			opts = append(opts, huh.NewOption(label, b.Iface))
		}
		return huh.NewSelect[string]().Title("Network bridge").Options(opts...).Value(dst)
	}
	return w.input("Network bridge", "", dst, nonEmpty)
}

// summary adds what was learnt from the host to Summary.
func (w *wizard) summary() string {
	out := Summary(w.c, w.cat)
	h := w.host
	if h == nil {
		return out + "\n\n  Addresses were not checked: keep them free and outside your DHCP range."
	}
	var b strings.Builder
	b.WriteString(out + "\n\n")
	need := w.c.Spec.Nodes.ControlPlane.Count*w.c.Spec.Nodes.ControlPlane.MemoryMiB + w.c.Spec.Nodes.Workers.Count*w.c.Spec.Nodes.Workers.MemoryMiB
	if need > h.freeMiB() {
		fmt.Fprintf(&b, "  ⚠ The VMs need %s; %s has only %s free.\n", mib(need), h.Node, mib(h.freeMiB()))
	} else {
		fmt.Fprintf(&b, "  %s has %s free; %s remain after this cluster.\n", h.Node, mib(h.freeMiB()), mib(h.freeMiB()-need))
	}
	configs := "this Proxmox cluster"
	if len(h.others) > 0 {
		configs += " or on " + strings.Join(h.others, ", ")
	}
	fmt.Fprintf(&b, "  Addresses picked from those that neither answer on the LAN nor are set on a VM in %s.\n"+
		"  Switched-off machines elsewhere cannot be seen: keep the range outside DHCP.", configs)
	for _, n := range h.notes {
		b.WriteString("\n  ⚠ " + n)
	}
	return b.String()
}

// --- presentation ------------------------------------------------------------------------

func optionLabel(o catalog.Component, def string) string {
	var parts []string
	switch {
	case o.Status != catalog.Supported:
		parts = append(parts, "planned")
	case o.ID == "none":
		parts = append(parts, "nothing to run")
	default:
		if o.AppVersion != "" {
			parts = append(parts, "v"+strings.TrimPrefix(o.AppVersion, "v"))
		}
		if o.MemoryMiB > 0 {
			parts = append(parts, "~"+mib(o.MemoryMiB))
		} else {
			parts = append(parts, "built in")
		}
	}
	meta := strings.Join(parts, " · ")
	if o.ID == def {
		meta += " · recommended"
	}
	return fmt.Sprintf("%-26s %s", o.Name, meta)
}

func optionDetails(cat *catalog.Category, id string) string {
	o, ok := cat.Option(id)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(o.Summary + "\n")
	for _, p := range o.Pros {
		b.WriteString("  + " + p + "\n")
	}
	for _, c := range o.Cons {
		b.WriteString("  − " + c + "\n")
	}
	if o.License != "" {
		fmt.Fprintf(&b, "  %s · CNCF %s", o.License, o.CNCF)
	}
	return b.String()
}

// Summary describes the cluster and checks that the platform fits on the workers.
func Summary(c *config.Cluster, cat *catalog.Catalog) string {
	s := c.Spec
	p := s.Provider.Proxmox
	cp, wk := s.Nodes.ControlPlane, s.Nodes.Workers
	vmMiB := cp.Count*cp.MemoryMiB + wk.Count*wk.MemoryMiB
	platform := cat.MemoryMiB(s.Components)
	appNodes, appMiB := "workers", wk.Count*wk.MemoryMiB
	if wk.Count == 0 {
		appNodes, appMiB = "control planes", cp.Count*cp.MemoryMiB
	}
	nodes, _ := c.Nodes()
	var first, last config.Node
	if len(nodes) > 0 {
		first, last = nodes[0], nodes[len(nodes)-1]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s · K3s %s on Proxmox node %s\n", c.Metadata.Name, s.Kubernetes.Version, p.Node)
	fmt.Fprintf(&b, "  Nodes        %s (%d vCPU, %s)", count(cp.Count, "control plane"), cp.CPU, mib(cp.MemoryMiB))
	if wk.Count > 0 {
		fmt.Fprintf(&b, " · %s (%d vCPU, %s)", count(wk.Count, "worker"), wk.CPU, mib(wk.MemoryMiB))
	}
	fmt.Fprintf(&b, "\n  Memory       %s for the VMs · platform ~%s of %s on the %s\n", mib(vmMiB), mib(platform), mib(appMiB), appNodes)
	fmt.Fprintf(&b, "  Proxmox      %s · disks on %s · bridge %s · VMIDs %d–%d\n", p.Endpoint, p.Datastores.Disks, p.Bridge, first.VMID, last.VMID)
	fmt.Fprintf(&b, "  Addresses    API %s · nodes %s–%s · LoadBalancer %s–%s\n", s.Network.APIVIP, first.IP, last.IP,
		s.Network.LoadBalancerRange.Start, s.Network.LoadBalancerRange.End)
	fmt.Fprintf(&b, "  Apps         https://<app>.%s · %s certificates · DNS records: %s\n", s.DNS.BaseDomain, s.TLS.Issuer, s.DNS.Provider)
	backup := "none"
	if s.Backup.S3 != nil {
		backup = s.Backup.S3.Bucket + " at " + s.Backup.S3.Endpoint
	}
	fmt.Fprintf(&b, "  Backups      %s\n", backup)
	fmt.Fprintf(&b, "  Components   %s", wrap(componentNames(c, cat), 80, "\n               "))
	if appMiB > 0 && platform*100/appMiB > 75 {
		b.WriteString("\n  ⚠ Little room left for your own applications: add workers or memory.")
	}
	return b.String()
}

// componentNames lists the chosen components in catalog order, skipping "none".
func componentNames(c *config.Cluster, cat *catalog.Catalog) string {
	sel := cat.Selection(c.Spec.Components)
	var names []string
	for i := range cat.Categories {
		if o, ok := cat.Categories[i].Option(sel[cat.Categories[i].ID]); ok && o.ID != "none" {
			names = append(names, o.Name)
		}
	}
	return strings.Join(names, ", ")
}

// wrap breaks a comma-separated list into lines of at most width characters.
func wrap(list string, width int, sep string) string {
	var lines []string
	line := ""
	for _, item := range strings.Split(list, ", ") {
		if line != "" && len(line)+2+len(item) > width {
			lines = append(lines, line+",")
			line = ""
		}
		if line != "" {
			line += ", "
		}
		line += item
	}
	return strings.Join(append(lines, line), sep)
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func samePool(a, b config.Pool) bool {
	a.FirstIP, b.FirstIP = "", ""
	return a == b
}

// --- inputs and validators ----------------------------------------------------------------

type textField struct {
	value    *string
	proposed string
}

type intField struct {
	text  string
	dst   *int
	scale int // the answer is multiplied by this: 1024 to ask in GiB for a MiB value
}

// input is a text question whose current value is the proposed answer. check may be nil.
func (w *wizard) input(title, desc string, value *string, check func(string) error) *huh.Input {
	f := &textField{value: value, proposed: *value}
	w.texts = append(w.texts, f)
	in := huh.NewInput().Title(title).Description(desc)
	if f.proposed != "" {
		if w.opts.Accessible {
			in.Title(title + " [" + f.proposed + "]") // accessible prompts do not print descriptions
		} else {
			in.Placeholder(f.proposed) // typing replaces the proposal; Enter keeps it
			*value = ""
		}
	}
	return in.Value(value).Validate(func(s string) error {
		if strings.TrimSpace(s) == "" && f.proposed != "" {
			return nil
		}
		if check != nil {
			return check(s)
		}
		return nil
	})
}

func (w *wizard) intInput(title, desc string, dst *int) *huh.Input {
	return w.scaledInput(title, desc, dst, 1)
}

func (w *wizard) gibInput(title, desc string, dstMiB *int) *huh.Input {
	return w.scaledInput(title, desc, dstMiB, 1024)
}

func (w *wizard) scaledInput(title, desc string, dst *int, scale int) *huh.Input {
	f := &intField{text: strconv.Itoa(*dst / scale), dst: dst, scale: scale}
	w.ints = append(w.ints, f)
	return w.input(title, desc, &f.text, func(s string) error {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 0 {
			return errors.New("enter a whole number")
		}
		return nil
	})
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

func validName(s string) error {
	if !dnsLabel.MatchString(strings.TrimSpace(s)) {
		return errors.New("use lowercase letters, digits and dashes")
	}
	return nil
}

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	return nil
}

func prefixed(p string) func(string) error {
	return func(s string) error {
		if !strings.HasPrefix(s, p) {
			return fmt.Errorf("must start with %s", p)
		}
		return nil
	}
}

func validAddr(s string) error {
	if _, ok := parseAddr(s); !ok {
		return errors.New("not an IPv4 address")
	}
	return nil
}

func validPrefix(s string) error {
	if _, err := netip.ParsePrefix(strings.TrimSpace(s)); err != nil {
		return errors.New("use CIDR notation, e.g. 192.168.50.0/24")
	}
	return nil
}

func parseAddr(s string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(strings.TrimSpace(s))
	return ip, err == nil && ip.Is4()
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// stdin remembers when piped input runs out: accessible prompts would otherwise re-ask
// the same question forever.
var stdin = &eofReader{r: os.Stdin}

type eofReader struct {
	r   io.Reader
	eof bool
}

func (e *eofReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if errors.Is(err, io.EOF) {
		e.eof = true
	}
	return n, err
}

func stdinClosed() bool { return stdin.eof }
