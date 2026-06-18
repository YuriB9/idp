---
name: architecture-decision-records
description: Use when capturing a significant architectural or cross-service technical decision in the SREOnCall project — during an OpenSpec change (when design.md reasoning is worth persisting beyond archive) or when backfilling ADRs from archived design.md files. Triggers on decisions about transport (RabbitMQ/chi), storage (Postgres/pgx, Redis), auth (Keycloak/JWKS), the go.work module layout, service boundaries, message/queue versioning, and migration strategy. Do NOT use for implementation details with no lasting architectural consequence.
---

# Architecture Decision Records for SREOnCall

This skill governs how Architectural Decision Records (ADRs) are written in the
SREOnCall project. It pairs with the OpenSpec `spec-driven-with-adr` schema: specs
capture *what the system does today*; ADRs capture *how and why it is built that way*.
ADRs live outside any single change and persist after a change is archived, so future
proposals can read prior architectural reasoning instead of re-litigating it.

## When to write an ADR

Write an ADR when a change makes a decision that has lasting, cross-cutting
architectural consequences. Good triggers in this project:

- Transport / messaging: choosing or changing RabbitMQ usage, chi HTTP boundaries,
  message/queue versioning or compatibility strategy.
- Storage: Postgres/pgx access patterns, ORM-vs-raw decisions, Redis usage, the
  migration strategy (golang-migrate).
- Auth: Keycloak / JWT / JWKS validation approach.
- Project structure: the go.work multi-module layout, what lives in pkg/ vs a service,
  service boundary changes across escalation / incident / ingestion / notification / scheduling.
- Cross-service contracts and any decision a future proposal would otherwise rediscover.

Do NOT write an ADR for: routine implementation details, local refactors, naming,
bug fixes, or anything with no architectural reach. Those belong in the change's
design.md, which is archived with the change.

One decision = one ADR, even if it was discussed across several changes. If a later
change supersedes an earlier decision, do not edit history — write a new ADR and mark
the old one Superseded (see Status).

## Where ADRs live

Store ADRs in `docs/adr/`, outside the `openspec/` folder, so they survive change
archival and stay readable by future proposals. One file per ADR:

```
docs/adr/NNNN-short-kebab-title.md
```

`NNNN` is a zero-padded sequential number (0001, 0002, ...). Never reuse or renumber.

## File format

Each ADR follows this structure. Keep it tight — an ADR is a decision record, not a
design doc.

```markdown
# ADR-NNNN: <short decision title>

- Status: <Proposed | Accepted | Superseded by ADR-XXXX | Deprecated>
- Date: <YYYY-MM-DD of the actual decision>
- Change: <openspec change name/id, or git commit/PR if backfilled>
- Affected: <services and pkg/ modules, e.g. services/notification, pkg/amqp>

## Context

What forced a decision here. The problem, the constraints, the relevant
forces (operational, performance, security, team). State things that were true
*at decision time*. A few sentences — no padding.

## Options considered

- **Option A** — short description. Trade-offs.
- **Option B** — short description. Trade-offs.
- (Include the option that was rejected; the rejection reasoning is the point.)

## Decision

What was chosen, stated plainly. One short paragraph.

## Consequences

What becomes easier and what becomes harder as a result. Include follow-on
obligations (e.g. "all queue consumers must tolerate schema version N-1"),
operational impact, and anything a future change must respect.
```

## Status values

- **Proposed** — decision drafted, not yet ratified.
- **Accepted** — in force; reflects current architecture.
- **Superseded by ADR-XXXX** — replaced by a later decision. Keep the file; add the
  pointer. Never delete.
- **Deprecated** — no longer relevant but kept for history.

The set of Accepted ADRs together represents the current state of the architecture.

## Honest dating and provenance

- `Date` is the date the decision was actually made, taken from the originating
  change or its git history — NOT the date the ADR file was written. This preserves
  the real architectural timeline.
- Always fill `Change` with the originating OpenSpec change, or with the git
  commit/PR when backfilling, so the reasoning is traceable to its source.

## Backfilling ADRs from archived design.md

SREOnCall was built spec-first, so prior reasoning already exists in archived
`design.md` files. To backfill:

1. Walk `openspec/changes/archive/` oldest → newest, keeping a running list of live
   architectural decisions.
2. For each genuine architectural decision, find the LATEST design.md where it is
   still in force; take the rationale from there. Use earlier design.md files only for
   "what was considered and rejected".
3. Extract — do not re-invent. The reasoning is already written and was reviewed;
   repackage it into the format above rather than reconstructing intent.
4. Skip decisions later overturned by a newer change — do not record reversed choices
   as current architecture.
5. Date each backfilled ADR from the original change's git history; cite that change.

A backfill pass should yield a small set (typically 5–10 ADRs), not one per change.

## Quality bar

- If you find yourself guessing *why* a decision was made and the source design.md is
  thin, write a short honest ADR that points to the change for details rather than
  fabricating rationale.
- Prefer fewer, load-bearing ADRs over many shallow ones.
- Every Accepted ADR must still be true of the system today. If it isn't, it should be
  Superseded or Deprecated.