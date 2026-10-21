# 0006 — A catalog of the common choices

**Status:** accepted · 2026-10-21

## Context

Teams disagree about stacks — Argo CD or Flux, Cilium or Calico. Offering every project is noise;
offering none forces our taste on everyone.

## Decision

`catalog/components.yaml` lists, per category, the **two or three most widely used options**,
each with a short summary, pros, cons, pinned chart and app versions, licence and approximate
memory. One option per category is the default.

Only options marked `supported` (implemented and tested here) are selectable; others are shown
as `planned`. Versions are pinned so the same `cluster.yaml` builds the same cluster next year;
Renovate proposes upgrades as pull requests.

## Consequences

The wizard can explain each choice instead of just asking it. Each supported option needs its
own manifests and CI coverage, so options are added one at a time.
