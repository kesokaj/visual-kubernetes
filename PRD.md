# PRD — visual-kubernetes

A read-only, real-time visualizer of a Kubernetes cluster's internals on one white page, for live teaching demos.
Status: draft · Owner: kesokaj · Last updated: 2026-11-09 · Design: [SDD.md](SDD.md) · Technical design: [TDD.md](TDD.md)

<!-- Read before every significant change. Immutable without the user's explicit approval of the exact text:
     propose the edit → approval → ADR → PRD.md and TODO.md in one commit (global context § Hard rules).
     Nothing here needs routine edits: a goal's baseline is a dated starting point, never updated; `Last updated`
     is set by the commit that applies an approved edit; how each requirement is met and tested is traced in
     SDD.md and TDD.md, which agents keep current. The `requirements` skill elicits the items below and traces
     them; it never writes "approved" itself. -->

## Problem

<!-- Who hurts, what hurts, and why now — two or three sentences, no solution yet. -->

## Users

<!-- One line per kind of user (a person, a team, a calling system): who they are and what they need from this. -->

- **<user>:** <who they are and what they need>.

## Goals

<!-- G1…: each with a metric and a dated baseline → target. A goal with no metric is a wish; say how it is measured. -->

| # | Goal | Metric | Baseline (YYYY-MM-DD) | Target |
|---|---|---|---|---|
| G1 | <outcome> | <how it is measured> | <value> | <value> |

## Non-goals

<!-- What this project deliberately does not do, one line each, so nobody adds it by accident. -->

- <one line>.

## Must — non-negotiable

<!-- The project is wrong without these. One sentence per item, each with a provenance line: who asked, when, and
     where the requirement is enforced (a test, a rule, a guide). Numbered R1…; a later edit keeps the numbers
     (a struck item stays as "R3 — struck <date>: <why>"). -->

- **R1** — <one sentence>.
  <!-- provenance: <who>, <date>; enforced by: <path[:N] | TestName | record ID>, … -->
- **R2** — <one sentence>.
  <!-- provenance: <who>, <date>; enforced by: <path[:N] | TestName | record ID>, … -->

## Should — expected unless a decision says otherwise

<!-- S1…: a deviation needs a D-row in the active plan, not an ADR. -->

- **S1** — <one sentence>.
  <!-- provenance: <who>, <date>; enforced by: <path[:N] | TestName | record ID>, … -->

## Could — welcome when cheap

<!-- C1…: nobody blocks on these; a plan may take one as a Follow-up. -->

- **C1** — <one sentence>.

<!-- Targets a service must meet (concurrency, p99 latency, data volume) are Must or Should items like any other,
     one sentence each with the number in it and the test, or the path or record of the dashboard, that measures it as "enforced by";
     a table is added only when there are five or more. A CLI, a library or a tool has none. -->

## Known limits

<!-- Facts about the environment or the platform the project lives with (a size cap, a rate limit, a CLI quirk), each with its source. -->

- <one line> — <source>.

## Open questions

<!-- What is not decided yet, one line each, with where it will be decided (a plan row, a date). Struck when answered. -->

- <one line>.
