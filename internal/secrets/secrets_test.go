package secrets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liosmarf/kubeanvil/internal/config"
)

// Round trip through real sops and age, with a throwaway key.
func TestSaveAndLoad(t *testing.T) {
	for _, tool := range []string{"sops", "age-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	dir := t.TempDir()
	t.Setenv("SOPS_AGE_KEY_FILE", filepath.Join(dir, "keys.txt"))
	if out, err := exec.Command("age-keygen", "-o", KeyFile()).CombinedOutput(); err != nil {
		t.Fatalf("age-keygen: %v %s", err, out)
	}

	path := filepath.Join(dir, "secrets.sops.yaml")
	want := &Secrets{CloudflareAPIToken: "cf-test", BackupS3: &S3{AccessKeyID: "AK", SecretAccessKey: "SK"}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "cf-test") || !strings.Contains(string(data), "ENC[") {
		t.Fatal("secrets.sops.yaml is not encrypted")
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.CloudflareAPIToken != "cf-test" || got.BackupS3 == nil || got.BackupS3.SecretAccessKey != "SK" {
		t.Errorf("Load = %+v", got)
	}
}

func TestMissing(t *testing.T) {
	c := &config.Cluster{}
	c.Spec.DNS.Provider = "cloudflare"
	c.Spec.Backup.S3 = &config.S3{Endpoint: "https://s3.example", Bucket: "b"}
	if got := Missing(c, &Secrets{}); len(got) != 2 {
		t.Errorf("Missing = %v, want the Cloudflare token and the S3 keys", got)
	}
	if got := Missing(c, &Secrets{CloudflareAPIToken: "x", BackupS3: &S3{"a", "b"}}); len(got) != 0 {
		t.Errorf("Missing = %v, want none", got)
	}
}

func TestLoadWithoutFile(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil || s == nil || s.CloudflareAPIToken != "" {
		t.Errorf("Load of a missing file = %+v, %v; want empty secrets", s, err)
	}
}
