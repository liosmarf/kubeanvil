// Package kubeanvil holds repository files embedded in the CLI binary, so the binary
// works without a checkout next to it.
package kubeanvil

import _ "embed"

// CatalogYAML is gitops/platform/catalog.yaml: the components the wizard offers and the
// platform chart installs. One file serves both.
//
//go:embed gitops/platform/catalog.yaml
var CatalogYAML []byte
