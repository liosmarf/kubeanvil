# 0007 — Cluster values travel in the root Application

**Status:** accepted · 2026-10-21 · amends [0001](0001-single-cluster-config.md)

## Context

The platform chart is shared; each cluster adds its own values: node and VIP addresses, the
domain, the chosen components. Keeping those values in the GitOps repository means every user
must fork kubeanvil and commit their LAN layout to it, and a public fork publishes that layout.

## Decision

`kubeanvil generate` renders the platform values into the Ansible variables, and the root
Application created at bootstrap passes them inline (`helm.valuesObject`). The repository
supplies only the chart, so a cluster can track the upstream repository at a release tag
without a fork. `cluster.yaml` stays the single source of truth; `kubeanvil apply` re-renders
and updates the root Application when it changes.

A fork that prefers its values in Git sets `argocd_cluster_values` to a file path, and the
root Application uses `valueFiles` instead.

## Consequences

- No addresses or domains need to be committed anywhere.
- Changing a cluster value is `kubeanvil apply`, not a pull request: the change history lives
  with `cluster.yaml`, wherever the user versions it.
- The platform chart itself, and every component version, still change only through Git.
