# ADR 0002 — Secrets: External Secrets Operator + OpenBao, SOPS for bootstrap

- Status: accepted
- Date: 2026-10-21

## Context

The platform needs two kinds of secrets:

1. **Bootstrap secrets**, required before the cluster can serve anything: the hypervisor API
   token, the DNS provider token for ACME, S3 credentials for backups.
2. **Runtime secrets** for platform components and workloads, which must be rotatable and
   auditable, and must not be stored in Git.

The lab this project grew from used Sealed Secrets. It works, but the sealing key lives inside
the cluster: losing the cluster without an out-of-band copy of the key makes every sealed
secret in Git unreadable, and the approach does not map to cloud secret stores.

## Decision

- **Runtime secrets:** [External Secrets Operator](https://external-secrets.io) (ESO) syncs
  secrets from an external store into Kubernetes. Git only contains `ExternalSecret`
  references (*where* a secret is), never values.
  - On-premises (Proxmox, vSphere): **[OpenBao](https://openbao.org)**, in-cluster, HA with
    integrated Raft storage, Kubernetes auth with one policy per consumer.
  - Azure: **Azure Key Vault** with Workload Identity — no in-cluster store.
- **Bootstrap secrets:** **SOPS + age**. `secrets.sops.yaml` is committed encrypted; the age
  private key is kept outside the cluster (password manager). Decryption does not depend on the
  cluster existing, which removes the chicken-and-egg problem of Sealed Secrets.
- OpenBao is chosen over HashiCorp Vault because Vault moved to the Business Source License in
  2023; OpenBao is the Linux Foundation fork with a compatible API and an OSI-approved licence.

## Consequences

- Workloads are portable across providers: only the `ClusterSecretStore` changes.
- OpenBao must be initialised and unsealed once; unseal keys are stored with the age key.
  Auto-unseal (transit or a cloud KMS) is a documented upgrade path.
- One more stateful component to back up (Raft snapshots to S3, scheduled).
