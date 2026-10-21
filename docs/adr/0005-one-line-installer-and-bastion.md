# ADR 0005 — One-line installer and a management bastion

- Status: accepted
- Date: 2026-10-21

## Context

Proxmox users are used to the Proxmox VE Community Scripts: paste one line in the host shell,
answer a few menus, get a working service. We want the same first-run experience for a full
Kubernetes platform, without compromising the hypervisor or hiding what runs.

## Decision

A single entry point, pinned to a release:

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/<owner>/kubeanvil/v1.0.0/install.sh)"
```

`install.sh` is small and readable. It detects where it runs:

- **On a Proxmox VE host**
  1. Discovers the environment with `pvesh` (node, storages and their content types, bridges,
     subnet, free CPU/RAM) to pre-fill the wizard.
  2. Offers, with explicit confirmation, to create a least-privilege `terraform@pve` user,
     role and API token.
  3. Offers to create a small **bastion LXC** (Debian) holding OpenTofu, Ansible, kubectl,
     helm and `kubeanvil`. The hypervisor itself gets no extra packages.
  4. Starts `kubeanvil init` inside the bastion.
- **Anywhere else** (Linux/macOS workstation): downloads the `kubeanvil` release binary,
  verifies its SHA-256 checksum and starts the wizard, asking for what it cannot discover.

The bastion keeps `cluster.yaml`, the Terraform state backend configuration, the age key
location and the kubeconfig: it becomes the place to operate the cluster from.

Unattended use: `install.sh --config cluster.yaml --yes`.

## Consequences

- Same UX as the Community Scripts, but every host change is confirmed and every download is
  pinned and checksummed. `curl | bash` still requires trust: the README shows how to download
  and read the script before running it.
- The wizard can refuse impossible plans early (e.g. node sizes exceeding free host RAM).
- Releases must publish `install.sh`, binaries and checksums together (GoReleaser in CI).
