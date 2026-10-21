# ADR 0003 — A common node contract across infrastructure providers

- Status: accepted
- Date: 2026-10-21

## Context

The first target is Proxmox VE, but the same platform should run on VMware vSphere and on
Azure. Provider APIs differ completely; what Ansible and Kubernetes need from them does not.

## Decision

Every provider module under `terraform/modules/<provider>-vm` implements the same contract.

**Input** (per node, rendered from `cluster.yaml`):

| Field | Meaning |
|---|---|
| `name` | Hostname, e.g. `lab-cp-01` |
| `role` | `server` (control plane + etcd) or `agent` (worker) |
| `ip` | Static IPv4 address with prefix |
| `cpu`, `memory_mib`, `os_disk_gib`, `data_disk_gib` | Sizing (`data_disk_gib = 0` means none) |
| `ssh_public_keys`, `admin_user` | Access injected through cloud-init |

**Output** (`nodes` map), consumed by the inventory generator:

```hcl
nodes = {
  "lab-cp-01" = { ip = "192.168.50.91", role = "server", zone = "pve" }
}
```

`zone` is the failure domain (Proxmox node, vSphere host/cluster, Azure availability zone)
and becomes the `topology.kubernetes.io/zone` label.

Every module must also: boot an Ubuntu LTS cloud image, configure cloud-init with the admin user,
expose the data disk with a stable device ID, and tag resources with the cluster name.

## What stays provider-specific

| Concern | Proxmox / vSphere | Azure |
|---|---|---|
| API VIP | kube-vip (ARP) | Azure internal Load Balancer |
| LoadBalancer Services | Cilium LB-IPAM + L2 announcements | Azure cloud provider |
| Block storage | Longhorn on the data disk | Azure Disk CSI |
| Secret store | OpenBao | Azure Key Vault |

These are switched by `spec.provider.type` in the generated Ansible and GitOps values.

## Consequences

- Ansible roles and GitOps manifests do not know which hypervisor they run on.
- A new provider is a new module plus one row in the table above.
