// Command kubeanvil builds a Kubernetes platform from one cluster.yaml.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/liosmarf/kubeanvil/internal/catalog"
	"github.com/liosmarf/kubeanvil/internal/config"
	"github.com/liosmarf/kubeanvil/internal/generate"
	"github.com/liosmarf/kubeanvil/internal/wizard"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0"
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var file string
	root := &cobra.Command{
		Use:           "kubeanvil",
		Short:         "Build a Kubernetes platform from one cluster.yaml",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().StringVarP(&file, "file", "f", "cluster.yaml", "cluster definition")

	var accessible bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Answer a few questions and write cluster.yaml",
		Long: "Asks about the hypervisor, network, node sizes, domain and platform components, then\n" +
			"writes cluster.yaml. Re-running it starts from the current file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cat, err := catalog.Load()
			if err != nil {
				return err
			}
			var existing *config.Cluster
			if _, statErr := os.Stat(file); statErr == nil {
				if existing, err = config.Load(file); err != nil {
					return err
				}
			}
			c, err := wizard.Run(existing, cat, wizard.Options{Accessible: accessible})
			if errors.Is(err, wizard.ErrCancelled) {
				fmt.Fprintln(cmd.OutOrStdout(), "Cancelled. Nothing was written.")
				return nil
			}
			if err != nil {
				return err
			}
			if err := c.Save(file); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nWrote %s. Next: kubeanvil generate\n", file)
			return nil
		},
	}
	initCmd.Flags().BoolVar(&accessible, "accessible", os.Getenv("KUBEANVIL_ACCESSIBLE") != "",
		"plain line-by-line prompts (screen readers, simple terminals, scripted input)")
	root.AddCommand(initCmd)

	root.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Check cluster.yaml for mistakes before anything is created",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, cat, err := load(file)
			if err != nil {
				return err
			}
			nodes, _ := c.Nodes()
			fmt.Fprintf(cmd.OutOrStdout(), "%s is valid: %d nodes, ~%d MiB for platform components\n",
				file, len(nodes), cat.MemoryMiB(c.Spec.Components))
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "generate",
		Short: "Render Terraform, Ansible and GitOps inputs from cluster.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, cat, err := load(file)
			if err != nil {
				return err
			}
			written, err := generate.All(c, cat, ".")
			if err != nil {
				return err
			}
			for _, f := range written {
				fmt.Fprintln(cmd.OutOrStdout(), "wrote", f)
			}
			return nil
		},
	})
	return root
}

// load reads and validates cluster.yaml against the embedded catalog.
func load(file string) (*config.Cluster, *catalog.Catalog, error) {
	cat, err := catalog.Load()
	if err != nil {
		return nil, nil, err
	}
	c, err := config.Load(file)
	if err != nil {
		return nil, nil, err
	}
	if err := config.Validate(c, cat); err != nil {
		return nil, nil, fmt.Errorf("%s has problems:\n%w", file, err)
	}
	return c, cat, nil
}
