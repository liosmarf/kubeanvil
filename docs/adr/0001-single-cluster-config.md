# 0001 — One `cluster.yaml` per cluster

**Status:** accepted · 2026-10-21

## Context

Terraform, Ansible and Argo CD each need the same values: node IPs, the API VIP, the domain.
Keeping three copies in sync by hand is how they drift apart.

## Decision

A cluster is described by one versioned `cluster.yaml` (`apiVersion: kubeanvil.io/v1alpha1`).
`kubeanvil init` writes it; `kubeanvil generate` renders the Terraform variables, the Ansible
inventory and the GitOps values from it. Generated files are never edited by hand.
A JSON Schema validates it in editors and CI; `kubeanvil validate` adds the checks a schema
cannot express (IPs inside the subnet, VIP outside the node range).

Secrets are kept out of it, in an encrypted `secrets.sops.yaml` ([0002](0002-secrets-eso-openbao-sops.md)).

## Consequences

- One place to change a value; the same file always produces the same result.
- The wizard is optional: CI and experienced users edit the file directly.
- The schema is a public interface; breaking changes need a new `apiVersion`.
