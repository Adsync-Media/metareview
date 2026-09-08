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

**What to run** (per parent §5.2, minimum): mrv × **glm-5.3-background** (the
model-under-test ID — do not confuse it with glm-5.3-flash, which is the JUDGE) × low and ×
high (xhigh rung) on the top-6 PRs, per `~/Developer/harnesseval/REPRODUCE.md` and the batch
tracking in `results/`. Point the adapter at a binary built from CURRENT main via
`HARNESS_MRV_BIN`.

**Keys** (`~/.config/harnesseval/keys.env`): the harness loads the file itself via
`harnesseval/keys.py` (HARNESS_LUNAROUTE_API_KEY + LUNAROUTE_BASE_URL) — do NOT export the
values into the shell or profile: the HARNESS_ prefix exists precisely so they can never
collide with the env-var names the CLIs watch (ANTHROPIC_API_KEY / OPENAI_API_KEY override
Claude Code / Codex OAuth when set). Never print them. For metareview's own FSM dogfood
judge (a separate concern), the session precedent passed OPENAI_BASE_URL (lunaroute host,
path stripped) + OPENAI_API_KEY inline per-command, never exported.

**Acceptance** (parent §5.3): the 8 named previously-missed findings, ≥6/8 caught in a
glm-low run, no golden-recall regression >2 points (baselines in parent §7:
GLM low rec 0.66–0.83 / hidden gold 24–25 per PR; high 0.67–0.74 / 26–33; CE 35–36 low,
39–46 high). The 8 findings are listed in parent §5.3 with their sources: findings 1–7 are
in `analysis/ce_only.json`; finding 8 (Office365 `updateEvent`/`deleteEvent` vs the
`Calendar` interface, PR #10967) is from `analysis/EXTERNAL_REVIEWER_GAPS.md` (the
CodeRabbit cross-check, parent §2.4), NOT ce_only.json. Everything under `analysis/` is
read-only.

**Hard gate alongside the runs** (parent §5.3's final sentence, easy to lose): Workstream C
changed `V2_PROMPT` and added `TIEBREAK_PROMPT`, so the adjudicator flip regression suite is
MANDATORY before trusting any v3-adjudicated numbers: run
`tools/score_flips.py` (harnesseval repo) against the frozen
`tests/fixtures/flip_pairs.json` — zero flips on identical text is the hard gate; the
direction report (how many of the 116 now adjudicate `bug`) needs judge API spend and is
yield measurement, not a gate.

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
  a trivial-diff PR but the campaign discipline still applies.
- Do this AFTER the §5.2/§5.3 runs if the acceptance evidence is wanted in the release
  notes; before, if the user wants the lens upgrade shipped regardless. Ask if unclear.

## 3. Two queued taxonomy clauses (do NOT land before the §5.2/§5.3 runs)

Motivated by the bots' residuals on PRs #149/#150/#16 (the metareview-first-then-bots
yardstick measured what we missed). One clause each on the **Architecture** lens in
`rubrics/artifact-review-rubric.md` — no new lenses (parent §4 logic):

1. **Stand-in-guard-fidelity, dead-gate-boolean sub-case**: extend the existing hunt's
   examples with "a gate boolean that can never take its failing value (assigned true and
   never set false; an error branch that is unreachable as written)". Evidence:
   `tools/score_flips.py`'s gate 1 shipped with `gate1_ok = True` and no path to `False`
   (caught by CodeRabbit on harnesseval#16, fixed in 7b8a3ca).
2. **api-contract hunt, producer/consumer field-contract clause**: broaden "a response shape
   existing callers depend on" to intra-repo contracts — "a field one module writes and
   another reads disagree on name or shape (a fixture producer and its scorer, a config
   writer and its reader, parallel hand-maintained enumerations that can drift)". Evidence:
   the ce_key producer/consumer break (harnesseval#16) AND the repo's own founding scar —
   the four-copies-in-three-spellings lens-list drift that `internal/lens` was created to
   eliminate (see its package doc).

Each is one clause, pattern-list style, and must re-sync the lab adapter in the same cycle.

## 4. Class-3 residual: hand-edits under self-ignored artifact paths (decide, don't rush)

An LLM lens cannot reliably see "this generated file was hand-edited" — it needs a
deterministic signal. Options: (a) unshelve PR #93's generated-artifact-hygiene work
(closed unmerged — a validation agent found a real partition flaw; read its comments
first), or (b) a simpler lint flagging diffs that modify tracked files under
`docs/metareview/**` which no tool generated in the current session. No decision was made;
this is the lowest-priority item.

## Deviations from the parent handoff (already landed — flagged here for HUMAN acceptance, not to foreclose re-litigation)

The first deviation below contradicts the parent handoff's explicit §8 instruction; the
implementing session judged the parent's own invariant to override its letter (reasoning
recorded with the change), but a human should ratify or reverse it — reversing means
re-keying the v10 era to 20260831-adjacent merge-date semantics and re-running the affected
gates, so decide deliberately.

1. **v10 era keyed from 2026-09-09, not the 2026-09-08 merge date.** The parent said "the
   era date must be the merge date of workstream B" (§8), but its own invariant — "older
   completed logs would be judged against a lens that did not exist when they were written"
   — is violated by a day-granular era on the merge date when same-day pre-merge reviews
   exist (one did: the 2026-09-08-dated artifact review of CHANGELOG.md; the push gate
   demonstrated the retroactive blocker live). The 10-lens artifact review of CHANGELOG.md
   caught this (high). Keyed from the first FULL day instead; PR #150's CHANGELOG and the
   `lensEras` comment document the reasoning. Merged 2026-09-08 08:56 UTC, well inside the
   sub-day window.
2. **A sixth hunt in Workstream A** (Security's normalization-mismatch bypass, A01 + the
   artifact rubric's Security section): the parent's five paste-ready briefs created
   deferral loops the dogfood review forced closed; the receiving hunt is the consistency
   completion, not a benchmark-driven addition. Documented in #149's CHANGELOG entry.
3. **The dead-guard mechanism wording** was corrected against `analysis/ce_only.json`
   (pair 40, PR #10): the always-false `cmd_tuples > 0` guard kills the guarded INSERT
   while the paired DELETE destroys the old rows — the parent's paste text said "the guard
   makes the delete unconditional," which is self-contradictory (an always-false guard
   makes its gated statement dead). Substance unchanged; acceptance finding #3 unaffected.

## 5. Process learnings worth keeping (from the bots' residuals; see also issue #151)

- **Dogfood the lab repo too.** The harnesseval diff went through zero metareview loops and
  leaked 10 bot findings; the metareview PRs went through 13/7 dogfood iterations and
  leaked ~3 each, mostly in self-ignored artifact paths. Same splice-artifact bug class was
  caught four times where the loop existed and zero times where it didn't.
- **`docs/metareview/**` is a never-reviewed location for hand-written content** — the
  scaffold-placeholder and evidence-wording residuals lived exactly there. Until §4's
  decision lands, treat hand-edits under it as suspect.
- **Re-review prompts that say "treat earlier fixes as accepted" anchor reviewers** — the
  `8: true` coverage-thinning survived that framing (fixed in PR #150's ef82846).
