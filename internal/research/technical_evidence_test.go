package research

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
	"github.com/deep-huang/stock-scanner/internal/technical"
)

// analyzeFixture builds a compounding price path and runs it through the REAL Analyze path,
// so the fixture cannot drift away from what the scanner would actually attach.
func analyzeFixture(n int, start, drift float64) *technical.Result {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]technical.Bar, n)
	p := start
	for i := 0; i < n; i++ {
		next := p * (1 + drift)
		bars[i] = technical.Bar{
			Date:   base.AddDate(0, 0, i),
			Open:   p,
			High:   math.Max(p, next) * 1.01,
			Low:    math.Min(p, next) * 0.99,
			Close:  p,
			Volume: 1000,
		}
		p = next
	}
	return technical.Analyze(bars, technical.DefaultConfig())
}

// recordWithTechnical persists one entry carrying the supplied technical result.
func recordWithTechnical(t *testing.T, rec *Recorder, e scanner.WatchlistEntry) map[string]store.Evidence {
	t.Helper()
	res, err := rec.RecordScan(context.Background(), RunMeta{TradingDate: "2026-08-20"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	return evidenceMap(t, rec.Store(), res.WatchlistSnapshots[e.A.Symbol])
}

// R14 must use the EXISTING R13 chain — scan_runs → stock_snapshots → evidence — not a
// second persistence system. This asserts the rows land in that table, reachable from the
// snapshot, with the run's own identity above them.
func TestTechnicalEvidenceUsesTheExistingR13Chain(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("2330")
	e.Technical = analyzeFixture(200, 100, 0.015)

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-20"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	snapID := res.WatchlistSnapshots["2330"]

	rows, err := rec.Store().Evidence().ByCategory(ctx, snapID, store.CategoryTechnical)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var techRows int
	for _, r := range rows {
		if strings.HasPrefix(r.Key, "tech_") {
			techRows++
			if r.Source != "technical.Result" {
				t.Errorf("%s: source = %q, want the technical layer", r.Key, r.Source)
			}
		}
	}
	if techRows == 0 {
		t.Fatal("no R14 evidence was written")
	}

	// Traceable all the way up: evidence → snapshot → run → date.
	snap, err := rec.Store().Scans().Snapshot(ctx, snapID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Symbol != "2330" || snap.TradingDate != "2026-08-20" {
		t.Errorf("snapshot does not identify the stock and date: %+v", snap)
	}
	run, err := rec.Store().Scans().RunByUID(ctx, res.RunUID)
	if err != nil || run.ID != snap.ScanRunID {
		t.Errorf("evidence is not reachable from its scan run: %v", err)
	}
}

// The parameters must travel with the values. A row saying "ADX 31.8" is worthless later if
// nobody can tell whether it was a 14- or a 20-period ADX.
func TestTechnicalEvidenceStoresItsParameters(t *testing.T) {
	rec, _ := openRecorder(t)
	e := bareEntry("2330")
	e.Technical = analyzeFixture(200, 100, 0.015)
	ev := recordWithTechnical(t, rec, e)

	for key, want := range map[string]float64{
		"tech_adx_period":         14,
		"tech_rsi_period":         14,
		"tech_macd_fast":          12,
		"tech_macd_slow":          26,
		"tech_macd_signal_period": 9,
		"tech_keltner_ema_period": 20,
		"tech_keltner_atr_period": 20,
		"tech_keltner_multiplier": 2,
	} {
		got, ok := ev[key]
		if !ok {
			t.Errorf("%s was not persisted — the reading is uninterpretable without it", key)
			continue
		}
		if got.NumValue == nil || *got.NumValue != want {
			t.Errorf("%s = %v, want %v", key, got.NumValue, want)
		}
	}
}

// Raw values must be machine-readable numbers, not formatted strings.
func TestTechnicalEvidenceIsMachineReadable(t *testing.T) {
	rec, _ := openRecorder(t)
	e := bareEntry("2330")
	e.Technical = analyzeFixture(200, 100, 0.015)
	ev := recordWithTechnical(t, rec, e)

	for _, key := range []string{
		"tech_adx", "tech_plus_di", "tech_minus_di", "tech_rsi",
		"tech_macd", "tech_macd_signal", "tech_macd_histogram",
		"tech_keltner_upper", "tech_keltner_middle", "tech_keltner_lower", "tech_keltner_width_pct",
		"tech_pivot", "tech_pivot_r1", "tech_pivot_s1", "tech_pivot_distance_pct",
	} {
		got, ok := ev[key]
		if !ok {
			t.Errorf("%s is missing", key)
			continue
		}
		if got.NumValue == nil {
			t.Errorf("%s was stored as text %q, want a number", key, deref(got.TextValue))
		}
	}
	// And the labels are labels.
	for _, key := range []string{
		"tech_adx_direction", "tech_adx_strength", "tech_rsi_zone", "tech_macd_cross",
		"tech_keltner_position", "tech_keltner_channel", "tech_pivot_location",
		"tech_context_direction", "tech_context_trend_strength", "tech_context_momentum",
		"tech_context_volatility", "tech_context_price_location",
	} {
		if got, ok := ev[key]; !ok || got.TextValue == nil {
			t.Errorf("%s is missing or not a label", key)
		}
	}
}

// The pivot's derivation window must be recorded: it is what proves the levels came from the
// session BEFORE the one being evaluated.
func TestPivotBasisDateIsPersisted(t *testing.T) {
	rec, _ := openRecorder(t)
	e := bareEntry("2330")
	e.Technical = analyzeFixture(200, 100, 0.015)
	ev := recordWithTechnical(t, rec, e)

	got, ok := ev["tech_pivot_basis_date"]
	if !ok || got.TextValue == nil {
		t.Fatal("the pivot basis date was not persisted — look-ahead becomes unauditable")
	}
	if *got.TextValue == "" {
		t.Error("the basis date is empty")
	}
}

// A non-OK indicator writes its STATUS and nothing else. A fabricated 0 would poison the
// very dataset meant to decide whether the indicator is worth anything.
func TestNonOKIndicatorWritesStatusOnly(t *testing.T) {
	rec, _ := openRecorder(t)
	e := bareEntry("2330")
	// 32 bars: enough for ADX (28) but short of MACD (36).
	e.Technical = analyzeFixture(32, 100, 0.5)
	ev := recordWithTechnical(t, rec, e)

	st, ok := ev["tech_macd_status"]
	if !ok || st.TextValue == nil || *st.TextValue != "INSUFFICIENT_DATA" {
		t.Fatalf("MACD status = %v, want INSUFFICIENT_DATA recorded", st.TextValue)
	}
	for _, absent := range []string{
		"tech_macd", "tech_macd_signal", "tech_macd_histogram", "tech_macd_cross",
		"tech_macd_fast", "tech_macd_momentum",
	} {
		if _, ok := ev[absent]; ok {
			t.Errorf("%q was written for an unavailable MACD", absent)
		}
	}
	// ADX did publish, so it must be there in full.
	if _, ok := ev["tech_adx"]; !ok {
		t.Error("one unavailable indicator must not suppress the others")
	}
}

// Feature off → not a single R14 row. Absence of the layer must be absence in the store.
func TestNoTechnicalResultWritesNoTechnicalEvidence(t *testing.T) {
	rec, _ := openRecorder(t)
	ev := recordWithTechnical(t, rec, bareEntry("2330"))

	for key := range ev {
		if strings.HasPrefix(key, "tech_") {
			t.Errorf("%q was written with R14 disabled", key)
		}
	}
	// The base technical facts the scanner always produces are still there.
	if _, ok := ev["ma20"]; !ok {
		t.Error("the base scan evidence went missing")
	}
}

// The context signals are stored as one set, because they describe a single reading and
// splitting them would make "which fired together" a reassembly job for every consumer.
func TestContextSignalsAreStoredAsASet(t *testing.T) {
	rec, _ := openRecorder(t)
	e := bareEntry("2330")
	e.Technical = analyzeFixture(200, 100, 0.03)
	ev := recordWithTechnical(t, rec, e)

	got, ok := ev["tech_context_signals"]
	if !ok || got.TextValue == nil {
		t.Fatal("the signal set was not persisted")
	}
	if *got.TextValue == "" {
		t.Skip("this fixture produced no signals; nothing to assert about the encoding")
	}
	for _, sig := range strings.Split(*got.TextValue, "|") {
		if sig == "" {
			t.Error("the signal list contains an empty entry")
		}
		// Still evidence, never an instruction.
		for _, token := range strings.Split(sig, "_") {
			switch token {
			case "BUY", "SELL", "ADD", "REDUCE", "AVOID":
				t.Errorf("a persisted signal contains the instruction %q: %s", token, sig)
			}
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
