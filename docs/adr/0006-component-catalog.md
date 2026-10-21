# ADR 0006 — A component catalog with pinned versions and trade-offs

- Status: accepted
- Date: 2026-10-21

## Context

Platform teams rarely agree on one stack: some prefer Flux over Argo CD, Calico over Cilium,
Gatekeeper over Kyverno. A useful platform repository should offer sensible defaults *and*
let users choose — while explaining the trade-offs, because the choice is the hard part.

## Decision

`catalog/components.yaml` lists, per **category**, the supported **options**. Each option records:

- a one-line summary, **pros** and **cons** (shown by the wizard next to the choice);
- the Helm chart (repository, name, **pinned version**) and the upstream app version;
- licence, CNCF maturity and an approximate memory footprint;
- the providers it works on, what it requires and what it conflicts with;
- a **status**: `supported` (implemented and tested in this repository) or `planned`
  (documented, not selectable yet).

`kubeanvil init` shows each category with the recommended option first, its pros/cons and
version; the selection is stored in `cluster.yaml` under `spec.components`.
`kubeanvil generate` enables the matching GitOps applications.

Versions are **pinned**, not resolved at install time: the same `cluster.yaml` must produce the
same cluster next year. Renovate keeps the catalog current through pull requests (each entry
carries a `# renovate:` hint), so upgrades are reviewed and tested like any other change.

## Consequences

- Users see *why* each default was chosen and what they give up by changing it.
- Every `supported` option needs its own GitOps manifests and CI coverage, so options move
  from `planned` to `supported` one at a time.
- The catalog doubles as documentation: `docs/components.md` is generated from it.
