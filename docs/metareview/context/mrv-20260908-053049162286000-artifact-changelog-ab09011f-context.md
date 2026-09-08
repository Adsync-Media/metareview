# metareview context: CHANGELOG.md

Run ID: `mrv-20260908-053049162286000-artifact-changelog-ab09011f`

## Target

- Path: `CHANGELOG.md`
- Repository mode: `metaswarm-extension`
- Git branch: `main`
- Git head: `995a5b1`

## Artifact Excerpt

```markdown
# Changelog

## Unreleased

### Added

- **Testing-gap claims are now verified against the repository head, not just the diff (issue #146).**
  The #140 mechanism (PR #145) only searched covering-test evidence among added diff lines, so a
  covering test outside every changed hunk was invisible and a false absence claim could be confirmed.
  The adjudicator now also searches the repository at the pinned head: `claimcheck.SubjectTokens`
  exports the subject-token logic, `judge.RepoTestEvidence` runs a two-stage grep-then-read search
  over injectable git seams (`judge.GrepSeam`/`ShowSeam` — the single implementation of the pinned-rev
  contract shared with the escalation sandbox and the eval), and `ContextForGapClaim` injects the
  candidates' line-capped content under a provenance-checked disclosure. A completed-but-empty search
  is disclosed as the search record a testing-gap confirmation must cite; a failed or skipped search
  stays silent and is counted as `repo_search_errors` in the claimcheck rollup — an infrastructure
  failure never reads as evidence of absence. The prompt criterion demands that search record before
  a gap claim is confirmable. `cmd/claimcheck-eval -repos <dir>` adds the repo-side structural pass
  over the harnesseval corpus (combined diff×repo matrix, no-clone/repo-error rows disclosed); the
  A/B re-judge against the v2 ground truth needs model spend and stays open in the issue.
- **`cmd/claimcheck-ab`, the A/B re-judge driver for #146.** Re-judges the harnesseval gap-claim
  corpus twice with one judge model — arm A replays the #145-era prompt (diff-only context, the
  pre-#146 `RubricAddendum` verbatim from git history), arm B runs the production render (repo-side
  evidence + the current criterion) — and scores both arms against the v2 three-way ground truth
  (`readjudication3.json`), reporting per-arm hallucinated-confirm (false accepts), true-gap
  regression, and flip counts. Corpus PRs pin locally as per-PR refs (`refs/heads/pr-<N>`), repo
  evidence is cached per claim under `<out>/evidence/`, and `results.jsonl` is append-only and
  resumable, so the run survives interruption and repeats cost only the un-judged pairs.

### Changed

- **Coverage gate is now require-100 for the whole repository.** After the repo-wide campaign brought
  every package to 100% statement coverage with zero surviving mutants, the per-package floor
  (`tests/coverage-floor.txt`) and the transitional bash gate (`tests/coverage.sh`, with its
  `--update-floor`/`--allow-floor-decrease` flags) were removed. `make cover` (logic in
  `internal/covergate`) is now the sole gate: it requires every package `go list ./...` reports to be at
  exactly 100.0% of statements, except the packages named in the new `tests/coverage-exclude.txt`
  (the embed-only module root, `internal/version`, `cmd/covergate`, and the black-box
  `internal/githooktest`). A new package is required at 100% by default; excluding one is a deliberate,
  commented line. The module-root package's embedded git-hook assets are now guarded by a dedicated
  `githookassets_test.go` embed-integrity test.

### Fixed

- **PR-ready now selects findings for the target under review.** Findings linked to the current
  branch, live pull request, or a task review whose covered paths overlap the current diff retain
  their blocking effect. Unrelated historical blockers remain visible as repository-health
  advisories and in the findings index without blocking a different release target.
- **Unchanged PR-ready verdicts are reused from authenticated local run evidence.** Each run records
  a canonical digest binding target, head/base, diff, live pull-request state, evidence, reviewer
  implementation, and relevant finding frontier. A byte-identical rerun writes a receipt naming
  the reused run without invoking reviewers; any changed input starts a fresh review, and stale or
  cross-target explicit previous runs fail closed.

### Added

- **Mechanical-precision 
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
- Mechanical-precision
