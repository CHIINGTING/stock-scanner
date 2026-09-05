package healthcheck

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/store"
)

func openTestHistory(t *testing.T) *History {
	t.Helper()
	h, err := OpenHistory(HistoryConfig{Enabled: true, Path: ":memory:"})
	if err != nil {
		t.Fatalf("open history: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// savedHealth is a full check with real figures, so the persistence tests are not exercising
// an empty struct.
func savedHealth(t *testing.T) *Health {
	t.Helper()
	s, root := valService(t, "2026-09-04")
	writeSessions(t, root, 25, "2026-09-04", 18)
	return check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
}

// ── it reuses the R13 schema ──────────────────────────────────────────────────────────

// The chain a health check writes is the SAME chain a scan writes, so M10's outcome
// pipeline works on it without a second implementation.
func TestSaveUsesTheExistingChain(t *testing.T) {
	h := openTestHistory(t)
	hc := savedHealth(t)

	id, err := h.Save(context.Background(), hc)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if id == 0 {
		t.Fatal("no snapshot id returned; M10 has nothing to anchor an outcome to")
	}

	ctx := context.Background()
	// The snapshot exists, is marked as a health check, and carries the basis price.
	runs, err := h.store.Scans().RunsByDate(ctx, "2026-09-04")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, err %v", runs, err)
	}
	if runs[0].Note != HistoryNote || runs[0].ScannerVersion != HistoryVersion {
		t.Fatalf("the run is not self-describing: %+v", runs[0])
	}
	if runs[0].FinishedAt == nil {
		t.Fatal("the run was left in flight")
	}

	snaps, err := h.store.Scans().SnapshotsByRun(ctx, runs[0].ID)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %v, err %v", snaps, err)
	}
	snap := snaps[0]
	if snap.SourceTab != TabHealthCheck {
		t.Fatalf("source_tab = %q — a health check must be distinguishable from a scan tab",
			snap.SourceTab)
	}
	if snap.Symbol != "2330" || snap.Name != "台積電" {
		t.Fatalf("identity not preserved: %+v", snap)
	}
	if snap.Close == nil {
		t.Fatal("no basis close; no outcome could ever be measured from this row")
	}
	if *snap.Close != 759 {
		t.Fatalf("close = %v, want the check's own 759", *snap.Close)
	}
}

// An absent figure produces NO ROW rather than a zero. That is what lets a later query tell
// "there was no P/E" from "the P/E was zero".
func TestMissingFiguresProduceNoEvidenceRow(t *testing.T) {
	h := openTestHistory(t)
	// No valuation archive at all, so the whole valuation block is unavailable.
	s, _ := fundService(t, "2026-09-04")
	hc := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})

	id, err := h.Save(context.Background(), hc)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, err := h.store.Evidence().BySnapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]store.Evidence{}
	for _, r := range rows {
		byKey[r.Category+"/"+r.Key] = r
	}
	for _, absent := range []string{
		"valuation/trailing_pe", "valuation/historical_pe_median",
		"valuation/historical_pe_percentile", "target_price/eps",
		"target_price/margin_of_safety", "target_price/scenario_base_target",
	} {
		if got, ok := byKey[absent]; ok {
			t.Errorf("%s was written with no figure behind it: %+v", absent, got)
		}
	}
	// And no row anywhere carries a status word as though it were a value.
	for k, r := range byKey {
		if r.TextValue != nil {
			switch *r.TextValue {
			case "INSUFFICIENT_DATA", "UNAVAILABLE", "NOT_IMPLEMENTED":
				// A status is a legitimate TEXT value for a status key, but never for a
				// key that names a figure.
				if k != "target_price/status" {
					t.Errorf("%s stores a status word as its value: %q", k, *r.TextValue)
				}
			}
		}
		if r.NumValue != nil && r.TextValue != nil {
			t.Errorf("%s carries both a number and text", k)
		}
	}
}

// §23: everything needed to grade this call later is on the row.
func TestEverythingNeededForOutcomeValidationIsStored(t *testing.T) {
	h := openTestHistory(t)
	hc := savedHealth(t)
	id, err := h.Save(context.Background(), hc)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, err := h.store.Evidence().BySnapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, r := range rows {
		have[r.Category+"/"+r.Key] = true
	}
	for _, want := range []string{
		"price/as_of", "price/close", "price/canonical_symbol",
		"assessment/verdict", "assessment/decided_by",
		"assessment/threshold_margin_attractive", "assessment/threshold_percentile_expensive",
		"target_price/rule", "target_price/eps_basis", "target_price/eps_base_date",
		"target_price/scenario_base_target", "target_price/margin_of_safety",
		"valuation/historical_pe_samples", "valuation/trailing_pe",
		"technical/scanner_action",
	} {
		if !have[want] {
			t.Errorf("missing evidence %s", want)
		}
	}
}

// The AI's result is recorded WITH its status, including its failures — "the model was down"
// and "we never asked" must stay distinguishable.
func TestAIResultIsRecordedIncludingFailures(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		aiStatus   Status
		wantStatus string
		wantRun    string
	}{
		{"available", StatusAvailable, "OK", store.RunStatusComplete},
		{"disabled", StatusDisabled, "DISABLED", store.RunStatusFailed},
		{"unreachable", StatusUnavailable, "ERROR", store.RunStatusFailed},
	} {
		h := openTestHistory(t)
		hc := savedHealth(t)
		hc.AI = AIBlock{Status: tc.aiStatus, Model: "test-model", Summary: "摘要",
			Reason: "理由", Confidence: ConfidenceLow}

		id, err := h.Save(ctx, hc)
		if err != nil {
			t.Fatalf("%s: save: %v", tc.name, err)
		}
		agents, err := h.store.Analyses().AgentAnalysesBySnapshot(ctx, id)
		if err != nil || len(agents) != 1 {
			t.Fatalf("%s: agents = %v, err %v", tc.name, agents, err)
		}
		a := agents[0]
		if a.Agent != AgentHealthJudge {
			t.Errorf("%s: agent = %q", tc.name, a.Agent)
		}
		if a.Status != tc.wantStatus {
			t.Errorf("%s: status = %q, want %q", tc.name, a.Status, tc.wantStatus)
		}
		if a.Model != "test-model" {
			t.Errorf("%s: the model that spoke was not recorded", tc.name)
		}
		// The verdict column stays empty: the judge writes prose, and the page's verdict is
		// the deterministic assessment. Filling it would attribute that call to the model.
		if a.Verdict != "" {
			t.Errorf("%s: a verdict was attributed to the judge: %q", tc.name, a.Verdict)
		}
		if a.OutputJSON == "" {
			t.Errorf("%s: the AI block was not stored", tc.name)
		} else {
			var back AIBlock
			if err := json.Unmarshal([]byte(a.OutputJSON), &back); err != nil {
				t.Errorf("%s: stored AI JSON does not round-trip: %v", tc.name, err)
			} else if back.Status != tc.aiStatus {
				t.Errorf("%s: round-tripped status = %s", tc.name, back.Status)
			}
		}

		runs, err := h.store.Analyses().AnalysisRunsBySnapshot(ctx, id)
		if err != nil || len(runs) != 1 {
			t.Fatalf("%s: analysis runs = %v, err %v", tc.name, runs, err)
		}
		if runs[0].Status != tc.wantRun {
			t.Errorf("%s: run status = %q, want %q", tc.name, runs[0].Status, tc.wantRun)
		}
		if runs[0].PromptSetVersion != PromptSetVersion {
			t.Errorf("%s: prompt set version not recorded", tc.name)
		}
	}
}

// No decision row is ever written. The decisions table is a population R13-M7 counts, and a
// health check is neither the scanner's call nor the R13 judge's.
func TestNoDecisionRowIsWritten(t *testing.T) {
	h := openTestHistory(t)
	hc := savedHealth(t)
	id, err := h.Save(context.Background(), hc)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	decisions, err := h.store.Analyses().DecisionsBySnapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 0 {
		t.Fatalf("a health check wrote %d decision rows into a population it does not belong "+
			"to: %+v", len(decisions), decisions)
	}
}

// The whole point of reusing the schema: an outcome can be attached to a health check with
// the existing repository, no second pipeline.
func TestOutcomeCanBeAttachedToAHealthCheck(t *testing.T) {
	ctx := context.Background()
	h := openTestHistory(t)
	hc := savedHealth(t)
	id, err := h.Save(ctx, hc)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	base, _ := hc.Price.Close.Float()
	if _, err := h.store.Outcomes().Ensure(ctx, id, base); err != nil {
		t.Fatalf("ensure outcome: %v", err)
	}
	if _, err := h.store.Outcomes().SetReturn(ctx, id, store.H5D, 4.2); err != nil {
		t.Fatalf("set return: %v", err)
	}
	got, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Return5D == nil || *got.Return5D != 4.2 {
		t.Fatalf("outcome = %+v", got)
	}
	if got.BaseClose != base {
		t.Fatalf("base close = %v, want %v", got.BaseClose, base)
	}
}

// Saving twice must not collide: two checks of the same stock on the same day are two
// legitimate judgements, and the schema's UNIQUE(scan_run_id, symbol, source_tab) allows
// them because each gets its own run.
func TestTwoChecksOfOneStockOnOneDayBothPersist(t *testing.T) {
	ctx := context.Background()
	h := openTestHistory(t)
	hc := savedHealth(t)

	first, err := h.Save(ctx, hc)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := h.Save(ctx, hc)
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if first == second {
		t.Fatal("the second check overwrote the first")
	}
	runs, err := h.store.Scans().RunsByDate(ctx, "2026-09-04")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	if runs[0].RunUID == runs[1].RunUID {
		t.Fatal("both checks share a run uid")
	}
}

// A history failure must not fail the request. The person waiting asked a question, not for
// a database write.
func TestHistoryFailureDoesNotFailTheCheck(t *testing.T) {
	s, root := valService(t, "2026-09-04")
	writeSessions(t, root, 25, "2026-09-04", 18)

	// A store whose database has been closed underneath it: every write errors.
	h := openTestHistory(t)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	s.History = h

	got, err := s.Check(context.Background(), Request{Symbol: "2330", AsOf: "2026-09-04"})
	if err != nil {
		t.Fatalf("a failed history write took down the whole check: %v", err)
	}
	if got.Price.Status != StatusAvailable {
		t.Fatalf("the answer was degraded by a persistence failure: %+v", got.Price)
	}
	if got.SnapshotID != 0 {
		t.Fatalf("a snapshot id was reported for a write that failed: %d", got.SnapshotID)
	}
}

// With history enabled the check reports the row it was recorded as, which is what M10
// needs to grade it later.
func TestCheckReportsItsSnapshotID(t *testing.T) {
	s, root := valService(t, "2026-09-04")
	writeSessions(t, root, 25, "2026-09-04", 18)
	s.History = openTestHistory(t)

	got := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if got.SnapshotID == 0 {
		t.Fatal("no snapshot id reported with history enabled")
	}
	rows, err := s.History.store.Evidence().BySnapshot(context.Background(), got.SnapshotID)
	if err != nil || len(rows) == 0 {
		t.Fatalf("the reported id has no evidence behind it: %d rows, err %v", len(rows), err)
	}
}

// With history off, nothing is written and no id is claimed.
func TestHistoryOffWritesNothing(t *testing.T) {
	s, root := valService(t, "2026-09-04")
	writeSessions(t, root, 25, "2026-09-04", 18)

	got := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if got.SnapshotID != 0 {
		t.Fatalf("a snapshot id was reported with history disabled: %d", got.SnapshotID)
	}
}
