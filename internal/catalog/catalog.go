// Package catalog reads gitops/platform/catalog.yaml (ADR 0006).
package catalog

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/liosmarf/kubeanvil"
)

type Catalog struct {
	Categories []Category  `yaml:"categories"`
	Core       []Component `yaml:"core"`
}

type Category struct {
	ID       string      `yaml:"id"`
	Title    string      `yaml:"title"`
	Question string      `yaml:"question"`
	Default  string      `yaml:"default"`
	Options  []Component `yaml:"options"`
}

// Component is a selectable option or an always-installed core component.
type Component struct {
	ID         string   `yaml:"id"`
	Name       string   `yaml:"name"`
	Status     string   `yaml:"status"`
	Summary    string   `yaml:"summary"`
	Pros       []string `yaml:"pros"`
	Cons       []string `yaml:"cons"`
	License    string   `yaml:"license"`
	CNCF       string   `yaml:"cncf"`
	MemoryMiB  int      `yaml:"memoryMiB"`
	AppVersion string   `yaml:"appVersion"`
	Chart      *Chart   `yaml:"chart"`
	Requires   []string `yaml:"requires"`
	When       string   `yaml:"when"`
}

type Chart struct {
	Repo    string `yaml:"repo"`
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

const Supported = "supported"

// Load parses the catalog embedded in the binary.
func Load() (*Catalog, error) {
	var c Catalog
	if err := yaml.Unmarshal(kubeanvil.CatalogYAML, &c); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	return &c, nil
}

func (c *Catalog) Category(id string) (*Category, bool) {
	for i := range c.Categories {
		if c.Categories[i].ID == id {
			return &c.Categories[i], true
		}
	}
	return nil, false
}

func (cat *Category) Option(id string) (*Component, bool) {
	for i := range cat.Options {
		if cat.Options[i].ID == id {
			return &cat.Options[i], true
		}
	}
	return nil, false
}

// Selection returns category id -> option id, filling unset categories with their default.
func (c *Catalog) Selection(chosen map[string]string) map[string]string {
	sel := make(map[string]string, len(c.Categories))
	for _, cat := range c.Categories {
		sel[cat.ID] = cat.Default
		if v, ok := chosen[cat.ID]; ok && v != "" {
			sel[cat.ID] = v
		}
	}
	return sel
}

// Check reports unknown categories, unknown or planned options and unmet requirements.
func (c *Catalog) Check(chosen map[string]string) []error {
	var errs []error
	for id := range chosen {
		if _, ok := c.Category(id); !ok {
			errs = append(errs, fmt.Errorf("components.%s: unknown category", id))
		}
	}
	sel := c.Selection(chosen)
	for _, cat := range c.Categories {
		opt, ok := cat.Option(sel[cat.ID])
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("components.%s: unknown option %q", cat.ID, sel[cat.ID]))
			continue
		case opt.Status != Supported:
			errs = append(errs, fmt.Errorf("components.%s: %q is planned, not supported yet", cat.ID, opt.ID))
		}
		for _, req := range opt.Requires { // "category=option"
			k, v, _ := strings.Cut(req, "=")
			if sel[k] != v {
				errs = append(errs, fmt.Errorf("components.%s: %s requires %s", cat.ID, opt.Name, req))
			}
		}
	}
	return errs
}

// MemoryMiB estimates the platform's steady-state memory for a selection, core included.
func (c *Catalog) MemoryMiB(chosen map[string]string) int {
	total := 0
	for _, core := range c.Core {
		total += core.MemoryMiB
	}
	sel := c.Selection(chosen)
	for _, cat := range c.Categories {
		if opt, ok := cat.Option(sel[cat.ID]); ok {
			total += opt.MemoryMiB
		}
	}
	return total
}
