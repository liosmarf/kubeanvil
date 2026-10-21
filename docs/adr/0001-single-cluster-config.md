# ADR 0001 — One `cluster.yaml` as the source of truth

- Status: accepted
- Date: 2026-10-21

## Context

A cluster is described by three toolchains: Terraform (machines), Ansible (operating system and
Kubernetes) and Argo CD (everything inside the cluster). Each needs overlapping values — IP
addresses, the API VIP, the base domain, node counts — and keeping them in sync by hand is the
most common source of drift in infrastructure repositories.

We also want an interactive setup assistant without coupling it to any one of those toolchains.

## Decision

Every cluster is described by a single, versioned `cluster.yaml` (`apiVersion: kubeanvil.io/v1alpha1`).

- `kubeanvil init` asks questions and **only** writes `cluster.yaml`.
- `kubeanvil generate` renders every downstream input from it: `terraform.tfvars.json`,
  the Ansible inventory and `group_vars`, and the cluster values consumed by GitOps.
- Generated files are marked as such and are never edited by hand.
- A JSON Schema validates the file in editors and in CI; `kubeanvil validate` adds the
  semantic checks a schema cannot express (IPs inside the CIDR, VIP outside the node range, …).
- Secrets live in a separate `secrets.sops.yaml`, encrypted with SOPS + age (ADR 0002).

## Consequences

- One place to change a value; reproducible output for the same input.
- The wizard is optional: CI and power users edit `cluster.yaml` directly.
- Adding a provider means adding a `spec.provider.<type>` block and a Terraform module that
  honours the common node contract (ADR 0003); Ansible and GitOps stay untouched.
- The schema becomes a public API: breaking changes require a new `apiVersion`.
