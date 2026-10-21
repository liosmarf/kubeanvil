# 0002 — Secrets: External Secrets + OpenBao, SOPS for bootstrap

**Status:** accepted · 2026-10-21

## Context

Some secrets are needed before the cluster exists (hypervisor token, DNS token for ACME, S3
keys). Others belong to running workloads and must be rotatable. Sealed Secrets, used in the
cluster this project grew from, keeps its key inside the cluster: lose the cluster without a
copy of that key and every sealed secret in Git becomes unreadable.

## Decision

- **Bootstrap secrets:** SOPS with age. `secrets.sops.yaml` is committed encrypted; the age
  key lives outside the cluster. Decryption never depends on the cluster.
- **Runtime secrets:** External Secrets Operator. Git holds references, not values. The store
  is OpenBao on Proxmox/vSphere (HA, Raft, Kubernetes auth) and Azure Key Vault on Azure.
- OpenBao rather than Vault: same API, but an open-source licence (Vault moved to the BSL in 2023).

## Consequences

- Workloads move between providers unchanged; only the secret store differs.
- OpenBao has to be initialised and unsealed once, and backed up (Raft snapshots to S3).
