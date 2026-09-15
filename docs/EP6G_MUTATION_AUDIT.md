# EP-6G mutation audit

Status: EP-6G is implemented; review status is tracked in README.md. This file records one audit run; it is not
a review verdict.

## Run

- Started 2026-09-15 05:55:10 CST, finished 2026-09-15 05:56:13 CST, exit status 0.
- Worktree: uncommitted EP-6G tree after review fix rounds 1 and 2.
- Re-run 2026-09-15 (wall clock 12:42:25–12:43:27 CST), exit status 0, same command, after EP-8 added one
  line to `cmd/scanner/main.go` (`ShowEntryPlan: cfg.Scanner.ShowEntryPlan` in the report options). The
  re-run printed `summary: 19 killed, 19 total; final production hashes match baseline`, the same result
  and red test for every row below.
- Re-run again 2026-09-15 (wall clock 14:37:11–14:38:12 CST), exit status 0, same command, after the EP-8
  review replaced the `TODO(EP-8 …)` comment block in `internal/scanner/entryplan_attach.go` with what
  EP-8 decided (a COMMENT-ONLY change; no code line moved). Same result again:
  `summary: 19 killed, 19 total; final production hashes match baseline`, same red test per row.
- The `cmd/scanner/main.go` and `internal/scanner/entryplan_attach.go` hashes in the Hashes block are the
  `baseline` lines printed by those two re-runs; the other four were unchanged in both.
- Command, and the ONLY runner that produced every row below:

```text
env GOCACHE=/tmp/ep6g-runner-gocache go run ./scripts/ep6g_mutation
```

## What the runner checks (scripts/ep6g_mutation/main.go)

For each mutant, applied to one production file:

1. every exact-text anchor occurs exactly once (R8a2 applies two replacements in the same file);
2. at least one changed line is code, not blank and not a `//` comment;
3. the file's SHA-256 changes;
4. `go build ./...` passes on the mutant (otherwise the row is `INVALID`, not a kill);
5. the named focused test runs; a non-zero exit counts as `KILLED` only when the output contains
   `--- FAIL:` (an assertion failure);
6. the original bytes are written back and the file's SHA-256 is re-checked against the baseline
   before the next mutant (a mismatch aborts the run).

Before any mutant it runs every distinct test command once on the unmutated tree and aborts if any
fails. After the last mutant it re-checks every touched file against its baseline. A deferred
restore rewrites every touched file on a panic. It does NOT run on `os.Exit`, but the runner only
calls `os.Exit` before the first mutation, or after the current file has already been written back.

Round 1's R8a/R8b/R8d/R8e/F-1 results were first produced by an ad-hoc scratchpad script. Those
rows were then added to this runner, and **every row in this document comes from the run above**,
not from the ad-hoc script.

## Results

| Mutant | File | Mutation | Result | Red test | First assertion message (abridged) |
| --- | --- | --- | --- | --- | --- |
| M1 | entryplan/input.go | `PermitsCeiling` drops `&& s != SuitabilityUnsuitable` | KILLED | `TestTheRiskCasesProduceWhatTheyWereBuiltFor` | `target 2 = 110, want 117 (ceiling "APPLIED")` |
| M2 | entryplan/input.go | `ValuationEvidence.Usable` drops `v.Status.OK()` | KILLED | `TestUnavailableValuationCannotFabricateACeiling` | `unavailable valuation changed the plan` |
| M3 | valuation/entryplan_evidence.go | BASE scenario replaced by BULL | KILLED | `TestBuildEntryPlanEvidenceProjectsOnlyPITBaseScenario` | `projection = {…}, want available BASE=200 …` |
| M4 | entryplan/targets.go | Target2 ceiling never applied | KILLED | `TestTheValuationCeilingLowersTargetTwoAndNeverRemovesIt` | `target 2 = 117, want 110` |
| M5 | entryplan/targets.go | ceiling comparison `<` → `>` | KILLED | `TestTheValuationCeilingLowersTargetTwoAndNeverRemovesIt` | `target 2 = 117, want 110` |
| M6 | entryplan/targets.go | allow capped Target2 below Target1 | KILLED | `TestAnInvertedTargetPairIsWithdrawnRatherThanReordered` | `target 2 = 100 — a ceiling at 100 is below the first target …` |
| M7 | entryplan/plan.go | usable valuation forces status | KILLED | `TestEntryPlanValuationProjectionIsShadowOnlyAndOptional` | `unsuitable valuation changed plan: …` |
| M8 | entryplan/plan.go | pre-decision contradiction skipped when valuation usable | KILLED | `TestEntryPlanValuationCannotEscapeSellContradiction` | `sell contradiction escaped: … Status:INSUFFICIENT_DATA …` |
| M9 | entryplan/plan.go | valuation target written into S1 `EntryTrace.Targets` | KILLED | `TestEntryPlanValuationCannotEscapeSellContradiction` | `sell contradiction escaped: … Status:INSUFFICIENT_DATA …` |
| M10 | cmd/scanner/main.go | entry-date check `!=` → `>` | KILLED | `TestEntryPlanValuationRejectsEntryBeforeResearchLoadAsOf` | `later-loaded research could cap the older plan: …` |
| M11 | entryplan/targets.go | Target2 tick rounding bypassed | KILLED | `TestEveryEP4PriceIsAlignedDownFromItsOwnRawValue` | `target 2 = 7.6949999999999985 is not on the tick grid …` |
| M12 | entryplan/plan.go | non-S1 unusable valuation row becomes AVAILABLE with value 0 | KILLED | `TestUnavailableValuationCannotFabricateACeiling` | `unavailable valuation fabricated target evidence: {… Status:AVAILABLE …}` |
| R8a | valuation/entryplan_evidence.go | drop `\|\| v.TrailingDate > asOf` | KILLED | `TestBuildEntryPlanEvidenceRefusesAFutureTrailingDate` | `future TrailingDate was projected: got {INSUFFICIENT_DATA <nil> SUITABLE HISTORICAL_PERCENTILE}, want the unprojected {UNAVAILABLE <nil> INSUFFICIENT_DATA}` |
| R8a2 | valuation/entryplan_evidence.go | R8a plus drop `d <= asOf` in the close loop | KILLED | `TestBuildEntryPlanEvidenceRefusesAFutureTrailingDate` | `future TrailingDate produced a BASE target 220` |
| R8b | valuation/entryplan_evidence.go | close match `d == TrailingDate` → `d <= TrailingDate` | KILLED | `TestBuildEntryPlanEvidenceRequiresTheCloseOnTheTrailingDate` | `no close on TrailingDate 2026-09-08, yet a BASE target 180 was projected (an earlier session's close was used …)` |
| R8d | scanner/entryplan_attach.go | drop `&& v.AsOf == snap.AsOf` | KILLED | `TestAttachEntryPlanIgnoresValuationForAnotherSession` | `valuation stamped 2026-09-09 capped the 2026-09-10 plan …` |
| R8e | valuation/entryplan_evidence.go | PublishedAt `<= asOf` → `true` | KILLED | `TestBuildEntryPlanEvidenceTreatsAFuturePublishedFilingAsAbsent` | `a filing published 2026-09-12 was used on 2026-09-10: got …SUITABLE, want as if absent …CONDITIONAL` |
| R8e2 | valuation/entryplan_evidence.go | drop `f.ObservedAt… <= asOf &&` | KILLED | `TestBuildEntryPlanEvidenceTreatsAFutureObservedFilingAsAbsent` | `a filing observed 2026-09-11 was used on 2026-09-10: got …SUITABLE, want as if absent …CONDITIONAL` |
| F1R | entryplan/plan.go | revert F-1: S1 `VALUATION_BASE_TARGET` row carries the value | KILLED | `TestSellSidePlanJSONCarriesNoValuationTarget` | `SELL: valuation target 187.6543 leaked into the serialized S1 plan: …` |

Summary: **19 killed, 0 survived, 0 invalid** (runner line: `summary: 19 killed, 19 total; final
production hashes match baseline`).

### Notes on rows whose meaning changed since the first audit

- **M1–M12 are unchanged mutations.** Every anchor still occurs exactly once in the current source.
  None was dropped as inapplicable.
- **M3's test filter** is the prefix `TestBuildEntryPlanEvidence`. Since fix round 1 that prefix
  matches five tests, not two. The kill comes from `TestBuildEntryPlanEvidenceProjectsOnlyPITBaseScenario`.
- **M8 and M9 are killed by status, not by trace or JSON content.** Both mutants leave the plan at
  `INSUFFICIENT_DATA`, so the `Status != NO_VALID_ENTRY` check fails first. This run does not show
  that the test's own trace-content check (`containsJSONNumber`) would catch M9 by itself. The
  whole-plan JSON guarantee is what F1R and `TestSellSidePlanJSONCarriesNoValuationTarget` pin.
- **M12's anchor sits in a different branch now.** Since fix round 1, plan.go is
  `if contradicted {…} else if in.Valuation.Usable() {…} else if valEv.Status == Available || … {…}`.
  The mutated arm now runs only for non-S1 plans, so M12 is "a non-S1 unusable valuation fabricates
  an AVAILABLE 0". The first audit's note that M12 required an extra assertion in
  `TestUnavailableValuationCannotFabricateACeiling` still applies; that assertion is what kills it.
- **R8a is killed by Status / Model / Suitability, not by the target.** The close loop's own
  `d <= asOf` check also keeps the future close out, so the BASE target stays nil. R8a2 removes
  both guards and shows the no-target assertion fires.

## Hashes

SHA-256 of every file the runner touched. The baseline was taken at the start of the run; all were
re-verified equal after each mutant and at the end, and re-checked with `shasum -a 256` after the
run:

```text
144b40b728298f2989aac6d70f1c56bb4214306b05fa6a5671adf5e983e01bb0  cmd/scanner/main.go
4ae5953632c028f90089b15ab90dd5255d30a6ca6abd027c08d143de0eec028e  internal/entryplan/input.go
f316dd5dbc8e0440cd97bb038512911c6223ecbec074d7f4f9c74cd6c77fc947  internal/entryplan/plan.go
00dd22aec4f08bf4698d3e91b1ad10c8966aacb911fef543c641a72b41e58dd9  internal/entryplan/targets.go
5b6ebc82eaef12893051e5396e4807aeb8ed6f5b66859723877fe28ed76c7b56  internal/scanner/entryplan_attach.go
005cb9113f0cb5b79dfe2aa58b779e535a8901af6d10c9f2625f9c360ca9b0df  internal/valuation/entryplan_evidence.go
```

These hashes describe this run only. Any later edit to these files makes this table stale, and the
audit must be re-run rather than the hashes updated by hand.
