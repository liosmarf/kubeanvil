// Package wizard asks the questions that produce a cluster.yaml (kubeanvil init).
package wizard

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/liosmarf/kubeanvil/internal/catalog"
	"github.com/liosmarf/kubeanvil/internal/config"
)

// Options tune how the wizard runs.
type Options struct {
	Accessible bool // plain line-by-line prompts (screen readers, dumb terminals, scripted input)
}

// Run asks for every setting, starting from existing (or defaults when nil), and returns a
// cluster that passes config.Validate. The caller decides where to save it.
func Run(existing *config.Cluster, cat *catalog.Catalog, opts Options) (*config.Cluster, error) {
	c := existing
	if c == nil {
		c = newCluster()
	}
	w := &wizard{c: c, cat: cat, opts: opts, edit: existing != nil}
	for {
		if err := w.ask(); err != nil {
			return nil, err
		}
		err := config.Validate(c, cat)
		if err == nil {
			return c, nil
		}
		if w.opts.Accessible && stdinClosed() {
			return nil, fmt.Errorf("cluster definition is not valid:\n%w", err)
		}
		retry := true
		if ferr := w.form(huh.NewGroup(
			huh.NewNote().Title("Some answers need fixing").Description(err.Error()),
			huh.NewConfirm().Title("Go through the questions again with your answers kept?").Value(&retry),
		)); ferr != nil {
			return nil, ferr
		}
		if !retry {
			return nil, fmt.Errorf("cluster definition is not valid:\n%w", err)
		}
	}
}

type wizard struct {
	edit bool // re-running on an existing cluster.yaml
	c    *config.Cluster
	cat  *catalog.Catalog
	opts Options
	// Inputs of the current step. Accessible prompts return "" for a bare Enter; these
	// remember the proposed value so Enter keeps it, as it does in the visual form.
	texts []*textField
	ints  []*intField
}

// ask runs every step, then shows the summary; "Start over" repeats the steps with the
// answers given so far as the proposed values.
func (w *wizard) ask() error {
	steps := []func() error{w.basics, w.proxmox, w.network, w.nodes, w.domain, w.components, w.access}
	for {
		for _, step := range steps {
			if err := step(); err != nil {
				return err
			}
		}
		done, err := w.summary()
		if err != nil || done {
			return err
		}
	}
}

// form runs one screen and copies numeric answers back into the cluster.
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
		return errors.New("aborted, nothing written")
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
			*f.dst = n
		}
	}
	w.texts, w.ints = nil, nil
	return nil
}

// --- steps -----------------------------------------------------------------------------

func (w *wizard) basics() error {
	preset := "production"
	var opts []huh.Option[string]
	if w.edit {
		preset = "keep"
		opts = append(opts, huh.NewOption(fmt.Sprintf("%-16s %s", "Current", "Keep the node counts and sizes in cluster.yaml."), "keep"))
	}
	for _, p := range presets {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%-16s %s", p.Title, p.Description), p.ID))
	}
	err := w.form(huh.NewGroup(
		w.input("Cluster name", "Lowercase; used for VM names like <name>-cp-01.", &w.c.Metadata.Name, nonEmpty),
		huh.NewSelect[string]().Title("Starting size").Description("Every value can be adjusted afterwards.").
			Options(opts...).Value(&preset),
	))
	for _, p := range presets {
		if p.ID == preset {
			w.c.Spec.Nodes.ControlPlane, w.c.Spec.Nodes.Workers = p.ControlPlane, p.Workers
			layoutIPs(w.c)
		}
	}
	return err
}

func (w *wizard) proxmox() error {
	p := w.c.Spec.Provider.Proxmox
	return w.form(huh.NewGroup(
		huh.NewNote().Title("Proxmox VE").Description("Where the VMs are created. vSphere and Azure are planned."),
		w.input("API endpoint", "", &p.Endpoint, prefixed("https://")),
		w.input("Node", "The Proxmox node that hosts the VMs.", &p.Node, nonEmpty),
		w.input("Datastore for VM disks", "", &p.Datastores.Disks, nonEmpty),
		w.input("Datastore for the cloud image (content: import)", "", &p.Datastores.Images, nonEmpty),
		w.input("Datastore for cloud-init snippets (content: snippets)", "", &p.Datastores.Snippets, nonEmpty),
		w.input("Network bridge", "", &p.Bridge, nonEmpty),
		w.intInput("First VMID", "Control planes get base+1…, workers base+11….", &p.VMIDBase),
	))
}

func (w *wizard) network() error {
	n := &w.c.Spec.Network
	cidr := n.CIDR
	if err := w.form(huh.NewGroup(
		w.input("Node subnet", "The LAN the VMs join, e.g. 192.168.50.0/24.", &cidr, validPrefix),
	)); err != nil {
		return err
	}
	if cidr != n.CIDR {
		setNetwork(w.c, cidr)
		layoutIPs(w.c)
	}
	dns := strings.Join(n.DNSServers, ", ")
	err := w.form(huh.NewGroup(
		huh.NewNote().Title("Addresses").Description("Proposed from the subnet. Keep them unless they clash with something on your LAN."),
		w.input("Gateway", "", &n.Gateway, validAddr),
		w.input("DNS servers", "Comma separated.", &dns, nonEmpty),
		w.input("Search domain", "Node FQDNs become <node>.<search domain>.", &n.SearchDomain, nonEmpty),
		w.input("Kubernetes API VIP", "A free address that floats between control planes.", &n.APIVIP, validAddr),
		w.input("LoadBalancer range start", "Free addresses for Ingress and LoadBalancer Services.", &n.LoadBalancerRange.Start, validAddr),
		w.input("LoadBalancer range end", "", &n.LoadBalancerRange.End, validAddr),
	))
	n.DNSServers = splitList(dns)
	return err
}

func (w *wizard) nodes() error {
	cp, wk := &w.c.Spec.Nodes.ControlPlane, &w.c.Spec.Nodes.Workers
	cpCount := strconv.Itoa(cp.Count)
	err := w.form(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Control planes").
				Options(huh.NewOption("1 — no high availability", "1"), huh.NewOption("3 — survives one failure", "3"),
					huh.NewOption("5 — survives two failures", "5")).Value(&cpCount),
			w.intInput("Control plane vCPUs", "", &cp.CPU),
			w.intInput("Control plane memory (MiB)", "4096 is enough for K3s and etcd; 8192 leaves headroom.", &cp.MemoryMiB),
			w.intInput("Control plane disk (GiB)", "", &cp.OSDiskGiB),
		).Title("Control plane"),
		huh.NewGroup(
			w.intInput("Workers", "Applications and platform components run here. 0 runs everything on the control planes.", &wk.Count),
			w.intInput("Worker vCPUs", "", &wk.CPU),
			w.intInput("Worker memory (MiB)", "", &wk.MemoryMiB),
			w.intInput("Worker OS disk (GiB)", "", &wk.OSDiskGiB),
			w.intInput("Worker data disk for Longhorn (GiB)", "0 for none.", &wk.DataDiskGiB),
		).Title("Workers"),
	)
	if n, _ := strconv.Atoi(cpCount); n != cp.Count {
		cp.Count = n
	}
	cp.FirstIP = w.c.Spec.Network.APIVIP
	if vip, ok := parseAddr(cp.FirstIP); ok { // nodes follow the VIP: VIP+1, VIP+2, …
		cp.FirstIP = vip.Next().String()
		next := vip.Next()
		for range cp.Count {
			next = next.Next()
		}
		wk.FirstIP = next.String()
	}
	return err
}

func (w *wizard) domain() error {
	d, t := &w.c.Spec.DNS, &w.c.Spec.TLS
	if err := w.form(huh.NewGroup(
		w.input("Base domain", "Apps are published as <app>.<domain>, e.g. grafana.k8s.example.com.", &d.BaseDomain, nonEmpty),
		huh.NewSelect[string]().Title("DNS records").Description("Who creates DNS records for new apps.").
			Options(huh.NewOption("Cloudflare (API token)", "cloudflare"),
				huh.NewOption("RFC 2136 dynamic updates (BIND, Technitium, PowerDNS)", "rfc2136"),
				huh.NewOption("None — I manage DNS myself", "none")).Value(&d.Provider),
		huh.NewSelect[string]().Title("Certificates").
			Options(huh.NewOption("Let's Encrypt — trusted everywhere, needs a DNS provider", "letsencrypt"),
				huh.NewOption("Internal CA — works offline, clients must trust the CA", "internal-ca")).Value(&t.Issuer),
	).Title("Domain and TLS")); err != nil {
		return err
	}

	d.RFC2136 = nil
	if d.Provider == "rfc2136" {
		r := &config.RFC2136{Server: w.c.Spec.Network.Gateway, Zone: d.BaseDomain}
		if err := w.form(huh.NewGroup(
			w.input("DNS server for updates", "", &r.Server, validAddr),
			w.input("Zone", "", &r.Zone, nonEmpty),
		).Title("RFC 2136")); err != nil {
			return err
		}
		d.RFC2136 = r
	}

	if t.Issuer != "letsencrypt" {
		t.ACMEEmail = ""
		return nil
	}
	return w.form(huh.NewGroup(w.input("Let's Encrypt account e-mail", "", &t.ACMEEmail, nonEmpty)))
}

func (w *wizard) components() error {
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
			huh.NewSelect[string]().Title(cat.Title).Description(cat.Question).Options(opts...).Value(&choice).
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

func (w *wizard) access() error {
	s := &w.c.Spec
	keys := strings.Join(s.Access.SSHPublicKeys, ", ")
	if keys == "" {
		keys = localPublicKey()
	}
	useBackup := s.Backup.S3 != nil
	if err := w.form(
		huh.NewGroup(
			w.input("Admin user on the nodes", "", &s.Access.AdminUser, nonEmpty),
			w.input("SSH public keys", "Comma separated. Password login is disabled.", &keys, nonEmpty),
			w.input("Time zone", "", &s.Access.Timezone, nil),
		).Title("Access"),
		huh.NewGroup(
			w.input("GitOps repository", "Argo CD reads the platform from here; a fork of kubeanvil works.", &s.GitOps.RepoURL, nonEmpty),
			w.input("Branch", "", &s.GitOps.Revision, nonEmpty),
			huh.NewConfirm().Title("Back up to S3-compatible storage (Velero)?").Value(&useBackup),
		).Title("GitOps and backups"),
	); err != nil {
		return err
	}
	s.Access.SSHPublicKeys = splitList(keys)

	if !useBackup {
		s.Backup.S3 = nil
		return nil
	}
	b := s.Backup.S3
	if b == nil {
		b = &config.S3{Region: "us-east-1"}
	}
	if err := w.form(huh.NewGroup(
		w.input("S3 endpoint", "", &b.Endpoint, prefixed("http")),
		w.input("Region", "", &b.Region, nil),
		w.input("Bucket", "", &b.Bucket, nonEmpty),
	).Title("Backups")); err != nil {
		return err
	}
	s.Backup.S3 = b
	return nil
}

func (w *wizard) summary() (bool, error) {
	ok := true
	err := w.form(huh.NewGroup(
		huh.NewNote().Title("Summary").Description(Summary(w.c, w.cat)),
		huh.NewConfirm().Title("Write cluster.yaml?").Affirmative("Write").Negative("Start over").Value(&ok),
	))
	return ok, err
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
	cp, wk := s.Nodes.ControlPlane, s.Nodes.Workers
	vmMiB := cp.Count*cp.MemoryMiB + wk.Count*wk.MemoryMiB
	platform := cat.MemoryMiB(s.Components)
	appNodes, appMiB := "workers", wk.Count*wk.MemoryMiB
	if wk.Count == 0 {
		appNodes, appMiB = "control planes", cp.Count*cp.MemoryMiB
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s on %s/%s · K3s %s\n", c.Metadata.Name, s.Provider.Type, s.Provider.Proxmox.Node, s.Kubernetes.Version)
	fmt.Fprintf(&b, "  %d control plane × %d vCPU, %s · %d worker × %d vCPU, %s\n",
		cp.Count, cp.CPU, mib(cp.MemoryMiB), wk.Count, wk.CPU, mib(wk.MemoryMiB))
	fmt.Fprintf(&b, "  API https://%s:6443 · apps https://<app>.%s (%s)\n", s.Network.APIVIP, s.DNS.BaseDomain, s.TLS.Issuer)
	fmt.Fprintf(&b, "  Hypervisor memory for VMs: %s\n", mib(vmMiB))
	fmt.Fprintf(&b, "  Platform components: ~%s of %s on the %s", mib(platform), mib(appMiB), appNodes)
	if appMiB > 0 && platform*100/appMiB > 75 {
		b.WriteString("\n  ⚠ Little room left for your own applications: add workers or memory.")
	}
	return b.String()
}

// --- inputs and validators ----------------------------------------------------------------

type textField struct {
	value    *string
	proposed string
}

type intField struct {
	text string
	dst  *int
}

// input is a text question whose current value is the proposed answer. check may be nil.
func (w *wizard) input(title, desc string, value *string, check func(string) error) *huh.Input {
	f := &textField{value: value, proposed: *value}
	w.texts = append(w.texts, f)
	if w.opts.Accessible && f.proposed != "" {
		title += " [" + f.proposed + "]" // accessible prompts do not print descriptions
	}
	return huh.NewInput().Title(title).Description(desc).Value(value).Validate(func(s string) error {
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
	f := &intField{text: strconv.Itoa(*dst), dst: dst}
	w.ints = append(w.ints, f)
	return w.input(title, desc, &f.text, func(s string) error {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 0 {
			return errors.New("enter a whole number")
		}
		return nil
	})
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

// localPublicKey offers the user's own SSH key as the default admin key: a standard
// id_<type>.pub first, otherwise any other id_*.pub.
func localPublicKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidates := []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"}
	if others, _ := filepath.Glob(filepath.Join(home, ".ssh", "id_*.pub")); others != nil {
		for _, o := range others {
			candidates = append(candidates, filepath.Base(o))
		}
	}
	for _, name := range candidates {
		if data, err := os.ReadFile(filepath.Join(home, ".ssh", name)); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}
