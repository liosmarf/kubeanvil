# kubeanvil

Build a highly available Kubernetes cluster on Proxmox VE and bootstrap it with GitOps, from
one configuration file. An interactive installer asks the questions; Terraform, Ansible and
Argo CD do the work.

> **Early development.** Nothing has been deployed end to end yet — see [status](#status).

## How it works

```mermaid
flowchart LR
    I["kubeanvil init<br/>(interactive)"] --> C[cluster.yaml]
    C --> T["Terraform<br/>VMs"]
    C --> A["Ansible<br/>OS + K3s + Cilium"]
    C --> G["Argo CD<br/>platform components"]
    T --> A --> G
```

`cluster.yaml` is the only file you edit (or let the wizard write). Everything else is
generated from it, so Terraform, Ansible and Argo CD never disagree about an IP address or a
domain. See [`examples/cluster.yaml`](examples/cluster.yaml) and
[ADR 0001](docs/adr/0001-single-cluster-config.md).

The planned entry point is a single command on the Proxmox host, in the spirit of the Proxmox
VE Community Scripts ([ADR 0005](docs/adr/0005-one-line-installer-and-bastion.md)):

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/liosmarf/kubeanvil/v0.1.0/install.sh)"
```

## Status

| Part | State |
|---|---|
| Proxmox VM module and environment ([`terraform/`](terraform/)) | ✅ validated with Terraform and OpenTofu |
| Ansible: OS hardening, HA K3s, Cilium, Argo CD bootstrap ([`ansible/`](ansible/)) | ✅ ported; passes ansible-lint (production) · 🚧 end-to-end run |
| [Component catalog](gitops/platform/catalog.yaml): 12 categories, the 2–3 most used options each, pinned versions | ✅ |
| GitOps platform chart: one Argo CD Application per selected component ([`gitops/`](gitops/)) | ✅ every component renders and validates ([`scripts/check-gitops.sh`](scripts/check-gitops.sh)) |
| `kubeanvil` CLI ([`cmd/`](cmd/), [`internal/`](internal/)): `validate`, `generate` | ✅ tested (`go test ./...`) |
| `kubeanvil init` wizard: size presets, component choice, memory budget | 🚧 |
| `kubeanvil apply`: Terraform → Ansible → GitOps | 🚧 |
| One-line installer with Proxmox discovery | 🚧 |
| vSphere and Azure modules ([ADR 0003](docs/adr/0003-provider-node-contract.md)) | planned |

## Design decisions

The [ADRs](docs/adr/) record what was decided and why: a single config file, secrets with
External Secrets + OpenBao (SOPS for bootstrap), a node contract shared by all providers, a Go
CLI, the installer, and the component catalog.

## License

[MIT](LICENSE)
