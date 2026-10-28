// Package apply turns cluster.yaml into a running cluster and removes it again. Terraform
// creates the VMs, Ansible configures them and bootstraps Argo CD, and the kubeconfig is
// fetched. Every phase says what it is about to change and asks first.
package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/liosmarf/kubeanvil/internal/catalog"
	"github.com/liosmarf/kubeanvil/internal/config"
	"github.com/liosmarf/kubeanvil/internal/generate"
	"github.com/liosmarf/kubeanvil/internal/secrets"
	"github.com/liosmarf/kubeanvil/internal/sshkey"
)

// ErrDeclined means the user answered no; nothing after that point was changed.
var ErrDeclined = errors.New("stopped, nothing more was changed")

type Runner struct {
	Root    string // kubeanvil checkout: terraform/ and ansible/ live here
	Cluster *config.Cluster
	Catalog *catalog.Catalog
	Yes     bool   // answer yes to every confirmation
	Secrets string // path of secrets.sops.yaml; it may not exist
	Out     io.Writer

	tf      string   // tofu or terraform
	env     []string // secrets for Terraform, kept in memory only
	nodeKey string   // private key that opens the nodes
	secrets *secrets.Secrets
}

// Apply creates or updates the cluster. Run again after a failure: finished phases find
// nothing to do.
func (r *Runner) Apply() error {
	name := r.Cluster.Metadata.Name
	nodes, _ := r.Cluster.Nodes()
	cp := r.Cluster.Spec.Nodes.ControlPlane

	r.phase(1, "Generate", "Terraform variables, Ansible inventory and platform values from cluster.yaml")
	if _, err := generate.All(r.Cluster, r.Catalog, r.Root); err != nil {
		return err
	}
	if err := r.preflight(); err != nil {
		return err
	}
	if missing := secrets.Missing(r.Cluster, r.secrets); len(missing) > 0 {
		return fmt.Errorf("cluster.yaml needs secrets that %s does not hold: %s. Run `kubeanvil secrets`",
			filepath.Base(r.Secrets), strings.Join(missing, ", "))
	}
	key, ok := sshkey.ForAuthorized(r.Cluster.Spec.Access.SSHPublicKeys)
	if !ok {
		return errors.New("none of access.sshPublicKeys has its private key in ~/.ssh: Ansible could not reach the nodes")
	}
	r.nodeKey = key
	if err := r.checkRepository(); err != nil {
		return err
	}

	r.phase(2, "Terraform", fmt.Sprintf("%d VMs on Proxmox node %s", len(nodes), r.Cluster.Spec.Provider.Proxmox.Node))
	if err := r.terraformInit(); err != nil {
		return err
	}
	plan := filepath.Join(r.dir(), "tfplan")
	changes, err := r.terraformPlan("plan", "-out="+plan)
	if err != nil {
		return err
	}
	if changes {
		if err := r.confirm("Apply this plan to Proxmox?"); err != nil {
			return err
		}
		if err := r.run(r.terraform("apply", "-input=false", plan)); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(r.Out, "The VMs already match cluster.yaml.")
	}

	r.phase(3, "Ansible", "operating system, K3s, Cilium and Argo CD on every node")
	if err := r.confirm(fmt.Sprintf("Configure the %d nodes now?", len(nodes))); err != nil {
		return err
	}
	if err := r.ansible(); err != nil {
		return err
	}

	r.phase(4, "Kubeconfig", "admin access to the API at "+r.Cluster.Spec.Network.APIVIP)
	kubeconfig, err := r.fetchKubeconfig(cp.FirstIP)
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(r.Root, kubeconfig)
	fmt.Fprintf(r.Out, `
%s is up. Argo CD is now installing the platform components; follow it with:

  export KUBECONFIG=%s
  kubectl get nodes
  kubectl -n argocd get applications

Argo CD UI: https://argocd.%s once that name points at the Cilium ingress address
(kubectl -n kube-system get svc cilium-ingress). Password for "admin":
  kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d
`, name, rel, r.Cluster.Spec.DNS.BaseDomain)
	return nil
}

// Destroy deletes every VM of the cluster after the user types its name.
func (r *Runner) Destroy() error {
	name := r.Cluster.Metadata.Name
	nodes, _ := r.Cluster.Nodes()
	if !r.Yes {
		typed := ""
		err := huh.NewInput().Title(fmt.Sprintf("Delete the %d VMs of %s and their disks? Type the cluster name to confirm", len(nodes), name)).
			Value(&typed).Run()
		if err != nil || strings.TrimSpace(typed) != name {
			return ErrDeclined
		}
	}
	if _, err := generate.All(r.Cluster, r.Catalog, r.Root); err != nil {
		return err
	}
	if err := r.preflight(); err != nil {
		return err
	}
	if err := r.terraformInit(); err != nil {
		return err
	}
	if err := r.run(r.terraform("destroy", "-input=false", "-auto-approve", "-var-file="+r.tfvars())); err != nil {
		return err
	}
	for _, f := range []string{"kubeconfig", "tfplan"} {
		os.Remove(filepath.Join(r.dir(), f))
	}
	fmt.Fprintf(r.Out, "\n%s is gone. cluster.yaml and the generated files are kept.\n", name)
	return nil
}

// --- preflight -----------------------------------------------------------------------------

// preflight finds the tools and the credentials before anything is changed.
func (r *Runner) preflight() error {
	for _, tf := range []string{"tofu", "terraform"} {
		if _, err := exec.LookPath(tf); err == nil {
			r.tf = tf
			break
		}
	}
	if r.tf == "" {
		return errors.New("neither tofu nor terraform is installed")
	}
	for _, tool := range []string{"ansible-playbook", "ansible-galaxy", "ssh"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s is not installed", tool)
		}
	}
	if _, err := os.Stat(filepath.Join(r.Root, "terraform", "envs", "proxmox")); err != nil {
		return errors.New("run kubeanvil from the root of a kubeanvil checkout (terraform/ and ansible/ are needed)")
	}

	s, err := secrets.Load(r.Secrets)
	if err != nil {
		return err
	}
	r.secrets = s

	token := os.Getenv("PROXMOX_VE_API_TOKEN")
	if token == "" {
		token = s.ProxmoxAPIToken
	}
	if token == "" {
		err := huh.NewInput().Title("Proxmox API token").
			Description("user@realm!tokenid=secret. Used for this run only, never written to disk.\n" +
				"Set PROXMOX_VE_API_TOKEN to skip this question.").
			EchoMode(huh.EchoModePassword).Value(&token).
			Validate(func(s string) error {
				if !strings.Contains(s, "!") || !strings.Contains(s, "=") {
					return errors.New("expected user@realm!tokenid=secret")
				}
				return nil
			}).Run()
		if err != nil {
			return ErrDeclined
		}
	}
	r.env = append(r.env, "TF_VAR_proxmox_api_token="+strings.TrimSpace(token))

	// Terraform uploads cloud-init snippets over SSH with the key that opens the host.
	p := r.Cluster.Spec.Provider.Proxmox
	u, err := url.Parse(p.Endpoint)
	if err != nil {
		return err
	}
	if key, ok := keyFor(p.SSHUser, u.Hostname()); ok {
		data, err := os.ReadFile(key)
		if err != nil {
			return err
		}
		r.env = append(r.env, "TF_VAR_proxmox_ssh_private_key="+string(data))
	} else {
		fmt.Fprintf(r.Out, "No key in ~/.ssh opens %s@%s on its own; Terraform will use the SSH agent.\n", p.SSHUser, u.Hostname())
	}

	return nil
}

// keyFor returns the first private key that logs in to user@host without a password.
func keyFor(user, host string) (string, bool) {
	for _, key := range sshkey.Private() {
		cmd := exec.Command("ssh", "-i", key, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
			"-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=accept-new", user+"@"+host, "true")
		if cmd.Run() == nil {
			return key, true
		}
	}
	return "", false
}

// checkRepository warns when Argo CD will not be able to read the GitOps repository.
func (r *Runner) checkRepository() error {
	repo := r.Cluster.Spec.GitOps.RepoURL
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "credential.helper=", "ls-remote", "--exit-code", repo, r.Cluster.Spec.GitOps.Revision)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true", "SSH_ASKPASS=true")
	if cmd.Run() == nil {
		return nil
	}
	fmt.Fprintf(r.Out, "⚠ %s (%s) cannot be read without credentials, so Argo CD will not be able to\n"+
		"  install the platform. Use a public repository or add read-only credentials.\n", repo, r.Cluster.Spec.GitOps.Revision)
	return r.confirm("Continue anyway? Terraform and Ansible still work.")
}

// --- Terraform -------------------------------------------------------------------------------

func (r *Runner) terraform(args ...string) *exec.Cmd {
	cmd := exec.Command(r.tf, append([]string{"-chdir=" + filepath.Join(r.Root, "terraform", "envs", "proxmox")}, args...)...)
	cmd.Env = append(os.Environ(), r.env...)
	// TF_IN_AUTOMATION drops hints like "run tofu apply" that make no sense inside kubeanvil.
	cmd.Env = append(cmd.Env, "TF_DATA_DIR="+filepath.Join(r.dir(), ".terraform"), "TF_IN_AUTOMATION=1")
	return cmd
}

func (r *Runner) terraformInit() error {
	cmd := r.terraform("init", "-input=false", "-reconfigure", "-backend-config=path="+filepath.Join(r.dir(), "terraform.tfstate"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s init failed:\n%s", r.tf, out)
	}
	return nil
}

// terraformPlan shows the plan and reports whether it changes anything.
func (r *Runner) terraformPlan(args ...string) (bool, error) {
	cmd := r.terraform(append(args, "-input=false", "-detailed-exitcode", "-var-file="+r.tfvars())...)
	err := r.run(cmd)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return false, nil
	case errors.As(err, &exit) && exit.ExitCode() == 2:
		return true, nil
	}
	return false, err
}

func (r *Runner) tfvars() string {
	tfvars, _, _ := generate.Paths(r.Cluster.Metadata.Name)
	return filepath.Join(r.Root, tfvars)
}

// --- Ansible and the kubeconfig ---------------------------------------------------------------

func (r *Runner) ansible() error {
	dir := filepath.Join(r.Root, "ansible")
	_, inventory, _ := generate.Paths(r.Cluster.Metadata.Name)
	// An explicit config: Ansible ignores ansible.cfg in a world-writable directory.
	env := append(os.Environ(), "ANSIBLE_CONFIG="+filepath.Join(dir, "ansible.cfg"))

	galaxy := exec.Command("ansible-galaxy", "collection", "install", "-r", "requirements.yml")
	galaxy.Dir, galaxy.Env = dir, env
	if out, err := galaxy.CombinedOutput(); err != nil {
		return fmt.Errorf("installing Ansible collections failed:\n%s", out)
	}

	// Bootstrap secrets reach the argocd role through the environment, never a file.
	bootstrap, err := json.Marshal(r.secrets)
	if err != nil {
		return err
	}
	play := exec.Command("ansible-playbook", "-i", filepath.Join(r.Root, inventory), "--private-key", r.nodeKey, "playbooks/site.yml")
	play.Dir, play.Env = dir, append(env, "KUBEANVIL_SECRETS="+string(bootstrap))
	return r.run(play)
}

// fetchKubeconfig copies the admin kubeconfig from the first control plane and points it at
// the API VIP instead of 127.0.0.1.
func (r *Runner) fetchKubeconfig(host string) (string, error) {
	name := r.Cluster.Metadata.Name
	cmd := exec.Command("ssh", "-i", r.nodeKey, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR",
		r.Cluster.Spec.Access.AdminUser+"@"+host, "sudo cat /etc/rancher/k3s/k3s.yaml")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("reading the kubeconfig from %s: %v %s", host, err, stderr.String())
	}
	kc := strings.NewReplacer(
		"https://127.0.0.1:6443", "https://"+r.Cluster.Spec.Network.APIVIP+":6443",
		"name: default", "name: "+name, "cluster: default", "cluster: "+name,
		"user: default", "user: "+name, "current-context: default", "current-context: "+name,
	).Replace(string(out))
	path := filepath.Join(r.dir(), "kubeconfig")
	return path, os.WriteFile(path, []byte(kc), 0o600)
}

// --- helpers --------------------------------------------------------------------------------

func (r *Runner) dir() string { return filepath.Join(r.Root, "clusters", r.Cluster.Metadata.Name) }

func (r *Runner) phase(n int, title, what string) {
	fmt.Fprintf(r.Out, "\n━━ %d/4 %s ━━ %s\n\n", n, title, what)
}

func (r *Runner) confirm(question string) error {
	if r.Yes {
		return nil
	}
	ok := false
	if err := huh.NewConfirm().Title(question).Affirmative("Yes").Negative("No").Value(&ok).Run(); err != nil || !ok {
		return ErrDeclined
	}
	return nil
}

// run streams a command's output, so the user sees exactly what Terraform and Ansible do.
func (r *Runner) run(cmd *exec.Cmd) error {
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
