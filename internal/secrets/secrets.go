// Package secrets keeps the secrets a cluster needs before it exists (ADR 0002) in
// secrets.sops.yaml, encrypted with SOPS and age. The file may be committed; the age key
// stays outside the repository and outside the cluster.
package secrets

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/liosmarf/kubeanvil/internal/config"
)

// Secrets is the decrypted content of secrets.sops.yaml.
type Secrets struct {
	ProxmoxAPIToken    string    `yaml:"proxmoxApiToken,omitempty" json:"-"` // read by kubeanvil, not passed on
	CloudflareAPIToken string    `yaml:"cloudflareApiToken,omitempty" json:"cloudflareApiToken,omitempty"`
	BackupS3           *S3       `yaml:"backupS3,omitempty" json:"backupS3,omitempty"`
	GitOps             *GitLogin `yaml:"gitops,omitempty" json:"gitops,omitempty"`
}

type S3 struct {
	AccessKeyID     string `yaml:"accessKeyId" json:"accessKeyId"`
	SecretAccessKey string `yaml:"secretAccessKey" json:"secretAccessKey"`
}

// GitLogin is a read-only login for a private GitOps repository over HTTPS.
type GitLogin struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// Path is where the secrets of the cluster defined in clusterFile live: next to it.
func Path(clusterFile string) string {
	return filepath.Join(filepath.Dir(clusterFile), "secrets.sops.yaml")
}

// Missing lists the secrets the cluster's choices need that s does not hold.
func Missing(c *config.Cluster, s *Secrets) []string {
	var missing []string
	if c.Spec.DNS.Provider == "cloudflare" && s.CloudflareAPIToken == "" {
		missing = append(missing, "Cloudflare API token (dns.provider: cloudflare)")
	}
	if c.Spec.Backup.S3 != nil && (s.BackupS3 == nil || s.BackupS3.AccessKeyID == "" || s.BackupS3.SecretAccessKey == "") {
		missing = append(missing, "S3 access key for backups (backup.s3)")
	}
	return missing
}

// Load decrypts path. A missing file is not an error: it returns empty secrets.
func Load(path string) (*Secrets, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return &Secrets{}, nil
	}
	out, err := sops("--decrypt", "--output-type", "yaml", path)
	if err != nil {
		return nil, fmt.Errorf("decrypting %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(out))
	dec.KnownFields(true)
	var s Secrets
	if err := dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) { // EOF: an empty file
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// Save encrypts s for the age key's recipient and writes it to path. The plaintext only
// ever exists in memory and in the pipe to sops.
func Save(path string, s *Secrets) error {
	recipient, err := Recipient()
	if err != nil {
		return err
	}
	plain, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	cmd := exec.Command("sops", "--encrypt", "--age", recipient, "--input-type", "yaml", "--output-type", "yaml", "/dev/stdin")
	cmd.Stdin, cmd.Env = bytes.NewReader(plain), env()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("sops: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return os.WriteFile(path, out, 0o600)
}

// KeyFile is the age key SOPS uses: SOPS_AGE_KEY_FILE, or ~/.config/sops/age/keys.txt.
func KeyFile() string {
	if f := os.Getenv("SOPS_AGE_KEY_FILE"); f != "" {
		return f
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sops", "age", "keys.txt")
}

// Recipient reads the public key from the age key file.
func Recipient() (string, error) {
	f, err := os.Open(KeyFile())
	if err != nil {
		return "", fmt.Errorf("no age key at %s: create one with `age-keygen -o %s` and keep a copy in your password manager", KeyFile(), KeyFile())
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if pub, ok := strings.CutPrefix(sc.Text(), "# public key: "); ok {
			return strings.TrimSpace(pub), nil
		}
	}
	return "", fmt.Errorf("%s has no \"# public key:\" line", KeyFile())
}

func sops(args ...string) ([]byte, error) {
	cmd := exec.Command("sops", args...)
	cmd.Env = env()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%v %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// env points SOPS at the same key file everywhere; on macOS it would otherwise look under
// ~/Library/Application Support.
func env() []string { return append(os.Environ(), "SOPS_AGE_KEY_FILE="+KeyFile()) }
