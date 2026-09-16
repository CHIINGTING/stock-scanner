package recon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func bars(dates ...string) []fetcher.Candle {
	out := make([]fetcher.Candle, 0, len(dates))
	for i, d := range dates {
		t, err := time.Parse(DateLayout, d)
		if err != nil {
			panic(err)
		}
		p := 100.0 + float64(i)
		out = append(out, fetcher.Candle{Date: t, Open: p, High: p + 1, Low: p - 1,
			Close: p, AdjClose: p, Volume: 1_000_000})
	}
	return out
}

func cacheOf(t *testing.T, syms map[string][]string) *Cache {
	t.Helper()
	c := &Cache{}
	set := map[string]struct{}{}
	for sym, dates := range syms {
		s := &Symbol{Data: fetcher.StockData{Symbol: sym, Candles: bars(dates...)},
			idxOf: map[string]int{}}
		for i, d := range dates {
			s.idxOf[d] = i
			set[d] = struct{}{}
		}
		c.Symbols = append(c.Symbols, s)
	}
	for d := range set {
		c.Axis = append(c.Axis, d)
	}
	sortStrings(c.Axis)
	return c
}

func sortStrings(x []string) {
	for i := 1; i < len(x); i++ {
		for j := i; j > 0 && x[j] < x[j-1]; j-- {
			x[j], x[j-1] = x[j-1], x[j]
		}
	}
}

// ── Point-in-time truncation ──────────────────────────────────────────────────────────────

// TestTruncateAtIsAPointInTimeCut is the guard the whole reconstruction rests on: the bars a
// session sees are bars[:i+1] and appending later sessions cannot change them.
func TestTruncateAtIsAPointInTimeCut(t *testing.T) {
	dates := []string{"2026-09-08", "2026-09-09", "2026-09-10", "2026-09-11", "2026-09-14"}
	c := cacheOf(t, map[string][]string{"2330": dates})

	got := c.TruncateAt("2026-09-10", 1)
	if len(got) != 1 {
		t.Fatalf("%d symbols, want 1", len(got))
	}
	if n := len(got[0].Candles); n != 3 {
		t.Fatalf("truncated series has %d bars, want 3 (the signal bar is the LAST one)", n)
	}
	last := got[0].Candles[2].Date.Format(DateLayout)
	if last != "2026-09-10" {
		t.Fatalf("truncated series ends on %s, want 2026-09-10 — entryPlanSeriesFor "+
			"(entryplan_attach.go:703) refuses any other alignment", last)
	}
	for _, k := range got[0].Candles {
		if k.Date.Format(DateLayout) > "2026-09-10" {
			t.Fatalf("a bar dated %s survived the cut", k.Date.Format(DateLayout))
		}
	}
}

func TestASymbolThatDidNotTradeOnTheSessionIsOmittedNotCarriedForward(t *testing.T) {
	c := cacheOf(t, map[string][]string{
		"2330": {"2026-09-09", "2026-09-10", "2026-09-11"},
		"2317": {"2026-09-09", "2026-09-11"}, // suspended on 09-10
	})
	got := c.TruncateAt("2026-09-10", 1)
	if len(got) != 1 || got[0].Symbol != "2330" {
		t.Fatalf("got %d symbols %v, want only 2330 — carrying 2317 with a 09-09 last bar "+
			"would put a different session's analysis in this session's population", len(got), got)
	}
}

func TestTruncateAtEnforcesTheHistoryMinimum(t *testing.T) {
	c := cacheOf(t, map[string][]string{"2330": {"2026-09-09", "2026-09-10", "2026-09-11"}})
	if n := len(c.TruncateAt("2026-09-10", 2)); n != 1 {
		t.Fatalf("2 bars of history with minBars=2 gave %d symbols, want 1", n)
	}
	if n := len(c.TruncateAt("2026-09-10", 3)); n != 0 {
		t.Fatalf("2 bars of history with minBars=3 gave %d symbols, want 0", n)
	}
}

func TestEligibleSessionsLeavesRoomForHistoryAndTheOutcomeWindow(t *testing.T) {
	c := &Cache{Axis: []string{"d01", "d02", "d03", "d04", "d05", "d06", "d07", "d08"}}
	got := c.EligibleSessions(3, 2)
	want := []string{"d03", "d04", "d05", "d06"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if n := len(c.EligibleSessions(9, 0)); n != 0 {
		t.Fatalf("a history requirement longer than the axis gave %d sessions", n)
	}
	if n := len(c.EligibleSessions(1, 99)); n != 0 {
		t.Fatalf("a forward window longer than the axis gave %d sessions", n)
	}
}

// TestEligibilityBarsIsTheProductionRead pins the constant against the production reads it is
// derived from, so raising or lowering it has to be argued rather than typed.
func TestEligibilityBarsIsTheProductionRead(t *testing.T) {
	if EligibilityBars < 30 {
		t.Fatalf("EligibilityBars is %d; EnrichWatchlist skips a stock below 30 candles "+
			"(watchlist.go:187) and computeRocket returns WAIT below 30 (rocket.go:97)",
			EligibilityBars)
	}
	if EligibilityBars != 61 {
		t.Fatalf("EligibilityBars is %d, want 61: analyzeConsolidation builds PivotHigh over "+
			"candles[n-1-min(60,n-1) : n-1] in BOTH branches (consolidation.go:85 and :100-101), "+
			"so below 61 bars the breakout pivot — an entryplan level — is silently computed "+
			"over a shorter window than production would use", EligibilityBars)
	}
}

// ── The sector projection ─────────────────────────────────────────────────────────────────

// TestSectorOfMatchesTheProductionRule compares SectorOf against an INDEPENDENT second
// implementation of cmd/scanner/main.go:896-922, written from that function rather than from
// SectorOf. buildSectorOf lives in package main and cannot be imported, so the only honest
// options are a transcription plus a cross-check, or a transcription plus nothing.
func TestSectorOfMatchesTheProductionRule(t *testing.T) {
	p := &SectorPanel{
		Order: []string{"AI", "Shipping", "Steel"},
		Members: map[string][]string{
			"AI":       {"2330", "3037"},
			"Shipping": {"2603", "2330"}, // 2330 is in two sectors on purpose
			"Steel":    {"2002"},
		},
	}
	// Ranked order is the opportunity order and it WINS: Shipping outranks AI here, so 2330
	// must resolve to Shipping even though AI lists it first in file order.
	ranked := []scanner.SectorRotation{{Name: "Shipping"}, {Name: "AI"}}

	got := p.SectorOf(ranked)
	want := independentSectorOf(p, ranked)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s → %q, want %q", k, got[k], v)
		}
	}
	if got["2330"] != "Shipping" {
		t.Fatalf("2330 → %q, want Shipping — the ranked order must win over file order",
			got["2330"])
	}
	if got["2002"] != "Steel" {
		t.Fatalf("2002 → %q, want Steel — a sector the rotation skipped must still resolve",
			got["2002"])
	}
}

// independentSectorOf is written from cmd/scanner/main.go:896-922, not from SectorOf.
func independentSectorOf(p *SectorPanel, ranked []scanner.SectorRotation) map[string]string {
	out := map[string]string{}
	for _, r := range ranked {
		for _, c := range p.Members[r.Name] {
			if _, seen := out[c]; !seen {
				out[c] = r.Name
			}
		}
	}
	for _, name := range p.Order {
		for _, c := range p.Members[name] {
			if _, seen := out[c]; !seen {
				out[c] = name
			}
		}
	}
	return out
}

func TestGroupOnlyPlacesSymbolsThatTradedOnTheSession(t *testing.T) {
	p := &SectorPanel{Order: []string{"AI"}, Members: map[string][]string{"AI": {"2330", "3037"}}}
	stocks := []fetcher.StockData{{Symbol: "2330", Candles: bars("2026-09-10")}}
	_, grouped := p.Group(stocks)
	if n := len(grouped["AI"]); n != 1 {
		t.Fatalf("AI holds %d members, want 1 — a symbol with no bar on the session must be "+
			"absent, exactly as production sees it when its fetch returns nothing", n)
	}
	if codes := p.Codes(); !codes["2330"] || !codes["3037"] || len(codes) != 2 {
		t.Fatalf("Codes() = %v, want the two listed members", codes)
	}
}

// ── The two arms ──────────────────────────────────────────────────────────────────────────

func TestRegimeBlindPassesUnavailableAndNotANeutralRegime(t *testing.T) {
	arch, err := OpenReplayArchive(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	m, prov, err := arch.Market(ArmRegimeBlind, "2026-09-10")
	if err != nil {
		t.Fatalf("blind arm errored: %v", err)
	}
	if m.Available {
		t.Fatal("the regime-blind arm reported a market as Available")
	}
	if m.Regime != "" {
		t.Fatalf("the regime-blind arm carried regime %q — production encodes 'no snapshot "+
			"covered this session' as Available=false with no regime", m.Regime)
	}
	if prov != entryplanbacktest.RegimeUnavailable {
		t.Fatalf("provenance %q, want %q", prov, entryplanbacktest.RegimeUnavailable)
	}
}

// TestTheReplayArmNeverSilentlyDowngradesToBlind is the guard on decision 1's "never mixed".
// A missing replay file must be an error the caller has to handle, not an Available=false
// that would put a regime-blind row inside a REPLAYED_PIT table.
func TestTheReplayArmNeverSilentlyDowngradesToBlind(t *testing.T) {
	arch, err := OpenReplayArchive(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if arch.Covers("2026-09-10") {
		t.Fatal("an empty archive claims to cover a session")
	}
	_, prov, err := arch.Market(ArmReplayedPIT, "2026-09-10")
	if err == nil {
		t.Fatal("a missing replay file produced a market instead of an error — this is " +
			"exactly the silent downgrade that would mix the two arms")
	}
	if prov != entryplanbacktest.RegimeUnavailable {
		t.Fatalf("provenance %q, want %q", prov, entryplanbacktest.RegimeUnavailable)
	}
	if _, _, err := arch.Market("SOMETHING_ELSE", "2026-09-10"); err == nil {
		t.Fatal("an undefined arm was accepted")
	}
}

func TestReplayCaveatsCarryBothMeasuredBiases(t *testing.T) {
	c := ReplayCaveats()
	if len(c) != 2 {
		t.Fatalf("%d caveats, want 2 — posture and universe are independent and each alone "+
			"is enough to make a mixed series wrong", len(c))
	}
	joined := strings.Join(c, "\n")
	for _, want := range []string{"posture", "R4", "R5", "survivorship", "3.91", "8.21"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the caveats never mention %q:\n%s", want, joined)
		}
	}
}

// ── The funnel ────────────────────────────────────────────────────────────────────────────

func TestFunnelReconcileCatchesEachBrokenLine(t *testing.T) {
	good := func() *Funnel {
		f := NewFunnel(ArmReplayedPIT, FullFidelity)
		f.Candidates, f.ScannerObservations, f.PlansGenerated = 100, 90, 90
		f.SemanticResolved, f.SemanticUnresolved = 10, 80
		f.ByStatus[entryplan.StatusInsufficientData] = 80
		f.ByStatus[entryplan.StatusWaitPullback] = 10
		f.WithZone, f.WithMaxChase = 10, 6
		f.WithInvalidation, f.WithTarget1, f.WithTarget2 = 10, 9, 9
		f.WithSuitableValuationCeiling = 0
		return f
	}
	if err := good().Reconcile(); err != nil {
		t.Fatalf("a consistent funnel was refused: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Funnel)
		want string
	}{
		{"more observations than candidates", func(f *Funnel) { f.Candidates = 10 }, "candidates"},
		{"more plans than observations", func(f *Funnel) { f.PlansGenerated = 95 }, "scanner observations"},
		{"statuses do not partition", func(f *Funnel) { f.ByStatus[entryplan.StatusBuyNow] = 1 }, "partition of the plans"},
		{"semantic split wrong", func(f *Funnel) { f.SemanticResolved = 11 }, "unresolved"},
		{"chase without a zone", func(f *Funnel) { f.WithMaxChase = 11 }, "ceiling needs a zone"},
		{"target without a stop", func(f *Funnel) { f.WithTarget1 = 11 }, "risk denominator"},
		{"ceiling without a Target2", func(f *Funnel) { f.WithSuitableValuationCeiling = 10 }, "valuation ceilings"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := good()
			c.mut(f)
			err := f.Reconcile()
			if err == nil {
				t.Fatalf("accepted %+v", f)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestTheFunnelCountsPlansAndNotEntries(t *testing.T) {
	f := NewFunnel(ArmRegimeBlind, FullFidelity)
	f.Add(SessionResult{Universe: 3, Entries: []scanner.WatchlistEntry{
		{}, {}, {}, // three entries, none with a plan attached
	}})
	if f.ScannerObservations != 3 {
		t.Fatalf("scanner observations %d, want 3", f.ScannerObservations)
	}
	if f.PlansGenerated != 0 {
		t.Fatalf("plans generated %d, want 0 — a nil EntryPlan must show up as a GAP between "+
			"observations and plans, not be counted as a plan", f.PlansGenerated)
	}
	if err := f.Reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func TestPlanSemanticReadsTheTraceAndFallsBackToEvidence(t *testing.T) {
	p := &entryplan.Plan{EntryTrace: &entryplan.EntryTrace{}}
	p.EntryTrace.Decision.Semantic = entryplan.EntrySemanticPullback
	if got := PlanSemantic(p); got != entryplan.EntrySemanticPullback {
		t.Fatalf("decision trace semantic read as %q", got)
	}
	p2 := &entryplan.Plan{EntryTrace: &entryplan.EntryTrace{}}
	p2.EntryTrace.Zone.Semantic = entryplan.EntrySemanticBreakout
	if got := PlanSemantic(p2); got != entryplan.EntrySemanticBreakout {
		t.Fatalf("zone trace semantic read as %q", got)
	}
	// An S1 plan runs no entry step at all, so neither trace carries a semantic and the
	// evidence row is the only source left.
	p3 := &entryplan.Plan{EntryTrace: &entryplan.EntryTrace{},
		Evidence: []entryplan.Evidence{{Key: entryplan.EvidenceEntrySemantic,
			Text: string(entryplan.EntrySemanticPullback)}}}
	if got := PlanSemantic(p3); got != entryplan.EntrySemanticPullback {
		t.Fatalf("evidence fallback read as %q", got)
	}
	if got := PlanSemantic(&entryplan.Plan{}); got != "" {
		t.Fatalf("a plan with no trace and no evidence reported semantic %q", got)
	}
}

// TestTheCacheDeduplicatesSymbolsDeterministically pins the fix for the defect EP-9's own §24
// integrity pass found on its first full-scale run: `.cache` holds both 5236_TW.json and
// 5236_TWO.json — one listing before and after a transfer between the exchanges — and without
// de-duplication that stock entered every session TWICE and was double-weighted in every
// metric (324 DUPLICATE_IDENTITY defects over a 60-session slice).
func TestTheCacheDeduplicatesSymbolsDeterministically(t *testing.T) {
	dir := t.TempDir()
	// Same symbol, two files. The TWO file is the older listing; the TW file runs later and
	// must win.
	writeCacheFile(t, dir, "5236_TWO.json", "5236", "2024-07-15", 40)
	writeCacheFile(t, dir, "5236_TW.json", "5236", "2024-09-16", 40)
	writeCacheFile(t, dir, "2330_TW.json", "2330", "2024-09-16", 40)

	c, err := LoadCache(dir, 10)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(c.Symbols) != 2 {
		t.Fatalf("loaded %d symbols, want 2 — a duplicated symbol double-weights that stock "+
			"in every metric", len(c.Symbols))
	}
	if got := c.DroppedDuplicates["5236"]; got != "5236_TWO.json" {
		t.Fatalf("dropped %q, want 5236_TWO.json — the winner is the series whose LAST bar is "+
			"newer, and the loser must be RECORDED rather than silently discarded", got)
	}
	// And the truncation now yields one row per symbol on a shared session.
	if n := len(c.TruncateAt(c.Axis[len(c.Axis)-1], 1)); n > 2 {
		t.Fatalf("a session produced %d rows for 2 symbols", n)
	}

	// DETERMINISM: the same directory must resolve the same way on a second load. A
	// non-deterministic winner would make the whole study irreproducible for that symbol.
	c2, err := LoadCache(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if c2.DroppedDuplicates["5236"] != c.DroppedDuplicates["5236"] {
		t.Fatalf("two loads of the same directory dropped different files: %q vs %q",
			c.DroppedDuplicates["5236"], c2.DroppedDuplicates["5236"])
	}
	if c.Symbol("5236") == nil || c.Symbol("2330") == nil {
		t.Fatal("a de-duplicated symbol is no longer reachable by code")
	}
}

// writeCacheFile writes a minimal .cache record: n consecutive daily bars from `start`.
func writeCacheFile(t *testing.T, dir, name, symbol, start string, n int) {
	t.Helper()
	d, err := time.Parse(DateLayout, start)
	if err != nil {
		t.Fatal(err)
	}
	var sd fetcher.StockData
	sd.Symbol = symbol
	for i := 0; i < n; i++ {
		p := 100.0 + float64(i)
		sd.Candles = append(sd.Candles, fetcher.Candle{Date: d.AddDate(0, 0, i),
			Open: p, High: p, Low: p, Close: p, AdjClose: p, Volume: 1000})
	}
	b, err := json.Marshal(map[string]any{"data": sd})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
