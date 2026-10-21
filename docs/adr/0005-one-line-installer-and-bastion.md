# 0005 — One-line installer and a bastion container

**Status:** accepted · 2026-10-21

## Context

Proxmox users know the Community Scripts: one line in the host shell, a few menus, done. We
want that first run, without installing tooling on the hypervisor or hiding what runs.

## Decision

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/liosmarf/kubeanvil/<tag>/install.sh)"
```

On a Proxmox host, `install.sh`:

1. reads the node, storages, bridges, subnet and free resources with `pvesh` to pre-fill answers;
2. offers to create a least-privilege `terraform@pve` user and API token;
3. offers to create a small Debian LXC (the bastion) with OpenTofu, Ansible, kubectl, helm and
   `kubeanvil`, and starts the wizard inside it.

Anywhere else it downloads the `kubeanvil` binary and starts the wizard. Every change to the
host is confirmed first; every download is pinned to a release and checked against its SHA-256.

## Consequences

- The hypervisor stays clean; the bastion holds `cluster.yaml`, state configuration and the
  kubeconfig, and is where the cluster is operated from.
- `curl | bash` still asks for trust: the README shows how to read the script before running it.
