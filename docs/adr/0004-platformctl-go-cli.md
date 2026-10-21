# ADR 0004 — `platformctl`, an interactive CLI written in Go

- Status: accepted
- Date: 2026-10-21

## Context

Setting up a cluster from this repository requires answering ~40 questions (provider endpoint,
network layout, sizing, domain, certificates, optional components) and then running Terraform,
Ansible and the GitOps bootstrap in order. Experienced users want a file; first-time users want
guidance; CI wants neither prompts nor surprises.

## Decision

Provide `platformctl`, a single static binary written in Go:

| Command | Purpose |
|---|---|
| `init` | Interactive wizard ([charmbracelet/huh](https://github.com/charmbracelet/huh)); writes `cluster.yaml`, pre-filling answers from an existing file |
| `validate` | Schema + semantic validation of `cluster.yaml` |
| `generate` | Renders tfvars, Ansible inventory/vars and GitOps cluster values |
| `secrets` | Prompts for secrets (hidden input) and writes `secrets.sops.yaml` encrypted with age |
| `plan` / `apply` | Runs the phases in order: Terraform → Ansible → GitOps bootstrap |

Go was chosen over Python or shell because it ships as one binary for macOS, Linux and Windows,
matches the ecosystem (kubectl, helm, talosctl), and the Charm libraries give a polished terminal UI.

## Consequences

- No runtime dependencies for users besides Terraform/OpenTofu and Ansible.
- Every wizard question maps to a documented field in `cluster.yaml`; `--non-interactive`
  plus a file covers automation.
- Releases are built for several platforms by CI (GoReleaser).
