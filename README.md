# kubeanvil

[![CI](https://github.com/liosmarf/kubeanvil/actions/workflows/ci.yml/badge.svg)](https://github.com/liosmarf/kubeanvil/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/liosmarf/kubeanvil)](https://github.com/liosmarf/kubeanvil/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A highly available Kubernetes platform on Proxmox VE from one configuration file. An
interactive wizard reads your Proxmox host and asks four questions; Terraform creates the VMs,
Ansible installs hardened Ubuntu, K3s and Cilium, and Argo CD installs the rest of the platform
from Git.

```text
kubeanvil init    →  cluster.yaml  →  kubeanvil apply
                                       ├─ OpenTofu   VMs, disks, cloud-init
                                       ├─ Ansible    OS hardening, HA K3s, Cilium, Argo CD
                                       └─ Argo CD    storage, monitoring, logs, policies…
```

## What you get

- **HA control plane:** K3s with embedded etcd on 1, 3 or 5 nodes behind a kube-vip address.
- **Networking:** Cilium in place of kube-proxy, with LoadBalancer IPs on your LAN, Ingress
  and Gateway API.
- **Platform:** components picked from a [catalog](gitops/platform/catalog.yaml) of the two
  or three options teams usually weigh, each with pinned versions, pros and cons. Every
  component is one Argo CD Application.
- **Always installed:** cert-manager (Let's Encrypt or an internal CA), metrics-server and kured
  for rolling reboots after security updates. External DNS and Velero backups are added when
  configured.

| Category | Default | Also supported |
|---|---|---|
| Pod networking, Ingress and Gateway API | Cilium | |
| Persistent storage | Longhorn | |
| GitOps | Argo CD | |
| Policy engine | Kyverno | |
| Metrics and alerting | Prometheus + Grafana | |
| Logs | Loki + Alloy | none |
| PostgreSQL operator | CloudNativePG | none |
| Cluster UI | none | Rancher |

Calico, Traefik, Envoy Gateway, Rook-Ceph, Flux, OpenBao, Keycloak, VictoriaMetrics and Tempo
are listed in the wizard as planned, so you can see the trade-offs.

## Quick start

On a Proxmox VE host, as root:

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/liosmarf/kubeanvil/v1.0.0/install.sh)"
```

The installer asks before every change. It creates:

1. an API user and token with only the privileges Terraform needs;
2. a small Debian container, the bastion, with kubeanvil, OpenTofu, Ansible, kubectl, helm and
   SOPS. Each download is checked against a pinned SHA-256.

Then run, inside the bastion:

```bash
cd kubeanvil
kubeanvil init     # four questions, then a review of the whole plan
kubeanvil apply    # shows the Terraform plan and asks before each phase
```

To read the script before running it:

```bash
curl -fsSLO https://raw.githubusercontent.com/liosmarf/kubeanvil/v1.0.0/install.sh
less install.sh && bash install.sh
```

On any other machine, the same script installs the binary and a checkout of this repository.
You then need OpenTofu (or Terraform) and Ansible. From source, you need Go 1.24+ and run
`go run ./cmd/kubeanvil`.

## Commands

| Command | What it does |
|---|---|
| `init` | Reads the Proxmox host over SSH with read-only `pvesh` calls and a ping sweep: storages, bridges, subnet, DNS, time zone, free memory, used VMIDs and taken IP addresses. Asks for a name, the host, a size and the components, then shows a review where any section can be changed. |
| `validate` | Catches overlapping addresses, an even number of etcd members, a LoadBalancer range that collides with nodes, and unsupported choices. |
| `generate` | Renders the Terraform variables, the Ansible inventory and the platform values from `cluster.yaml`. |
| `secrets` | Hidden prompts for the secrets your choices need, such as a DNS token or backup keys. Writes them to `secrets.sops.yaml`, encrypted with SOPS and age. |
| `apply` | Generate, then Terraform, Ansible, the Argo CD bootstrap and the kubeconfig. Safe to run again: unchanged phases do nothing. |
| `destroy` | Deletes the cluster's VMs after you type its name. |

## How it works

`cluster.yaml` is the only file you edit, or let the wizard write. Terraform, Ansible and
Argo CD all read values generated from it, so they never disagree about an address or a
domain. See [`examples/cluster.yaml`](examples/cluster.yaml).

- **Terraform** ([`terraform/`](terraform/)) creates one VM per node from the Ubuntu cloud
  image and passes its network and user configuration through cloud-init. The node interface
  is the same for every provider, so vSphere and Azure modules can slot in.
- **Ansible** ([`ansible/`](ansible/)) hardens the OS, installs K3s and Cilium, then installs
  Argo CD. Once Argo CD runs, it hands Cilium and Argo CD over to Argo CD itself without
  reinstalling either.
- **Argo CD** renders the [platform chart](gitops/platform/), which creates one Application
  per selected component, in dependency order.
- **Your network stays out of Git.** The cluster's own values (addresses, domain) travel
  inside the root Application, so a cluster can follow this public repository without a fork.
- **Secrets:** bootstrap secrets are encrypted with SOPS + age and handed to Ansible in
  memory. kubeanvil never writes the Proxmox token to disk. The installer stores it only
  inside the bastion, readable by root.

## Tested

- **End to end on Proxmox VE 9:** 3 control planes and 3 workers. All 19 Applications are
  Synced and Healthy. A second `apply` changed only what `cluster.yaml` changed (adding
  Rancher).
- **CI on every push:**
  - unit tests, including a real SOPS/age round trip;
  - the wizard driven with key presses in a pseudo-terminal;
  - `tofu validate` and `ansible-lint` (production profile);
  - every component rendered and checked with kubeconform;
  - ShellCheck and gitleaks over the full history.
- **Dependencies:** Renovate proposes updates for the 28 pinned chart versions and the CI
  tools.

Known limits in 1.0:
- Proxmox is the only provider.
- External Secrets with OpenBao and single sign-on are planned.
- `destroy` and the installer's bastion have been exercised less than `apply`.

## Design decisions

The [architecture decision records](docs/adr/) explain the choices:

1. [One `cluster.yaml` per cluster](docs/adr/0001-single-cluster-config.md)
2. [Secrets: External Secrets + OpenBao, SOPS for bootstrap](docs/adr/0002-secrets-eso-openbao-sops.md)
3. [A node contract shared by all providers](docs/adr/0003-provider-node-contract.md)
4. [A Go CLI](docs/adr/0004-kubeanvil-go-cli.md)
5. [One-line installer and a bastion container](docs/adr/0005-one-line-installer-and-bastion.md)
6. [A catalog of common choices](docs/adr/0006-component-catalog.md)
7. [Cluster values in the root Application](docs/adr/0007-cluster-values-in-the-root-application.md)

## License

[MIT](LICENSE)
