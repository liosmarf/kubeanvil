package generate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/liosmarf/kubeanvil/internal/catalog"
	"github.com/liosmarf/kubeanvil/internal/config"
)

// The hand-written files under examples/ and gitops/clusters/example/ document the output
// format; generating from examples/cluster.yaml must reproduce them.
func TestGenerateMatchesExamples(t *testing.T) {
	c, err := config.Load("../../examples/cluster.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := All(c, cat, dir); err != nil {
		t.Fatal(err)
	}
	tfvars, _, gitops := Paths(c.Metadata.Name)

	var got, want map[string]any
	readJSON(t, filepath.Join(dir, tfvars), &got)
	readJSON(t, "../../examples/terraform.tfvars.json", &want)
	delete(want, "//")
	for _, n := range got["nodes"].(map[string]any) { // control planes carry an explicit 0
		if node := n.(map[string]any); node["data_disk_gib"] == 0.0 {
			delete(node, "data_disk_gib")
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("terraform.tfvars.json differs from examples/terraform.tfvars.json")
	}

	readYAML(t, filepath.Join(dir, gitops), &got)
	readYAML(t, "../../gitops/clusters/example/values.yaml", &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GitOps values differ from gitops/clusters/example/values.yaml")
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

func readYAML(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}
