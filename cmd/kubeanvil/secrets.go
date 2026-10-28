package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/liosmarf/kubeanvil/internal/secrets"
)

func secretsCmd(file *string) *cobra.Command {
	return &cobra.Command{
		Use:   "secrets",
		Short: "Enter the secrets the cluster needs; writes secrets.sops.yaml, encrypted",
		Long: "Asks, with hidden input, only for the secrets your cluster.yaml choices need (DNS\n" +
			"provider token, backup S3 keys, a login for a private GitOps repository) plus an optional\n" +
			"Proxmox API token, and encrypts them with SOPS and your age key. Enter keeps a value.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := load(*file)
			if err != nil {
				return err
			}
			path := secrets.Path(*file)
			s, err := secrets.Load(path)
			if err != nil {
				return err
			}

			var fields []huh.Field
			var keep keeper
			hidden := keep.hidden
			fields = append(fields, hidden("Proxmox API token (optional)",
				"user@realm!tokenid=secret. Saves typing it at every apply.", &s.ProxmoxAPIToken))
			if c.Spec.DNS.Provider == "cloudflare" {
				fields = append(fields, hidden("Cloudflare API token", "Zone:DNS:Edit on "+c.Spec.DNS.BaseDomain+"'s zone.", &s.CloudflareAPIToken))
			}
			if c.Spec.Backup.S3 != nil {
				if s.BackupS3 == nil {
					s.BackupS3 = &secrets.S3{}
				}
				fields = append(fields,
					hidden("S3 access key ID", "For bucket "+c.Spec.Backup.S3.Bucket+".", &s.BackupS3.AccessKeyID),
					hidden("S3 secret access key", "", &s.BackupS3.SecretAccessKey))
			}
			private := s.GitOps != nil
			fields = append(fields, huh.NewConfirm().Title("Does Argo CD need a login to read "+c.Spec.GitOps.RepoURL+"?").Value(&private))
			groups := make([]*huh.Group, len(fields))
			for i, f := range fields {
				groups[i] = huh.NewGroup(f)
			}
			if err := huh.NewForm(groups...).Run(); err != nil {
				return declinedNothingWritten(cmd, err)
			}
			keep.restore()
			if private {
				if s.GitOps == nil {
					s.GitOps = &secrets.GitLogin{}
				}
				if err := huh.NewForm(
					huh.NewGroup(huh.NewInput().Title("Git username").Value(&s.GitOps.Username)),
					huh.NewGroup(hidden("Git password or read-only token", "", &s.GitOps.Password)),
				).Run(); err != nil {
					return declinedNothingWritten(cmd, err)
				}
				keep.restore()
			} else {
				s.GitOps = nil
			}

			if _, err := secrets.Recipient(); err != nil {
				if err := createAgeKey(cmd); err != nil {
					return err
				}
			}
			if err := secrets.Save(path, s); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s, encrypted for the age key in %s.\n", path, secrets.KeyFile())
			return nil
		},
	}
}

// keeper remembers current values, so a hidden input left empty keeps them.
type keeper []func()

// hidden is a password-style input; an existing value is never shown.
func (k *keeper) hidden(title, desc string, value *string) *huh.Input {
	if current := *value; current != "" {
		desc += " Enter keeps the current value."
		*value = ""
		*k = append(*k, func() {
			if *value == "" {
				*value = current
			}
		})
	}
	return huh.NewInput().Title(title).Description(desc).EchoMode(huh.EchoModePassword).Value(value)
}

func (k *keeper) restore() {
	for _, f := range *k {
		f()
	}
	*k = nil
}

// createAgeKey offers to create the key that encrypts secrets.sops.yaml.
func createAgeKey(cmd *cobra.Command) error {
	path := secrets.KeyFile()
	ok := false
	err := huh.NewConfirm().Title("No age key yet. Create one at " + path + "?").
		Description("Keep a copy in your password manager: without it secrets.sops.yaml cannot be decrypted.").
		Value(&ok).Run()
	if err != nil || !ok {
		return errors.New("an age key is needed to encrypt the secrets; nothing was written")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	gen := exec.Command("age-keygen", "-o", path)
	gen.Stderr = cmd.ErrOrStderr()
	return gen.Run()
}

func declinedNothingWritten(cmd *cobra.Command, err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Fprintln(cmd.OutOrStdout(), "Cancelled. Nothing was written.")
		return nil
	}
	return err
}
