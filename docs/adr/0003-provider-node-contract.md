# 0003 — One node contract for every provider

**Status:** accepted · 2026-10-21

## Context

Proxmox first, then vSphere and Azure. Their APIs have nothing in common; what Ansible needs
from them is the same: a list of reachable Ubuntu machines with a role.

## Decision

Every `terraform/modules/<provider>-vm` takes the same inputs — `name`, `role`
(`server`/`agent`), `ip_cidr`, `cpu`, `memory_mib`, `os_disk_gib`, `data_disk_gib`,
`admin_user`, `ssh_public_keys` — plus its own provider settings, and returns the same outputs:

```hcl
nodes = { "lab-cp-01" = { ip = "192.168.50.91", role = "server", zone = "pve" } }
```

`zone` is the failure domain and becomes the `topology.kubernetes.io/zone` node label.

What cannot be shared is switched by provider type:

| | Proxmox / vSphere | Azure |
|---|---|---|
| API VIP | kube-vip (ARP) | Azure internal load balancer |
| LoadBalancer Services | Cilium LB-IPAM + L2 | Azure cloud provider |
| Block storage | Longhorn | Azure Disk CSI |
| Secret store | OpenBao | Azure Key Vault |

## Consequences

Ansible and the GitOps layer never know which provider built the machines. A new provider is
a new module and a column in that table.
