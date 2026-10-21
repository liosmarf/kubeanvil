# 0004 — The `kubeanvil` CLI, in Go

**Status:** accepted · 2026-10-21

## Context

A first cluster means ~40 decisions and three tools run in the right order. New users want
guidance, experienced users want a file, CI wants no prompts.

## Decision

A single Go binary:

| Command | Does |
|---|---|
| `init` | Interactive wizard ([huh](https://github.com/charmbracelet/huh)); writes `cluster.yaml`, pre-filled from an existing one |
| `validate` | Schema and semantic checks |
| `generate` | Renders Terraform, Ansible and GitOps inputs |
| `secrets` | Hidden prompts; writes `secrets.sops.yaml` |
| `plan` / `apply` | Terraform → Ansible → GitOps bootstrap, in order |

Go because it ships as one static binary for Linux, macOS and Windows, like kubectl and helm.

## Consequences

Users need only Terraform or OpenTofu and Ansible besides the binary. Every question in the
wizard maps to a documented field, so `--non-interactive` with a file covers automation.
