package catalog

import (
	"strings"
	"testing"
)

func TestCatalogIsConsistent(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if errs := c.Check(nil); len(errs) > 0 {
		t.Fatalf("defaults must be supported and satisfy their requirements: %v", errs)
	}
	for _, cat := range c.Categories {
		for _, opt := range cat.Options {
			if opt.Summary == "" || len(opt.Pros) == 0 || len(opt.Cons) == 0 {
				t.Errorf("%s/%s: summary, pros and cons are shown by the wizard and must be set", cat.ID, opt.ID)
			}
			for _, req := range opt.Requires {
				k, v, _ := strings.Cut(req, "=")
				target, ok := c.Category(k)
				if !ok {
					t.Errorf("%s/%s requires unknown category %q", cat.ID, opt.ID, k)
					continue
				}
				if _, ok := target.Option(v); !ok {
					t.Errorf("%s/%s requires unknown option %q", cat.ID, opt.ID, req)
				}
			}
		}
	}
}
