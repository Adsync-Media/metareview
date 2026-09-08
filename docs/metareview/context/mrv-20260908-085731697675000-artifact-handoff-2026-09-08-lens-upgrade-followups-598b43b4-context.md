# metareview context: .handoffs/handoff-2026-09-08-lens-upgrade-followups.md

Run ID: `mrv-20260908-085731697675000-artifact-handoff-2026-09-08-lens-upgrade-followups-598b43b4`

## Target

- Path: `.handoffs/handoff-2026-09-08-lens-upgrade-followups.md`
- Repository mode: `metaswarm-extension`
- Git branch: `main`
- Git head: `fbd7e55`

## Artifact Excerpt

```markdown
# Handoff: lens-upgrade follow-ups — benchmark acceptance runs, 0.11.0 release, two taxonomy clauses

**Date**: 2026-09-08 · **Branch**: `main` · **Author session**: Pi (the session that implemented handoff-2026-09-08-benchmark-driven-lens-upgrade)

> Fresh agent, zero prior context. The parent handoff (`.handoffs/handoff-2026-09-08-benchmark-driven-lens-upgrade.md`)
> is ~90% done: Workstreams A and B are MERGED to metareview main, Workstream C is on
> harnesseval `adjudication-hardening-v3` (PR dsifry/harnesseval#16, in review when this was
> written — check its state first). What remains is §5.2/§5.3 of the parent handoff (the
> benchmark re-runs and acceptance check), the 0.11.0 release the user asked for, and a small
> set of follow-ups the bots' residuals motivated. Read the parent handoff §5 first; this file
> only covers the delta.

## State as of this writing

| Item | State |
|---|---|
| Workstream A (rubric briefs) | **Merged** — metareview PR #149 (squash `3f9d07d`), post-merge learning recorded |
| Workstream B (Runtime-reliability lens, 10th) | **Merged** — metareview PR #150 (squash `93d2ee7`), post-merge learning recorded |
| Workstream C (adjudicator v3 + adapter sync) | harnesseval PR #16 — CodeRabbit round 1 and Bugbot round 1 both addressed, threads resolved; **check whether it merged** and do post-merge learning if the lab has an analog |
| Era boundary | v10 lens era keyed from **2026-09-09** (see "Deviations" below) |
| FINDINGS.md worktree clobber | **Issue #151 filed** — not a blocker for anything here |
| §5.1 adapter sync | Done in PR #16 (Runtime-reliability is dispatch call #9; Mechanical-precision stays out of the benchmark set) |
| §5.2/§5.3 benchmark re-runs + acceptance | **NOT DONE** — this is the main remaining work |
| 0.11.0 release | **NOT DONE** — user explicitly asked for a minor version bump "when we are done" |

## 1. The benchmark acceptance runs (parent handoff §5.2/§5.3) — the main remaining work

Everything the runs need is in place: the merged rubrics, the synced adapter
(`harnesseval/adapters/metareview_realistic.py`, 9 lens dispatches), the hardened
adjudicator (`readjudicate3.py` v3: cross-run dedup, grounded hallucination, k=3 majority,
provenance stripping, `--second-pass`).

**What to run** (per parent §5.2, minimum): mrv × glm-5.3 × low and × high (xhigh rung) on
the top-6 PRs, per `~/Developer/harnesseval/REPRODUCE.md` and the batch tracking in
`results/`. Judge/model access: `~/.config/harnesseval/keys.env` (HARNESS_LUNAROUTE_API_KEY
+ LUNAROUTE_BASE_URL for GLM; never print them). Point the adapter at a binary built from
CURRENT main via `HARNESS_MRV_BIN`.

**Acceptance** (parent §5.3): the 8 named previously-missed findings, ≥6/8 caught in a
glm-low run, no golden-recall regression >2 points (baselines in parent §7:
GLM low rec 0.66–0.83 / hidden gold 24–25 per PR; high 0.67–0.74 / 26–33; CE 35–36 low,
39–46 high). The 8 findings are listed in parent §5.3; all are in `analysis/ce_only.json`
(read-only — do not modify anything under `analysis/`).

**Sequencing note**: if any brief text changes again (see §3 below), the adapter must be
re-synced BEFORE re-measuring — that is why the taxonomy clauses are deliberately queued
behind these runs.

**Known measurement caveat** (parent §8): re-adjudicating with the v3 adjudicator makes new
hidden-gold counts non-comparable to `report2.md`'s v2 numbers — re-run the baseline cells
under v3 too, or keep a version marker in the output JSON.

## 2. The 0.11.0 release (user-requested)

- `internal/version/version.go`: `0.10.1` → `0.11.0`.
- CHANGELOG: fold `## Unreleased` into `## 0.11.0 - 2026-09-08` (or the actual release date).
- Tag `v0.11.0` (annotated, message style: `metareview 0.11.0 — <summary>`; see `git show v0.10.0`).
- Through the standard pipeline (test + CodeRabbit + Bugbot + gtg, dogfood pr-ready) — it is
  a trivial-diff PR but the campaign discipline stil
```

## Service Inventory

No service inventory found.

## Knowledge Facts

No Beads knowledge facts found.

## Suggested Reviewers

- Feasibility
- Completeness
- Scope and alignment
- Architecture
- Intent preservation
- Security
- Testing-quality
- Data-migration
- Runtime-reliability
- Mechanical-precision
