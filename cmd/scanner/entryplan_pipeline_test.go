package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/research"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-6B — PRODUCTION WIRING PROOF + OFF-vs-ON SHADOW ISOLATION PROOF
//
// Two questions, and both are answered by RUNNING THE PIPELINE rather than by reading it:
//
//	A. Does the production path actually attach an entry plan? Deleting the attach call from
//	   buildWatchlist must turn this file red.
//	B. With the same candles in, does switching the flag change anything except the EntryPlan
//	   field? The comparison is over the COMPLETE result the production pipeline returns —
//	   after enrichment, after every shadow post-pass and after the sort — because the
//	   contamination worth catching is an INDIRECT one. Snapshotting before the attach and
//	   announcing afterwards that the snapshot had not changed would prove nothing at all.
//
// WHAT THESE TESTS DO NOT COVER, so nobody has to infer it. They drive buildWatchlist, which
// is the production seam main() calls; they do NOT execute main(). Removing the buildWatchlist
// call from main() is guarded one level up by an AST assertion
// (TestMainCallsTheProductionWatchlistSeam), which is structural and strictly weaker. See the
// doc comment on buildWatchlist in main.go.
// ──────────────────────────────────────────────────────────────────────────────

// pipelineAnalysisDate is fixed. A wall-clock date would make every assertion below a
// statement about the day the test ran.
var pipelineAnalysisDate = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

// pipelineConfig is the fixture config. It turns on every shadow layer that is PURE — no
// network, no filesystem, no clock — so the OFF-vs-ON comparison runs over a result with as
// many populated fields as can be produced offline, and the equality it asserts is therefore
// about a full page rather than about a skeleton.
//
// Deliberately OFF: enable_etf_flow, enable_institution and enable_news read snapshot
// directories, and enable_ai reaches the network. Including them would make the fixture's
// determinism depend on the machine it runs on, which is the opposite of what §15 asks for.
//
// This is a TEST FIXTURE and not a recommended configuration — in particular
// enable_signal_guardrail_scoring is on here because it widens the compared surface, which
// says nothing about whether it should be on in configs/config.yaml.
func pipelineConfig(enableEntryPlan bool) scanner.Config {
	return scanner.Config{
		TopN:                         50,
		EnableSignalGuardrailScoring: true,
		EnableRSRank:                 true,
		EnableNewHigh:                true,
		EnableVCP:                    true,
		EnableMomentumFlow:           true,
		EnableMultiTimeframe:         true,
		MTFRiskWarningEnabled:        true,
		MTFSortTieBreakerEnabled:     true,
		EnableHoldingHorizon:         true,
		EnableHorizonHint:            true,
		EnableBias:                   true,
		EnableCandlestick:            true,
		EnableTrendExtension:         true,
		EnableTechnicalIndicators:    true,

		EnableEntryPlan: enableEntryPlan,
	}
}

// pipelineCandles builds a deterministic OHLCV series. Every value is a function of the index
// alone: no clock, no randomness, no rounding that depends on anything outside this function.
func pipelineCandles(n int, start, step float64, vol int64) []fetcher.Candle {
	base := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	out := make([]fetcher.Candle, n)
	prev := start
	for i := 0; i < n; i++ {
		// A small index-driven wiggle so the series is not a straight line — pattern,
		// pivot and volatility layers need shape to have anything to say.
		c := start + step*float64(i) + 0.2*float64((i*7)%5)
		out[i] = fetcher.Candle{
			Date:     base.AddDate(0, 0, i),
			Open:     prev,
			High:     c + 0.6,
			Low:      c - 0.6,
			Close:    c,
			AdjClose: c,
			Volume:   vol + int64(i%3)*10_000,
		}
		prev = c
	}
	return out
}

// pipelineExtendedCandles is the EP-6C addition: a series the scanner reads as OVERHEATED
// WITH THE TREND INTACT, which rocket.go's watchActionFor turns into PULLBACK_BUY ("simply
// extended in a healthy trend → wait for the pullback to enter").
//
// It exists because NONE of the six original fixture stocks reaches a BUY action — they come
// back WATCH_CLOSELY / PREPARE_ENTRY / WAIT — so every plan built on them is
// INSUFFICIENT_DATA for want of an entry SHAPE, and a test that wanted to see a priced plan
// come out of the production seam had nothing to look at.
//
// The shape: a steady 0.4%/session climb for most of the window, then five sessions of +5%,
// which puts ret5 above the extension threshold while the moving averages stay bull-aligned
// and the close stays above MA20. Volume is constant, so no climax / limit-distribution
// pattern fires and the OVERHEATED reading stays an ENTRY-TIMING call rather than an exit.
// Values are rounded to two decimals so the fixture is readable and exactly reproducible.
func pipelineExtendedCandles() []fetcher.Candle {
	base := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	out := make([]fetcher.Candle, 90)
	c, prev := 100.0, 100.0
	for i := 0; i < 90; i++ {
		if i > 0 {
			g := 1.004
			if i > 84 {
				g = 1.05
			}
			c *= g
		}
		px := math.Round(c*100) / 100
		out[i] = fetcher.Candle{
			Date:     base.AddDate(0, 0, i),
			Open:     prev,
			High:     math.Round((px+0.6)*100) / 100,
			Low:      math.Round((px-0.6)*100) / 100,
			Close:    px,
			AdjClose: px,
			Volume:   2_000_000,
		}
		prev = px
	}
	return out
}

// pipelineWatchlistStocks is the §9 MULTI-STOCK fixture.
//
// It contains two DELIBERATE TIE PAIRS — (2330, 2454) and (1101, 1102) share a candle series
// exactly, so each pair gets the same Score, the same RocketScore and the same Action, and
// their relative order is decided purely by the stable sort. That is the condition under
// which a new tie-break would show itself: if the entry plan ever became one, these are the
// entries that would move.
//
// 6505 is EP-6C's addition (see pipelineExtendedCandles) and is deliberately NOT part of
// either tie pair.
func pipelineWatchlistStocks() []fetcher.StockData {
	up := pipelineCandles(90, 100, 0.8, 3_000_000)
	flat := pipelineCandles(90, 40, 0, 900_000)
	return []fetcher.StockData{
		{Symbol: "6505", Name: "台塑化", Market: fetcher.MarketTWSE, Source: "watchlist",
			Candles: pipelineExtendedCandles()},
		{Symbol: "2330", Name: "台積電", Market: fetcher.MarketTWSE, Source: "watchlist", Candles: up},
		{Symbol: "2454", Name: "聯發科", Market: fetcher.MarketTWSE, Source: "watchlist", Candles: up},
		{Symbol: "1101", Name: "台泥", Market: fetcher.MarketTWSE, Source: "watchlist", Candles: flat},
		{Symbol: "1102", Name: "亞泥", Market: fetcher.MarketTWSE, Source: "watchlist", Candles: flat},
		{Symbol: "3008", Name: "大立光", Market: fetcher.MarketTWSE, Source: "watchlist",
			Candles: pipelineCandles(90, 300, -1.1, 400_000)},
		{Symbol: "2603", Name: "長榮", Market: fetcher.MarketTWSE, Source: "watchlist",
			Candles: pipelineCandles(90, 60, 0.15, 15_000_000)},
	}
}

// runProductionWatchlist runs THE PRODUCTION PIPELINE — buildWatchlist, the function main()
// calls — over the fixture, and returns the complete result.
//
// It goes through the seam rather than through scanner.EnrichWatchlist + scanner.
// AttachEntryPlan directly, and that is the entire point: calling the scanner package
// directly would prove the attach function works and NOTHING about whether the binary ever
// asks for it. The config is validated first, exactly as main() does at startup, so a fixture
// that the binary would have rejected cannot reach these assertions.
func runProductionWatchlist(t *testing.T, sc scanner.Config) []scanner.WatchlistEntry {
	t.Helper()
	return runProductionWatchlistIn(t, sc, research.MarketContext{})
}

// runProductionWatchlistIn is runProductionWatchlist with the market read the binary would
// have loaded for the session. EP-6C's entry-plan projection reads it, so it has to be
// variable for the wiring to be testable at all; every pre-EP-6C caller passes the empty
// context, which is the state of a scan that ran before cmd/market-fetch.
func runProductionWatchlistIn(t *testing.T, sc scanner.Config,
	mc research.MarketContext) []scanner.WatchlistEntry {

	t.Helper()
	if err := sc.Validate(); err != nil {
		t.Fatalf("fixture config would be rejected at startup: %v", err)
	}
	stocks := pipelineWatchlistStocks()
	got := buildWatchlist(
		scanner.New(sc), sc,
		stocks,        // watchlist candles
		stocks,        // "full market" universe, for the C6a RS table
		nil, nil, nil, // no sector list / rotation / grouping
		pipelineAnalysisDate,
		mc,
	)
	if len(got) != len(stocks) {
		t.Fatalf("fixture produced %d entries from %d stocks — the fixture is being "+
			"filtered out (EnrichWatchlist needs >= 30 candles) and every assertion built "+
			"on it would be vacuous", len(got), len(stocks))
	}
	return got
}

// ── PROOF A: the production pipeline attaches the plan ────────────────────────

// §3 / mutation M1. Deleting `attachEntryPlan(out, sc)` from buildWatchlist makes THIS test
// fail, on results rather than on source text — which is what the repo rule asks for and what
// an AST scan, a grep, an unused-import error or a direct test of scanner.AttachEntryPlan
// cannot give.
func TestProductionPipelineAttachesTheEntryPlan(t *testing.T) {
	got := runProductionWatchlist(t, pipelineConfig(true))

	for i := range got {
		if got[i].EntryPlan == nil {
			t.Fatalf("PRODUCTION WIRING CONTRACT BROKEN: enable_entry_plan is true, the "+
				"production watchlist pipeline (buildWatchlist → attachEntryPlan → "+
				"scanner.AttachEntryPlan) ran to completion, and %s came back with a NIL "+
				"EntryPlan.\n"+
				"This is not a statement about the plan's contents and not a test of the "+
				"entryplan domain — ComputePlan is total and always returns a plan. It says "+
				"the post-pass is no longer wired into the pipeline the binary runs, so the "+
				"feature is off for every user no matter what their config says.",
				got[i].A.Symbol)
		}
		if got[i].EntryPlan.Symbol != got[i].A.Symbol {
			t.Errorf("entry %d is %s but carries a plan about %q — the plans have been "+
				"crossed between entries", i, got[i].A.Symbol, got[i].EntryPlan.Symbol)
		}
	}
}

// §5. What the plan SAYS is not this work item's subject. EP-6C is what projects the entry
// semantic, the regime, MA60, the previous close, the adjustment age and the valuation
// ceiling; until it lands, essentially every plan is INSUFFICIENT_DATA carrying no executable
// price, and that is the CORRECT output of a half-built projection.
//
// So this test asserts the plan is a well-formed, deterministic artefact of the pipeline —
// it is about the right symbol, it has a status, and it is the same on every run — and
// asserts nothing about it being actionable. A test that demanded an entry price today would
// be demanding that the bridge invent evidence the scan does not produce.
func TestProductionPipelinePlansAreWellFormedButNotYetActionable(t *testing.T) {
	got := runProductionWatchlist(t, pipelineConfig(true))
	for i := range got {
		p := got[i].EntryPlan
		if p == nil {
			t.Fatalf("%s: no plan — see TestProductionPipelineAttachesTheEntryPlan",
				got[i].A.Symbol)
		}
		if p.Status == "" {
			t.Errorf("%s: plan carries an empty status; every plan must say what it is",
				got[i].A.Symbol)
		}
		if p.AsOf != got[i].A.Date.Format("2006-01-02") {
			t.Errorf("%s: plan is dated %q but the analysis is dated %q — the plan is not "+
				"describing the session the evidence came from",
				got[i].A.Symbol, p.AsOf, got[i].A.Date.Format("2006-01-02"))
		}
	}
}

// ── PROOF A, the level above the seam: STRUCTURAL ONLY ────────────────────────

// TestMainCallsTheProductionWatchlistSeam is the WEAKEST guard in this file, and it is here
// because nothing stronger is available today.
//
// WHAT IT IS: an AST assertion that main() contains a call to buildWatchlist.
//
// WHAT IT IS NOT: a behavioural proof. It reads source text. It would still pass if the call
// were moved behind a condition that never fires, if its result were discarded, or if the
// pipeline it feeds were removed. It says the call EXISTS, not that it RUNS or that anything
// downstream uses what it returns.
//
// WHY NOT SOMETHING BETTER: covering this level means executing main(), and main() parses
// flags, loads a config file, fetches over the network, writes reports to disk and calls
// log.Fatalf. Making it testable is a refactor of the whole binary, which is not EP-6B's
// scope. The honest summary of the two layers is: the wiring INSIDE the seam is proven by
// behaviour, and the seam's own presence in main() is not — it is merely watched.
func TestMainCallsTheProductionWatchlistSeam(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var mainFn *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" {
			mainFn = fn
		}
	}
	if mainFn == nil {
		t.Fatal("no func main() in main.go — this scan cannot see the binary's entry point " +
			"any more and would pass for the wrong reason")
	}

	var called bool
	ast.Inspect(mainFn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "buildWatchlist" {
			called = true
		}
		return true
	})
	if !called {
		t.Fatal("main() no longer calls buildWatchlist. Every shadow post-pass — ETF flow, " +
			"institution, AI and the EP-6 entry plan — lives inside that function, so the " +
			"binary now runs none of them, while the behavioural tests in this file keep " +
			"passing because they call the seam directly.\n" +
			"This check is STRUCTURAL and deliberately weak: it is the only thing standing " +
			"at this level, because proving it by behaviour means executing main().")
	}
}

// ── PROOF B: OFF vs ON ────────────────────────────────────────────────────────

// §4. OFF means the field is ABSENT, not present-and-disabled.
//
// nil is the encoding of "the operator did not switch this on". A plan carrying
// Status=DISABLED would be a statement about the stock, and there is no such statement to
// make — feature absence is not a domain status, and the first consumer to forget that arm
// would render "no entry is valid here" for a feature nobody enabled.
//
// WHAT THIS TEST DOES NOT PROVE, stated rather than implied: it shows nothing is PUBLISHED,
// not that nothing is COMPUTED. Zero computation on the OFF path comes from the early return
// in scanner.AttachEntryPlan, which TestOffPathReturnsBeforeAnyComputation watches
// structurally. No counter or hook was added to the production API to prove it here — a
// test-only observation point inside a shipped function is a worse trade than saying plainly
// which of the two claims is proven by behaviour.
func TestProductionPipelinePublishesNoPlanWhenDisabled(t *testing.T) {
	got := runProductionWatchlist(t, pipelineConfig(false))
	for i := range got {
		if got[i].EntryPlan != nil {
			t.Errorf("%s: enable_entry_plan is false and the pipeline still published a "+
				"plan (%+v). OFF must mean the field stays nil",
				got[i].A.Symbol, got[i].EntryPlan)
		}
	}

	// show_entry_plan is a DISPLAY flag. Config.Validate rejects show-without-enable at
	// startup; this is the second half of the same rule — even if that check were removed,
	// the display flag alone must not switch the computation on.
	showOnly := pipelineConfig(false)
	showOnly.ShowEntryPlan = true
	showOnly.EnableEntryPlan = true // Validate requires the pair; then take enable away
	showOnly.EnableEntryPlan = false
	s := scanner.New(showOnly)
	stocks := pipelineWatchlistStocks()
	out := buildWatchlist(s, showOnly, stocks, stocks, nil, nil, nil,
		pipelineAnalysisDate, research.MarketContext{})
	for i := range out {
		if out[i].EntryPlan != nil {
			t.Errorf("%s: show_entry_plan alone computed a plan", out[i].A.Symbol)
		}
	}
}

// §4, second half / mutation M7: the OFF path must return BEFORE doing the work, not compute
// a plan and drop it.
//
// This is an AST check and therefore WEAKER than everything around it: it asserts the shape
// of scanner.AttachEntryPlan's first statement, not what the function does. It cannot see a
// computation moved into a helper called from the guard, and it would pass for a body that
// returned early and then did the work in a defer. It is here because the alternative —
// exporting a call counter from a production package so a test can watch it — puts test
// scaffolding into the shipped API, and the claim being guarded (a wasted computation whose
// result is discarded) is a performance claim, not a correctness one.
func TestOffPathReturnsBeforeAnyComputation(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../../internal/scanner/entryplan_attach.go", nil, 0)
	if err != nil {
		t.Fatalf("parse entryplan_attach.go: %v — repoint this check at wherever the bridge "+
			"moved, rather than dropping it", err)
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if d, ok := d.(*ast.FuncDecl); ok && d.Recv == nil && d.Name.Name == "AttachEntryPlan" {
			fn = d
		}
	}
	if fn == nil || fn.Body == nil || len(fn.Body.List) == 0 {
		t.Fatal("AttachEntryPlan not found in internal/scanner/entryplan_attach.go — this " +
			"check can no longer see the function it exists to watch")
	}
	guard, ok := fn.Body.List[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("the first statement of AttachEntryPlan is %T, not an `if`. The flag guard "+
			"must come first, so a disabled feature costs nothing", fn.Body.List[0])
	}
	if len(guard.Body.List) != 1 {
		t.Fatalf("the flag guard's body has %d statements; it must do nothing but return",
			len(guard.Body.List))
	}
	if _, ok := guard.Body.List[0].(*ast.ReturnStmt); !ok {
		t.Fatalf("the flag guard's body is %T, not a return — the OFF path is doing work",
			guard.Body.List[0])
	}
}

// §6-§8, §14: THE ISOLATION PROOF.
//
// Both sides run the COMPLETE production pipeline over identical candles; the only difference
// in the inputs is enable_entry_plan. The comparison happens on the finished results, so an
// indirect effect — a plan that nudged a score through some shared state, a post-pass that
// reordered the slice — is inside the comparison rather than outside it.
//
// The structural diff is the correctness source. It covers Decision (A.Action), Score,
// RocketScore, WatchAction, ExplosionProb, the ranking fields, the sort keys, Reasons, the
// consolidation/backtest reads and every pre-existing shadow signal, and it covers them
// because they are FIELDS, not because anyone listed them.
func TestEntryPlanIsTheOnlyDifferenceBetweenOffAndOn(t *testing.T) {
	off := runProductionWatchlist(t, pipelineConfig(false))
	on := runProductionWatchlist(t, pipelineConfig(true))

	// Runs FIRST and only to improve the failure message: the four fields below are the
	// ones a trader would recognise, and naming them beats a reflect path. It is NOT the
	// correctness source — if it passes and the structural diff fails, the structural diff
	// is right — and it must never grow into a hand-maintained list of everything that
	// matters, which is exactly the blind-spot machine this file avoids.
	for i := range off {
		o, n := off[i], on[i]
		switch {
		case o.A.Symbol != n.A.Symbol:
			t.Errorf("position %d holds %s with the plan off and %s with it on",
				i, o.A.Symbol, n.A.Symbol)
		case o.A.Action != n.A.Action:
			t.Errorf("%s: Action moved %s → %s when the entry plan was switched on",
				o.A.Symbol, o.A.Action, n.A.Action)
		case o.A.Score != n.A.Score:
			t.Errorf("%s: Score moved %v → %v when the entry plan was switched on",
				o.A.Symbol, o.A.Score, n.A.Score)
		case o.RocketScore != n.RocketScore:
			t.Errorf("%s: RocketScore moved %d → %d when the entry plan was switched on",
				o.A.Symbol, o.RocketScore, n.RocketScore)
		case o.WatchAction != n.WatchAction:
			t.Errorf("%s: WatchAction moved %s → %s when the entry plan was switched on",
				o.A.Symbol, o.WatchAction, n.WatchAction)
		}
	}

	assertEntryPlanShadowOnlyDifference(t, off, on)

	// ANTI-VACUITY. The assertion above is satisfied by two identical results, so it would
	// also pass if the feature had quietly stopped working — the permitted difference must
	// actually be there.
	diffs, _, err := shadowDifferences(off, on)
	if err != nil {
		t.Fatalf("comparison incomplete: %v", err)
	}
	if len(diffs) != len(on) {
		t.Fatalf("expected exactly one difference per entry (the EntryPlan field appearing "+
			"on %d entries), got %d differences: %v. If this is zero, OFF and ON produced "+
			"identical results and the isolation proof above is vacuous", len(on), len(diffs), diffs)
	}
	for _, d := range diffs {
		if d.Path != "[i].EntryPlan" {
			t.Errorf("unexpected difference at %s", d)
		}
	}
}

// §9: ORDERING. The page order must be identical with the plan off and on.
//
// The fixture is built so that this can fail: (2330, 2454) and (1101, 1102) are score ties
// held apart only by the stable sort, and a tie is where a new comparator term shows up
// first. The tie is asserted to still exist, because a fixture whose ties evaporated would
// make this test pass while testing nothing.
func TestEntryPlanDoesNotChangeWatchlistOrder(t *testing.T) {
	off := runProductionWatchlist(t, pipelineConfig(false))
	on := runProductionWatchlist(t, pipelineConfig(true))

	var ties int
	for i := 1; i < len(off); i++ {
		if off[i].RocketScore == off[i-1].RocketScore &&
			off[i].A.Score == off[i-1].A.Score &&
			off[i].A.Action == off[i-1].A.Action {
			ties++
		}
	}
	if ties == 0 {
		t.Fatalf("the fixture produced no adjacent pair sharing RocketScore, Score and "+
			"Action, so nothing here could detect the entry plan becoming a tie-break. "+
			"Order was: %v", symbolsOf(off))
	}

	if got, want := symbolsOf(on), symbolsOf(off); !equalStrings(got, want) {
		t.Fatalf("switching enable_entry_plan on reordered the watchlist.\n"+
			"  off: %v\n  on:  %v\n"+
			"The plan is computed after the sort is final and is read back by nothing, so "+
			"any reordering means it has become an input to the ranking — a tie-break, a "+
			"score modifier or a comparator term.", got, want)
	}
}

// §15: DETERMINISM. The same fixture, the same config, twice — bit-identical, plans included.
// Nothing in this path may read time.Now, a map iteration order, a random source, the network
// or any "latest" state.
func TestProductionPipelineIsDeterministic(t *testing.T) {
	first := runProductionWatchlist(t, pipelineConfig(true))
	second := runProductionWatchlist(t, pipelineConfig(true))
	assertBitIdentical(t, first, second, "two ON runs of the same fixture")

	// The plans specifically, in a message that names the symbol — the assertion above
	// already covers them, this one just says so out loud when it breaks.
	for i := range first {
		if first[i].EntryPlan == nil || second[i].EntryPlan == nil {
			t.Fatalf("entry %d lost its plan between runs", i)
		}
		if first[i].EntryPlan.Status != second[i].EntryPlan.Status {
			t.Errorf("%s: plan status differs between two identical runs: %s vs %s",
				first[i].A.Symbol, first[i].EntryPlan.Status, second[i].EntryPlan.Status)
		}
	}

	// The OFF path too: determinism is not a property of the ON branch alone.
	assertBitIdentical(t, runProductionWatchlist(t, pipelineConfig(false)),
		runProductionWatchlist(t, pipelineConfig(false)), "two OFF runs of the same fixture")
}

func symbolsOf(entries []scanner.WatchlistEntry) []string {
	out := make([]string, len(entries))
	for i := range entries {
		out[i] = entries[i].A.Symbol
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── EP-6C: the two inputs the projection gained must arrive THROUGH THE PIPELINE ──

// evidenceRow returns a plan's evidence row for a key, and whether it exists.
func evidenceRow(p *entryplan.Plan, key string) (entryplan.Evidence, bool) {
	if p == nil {
		return entryplan.Evidence{}, false
	}
	for _, ev := range p.Evidence {
		if ev.Key == key {
			return ev, true
		}
	}
	return entryplan.Evidence{}, false
}

// The MARKET REGIME main() loaded reaches the plan, through buildWatchlist.
//
// BEHAVIOURAL, not structural: it runs the production seam twice with two different market
// contexts and reads the resulting plans. Dropping the marketCtx argument from the
// attachEntryPlan call — or ignoring it inside the wrapper — turns this red, which an AST
// scan of the call site could not do (the argument would still be written there).
//
// It says nothing about whether main() loads the right snapshot; loadMarketContext is main's
// and is not executed here.
func TestProductionPipelineThreadsTheMarketRegimeIntoThePlan(t *testing.T) {
	withRegime := runProductionWatchlistIn(t, pipelineConfig(true),
		research.MarketContext{Regime: "BULL", AsOfDate: "2026-09-10"})

	var available int
	for i := range withRegime {
		row, ok := evidenceRow(withRegime[i].EntryPlan, entryplan.EvidenceMarketRegime)
		if !ok {
			t.Fatalf("%s: the plan carries no MARKET_REGIME evidence row", withRegime[i].A.Symbol)
		}
		if row.Status != entryplan.Available || row.Text != "BULL" {
			t.Errorf("%s: MARKET_REGIME row = %+v, want an AVAILABLE BULL — the market read "+
				"main() loaded is not reaching the entry plan", withRegime[i].A.Symbol, row)
			continue
		}
		available++
	}
	if available != len(withRegime) {
		t.Fatalf("%d of %d entries carried the regime", available, len(withRegime))
	}

	// The other half of the truth table: with no snapshot the regime must NOT appear. A
	// bridge that hard-coded a regime would pass the loop above and fail here.
	none := runProductionWatchlistIn(t, pipelineConfig(true), research.MarketContext{})
	for i := range none {
		row, ok := evidenceRow(none[i].EntryPlan, entryplan.EvidenceMarketRegime)
		if ok && row.Status == entryplan.Available {
			t.Errorf("%s: no market snapshot covered the session and the plan still reports "+
				"an AVAILABLE regime (%+v)", none[i].A.Symbol, row)
		}
	}
}

// The CANDLE SERIES the pipeline analysed reaches the plan, through buildWatchlist.
//
// The previous close is the visible consequence: it can only come from the series, and the
// value is compared against the FIXTURE's own second-to-last close rather than a literal, so
// the test cannot drift away from the data it is about.
func TestProductionPipelineThreadsTheCandleSeriesIntoThePlan(t *testing.T) {
	got := runProductionWatchlistIn(t, pipelineConfig(true),
		research.MarketContext{Regime: "BULL"})

	// symbol → the previous close the series says, built from the same fixture the pipeline
	// was handed.
	want := map[string]float64{}
	for _, s := range pipelineWatchlistStocks() {
		want[s.Symbol] = s.Candles[len(s.Candles)-2].Close
	}

	var checked int
	for i := range got {
		sym := got[i].A.Symbol
		row, ok := evidenceRow(got[i].EntryPlan, entryplan.EvidencePreviousClose)
		if !ok {
			t.Fatalf("%s: the plan carries no PREVIOUS_CLOSE evidence row", sym)
		}
		if row.Status != entryplan.Available || row.Value == nil {
			t.Errorf("%s: PREVIOUS_CLOSE row = %+v, want the series' own second-to-last "+
				"close (%v) — the candle series is not reaching the entry plan",
				sym, row, want[sym])
			continue
		}
		if *row.Value != want[sym] {
			t.Errorf("%s: previous close = %v, want %v", sym, *row.Value, want[sym])
			continue
		}
		checked++
	}
	// ANTI-VACUITY: every fixture stock must have been checked, so a loop that silently
	// stopped matching symbols cannot pass.
	if checked != len(got) {
		t.Fatalf("only %d of %d entries had a previous close to compare", checked, len(got))
	}
}

// EP-6C makes plans that CARRY PRICES, and the OFF-vs-ON isolation must still hold then.
//
// TestEntryPlanIsTheOnlyDifferenceBetweenOffAndOn runs with no market snapshot, where every
// plan is INSUFFICIENT_DATA with no prices — a weaker input than production will see. This
// re-runs the same structural diff in a BULL market, where the projection produces zones,
// stops and targets, and asserts the same thing: the only difference is [i].EntryPlan.
func TestEntryPlanRemainsShadowOnlyWhenPlansCarryPrices(t *testing.T) {
	mc := research.MarketContext{Regime: "BULL"}
	off := runProductionWatchlistIn(t, pipelineConfig(false), mc)
	on := runProductionWatchlistIn(t, pipelineConfig(true), mc)

	// ANTI-VACUITY, and it is the whole reason this test exists next to the other one: at
	// least one plan must actually publish an executable price, or this is just the
	// no-regime isolation proof run a second time.
	var priced int
	for i := range on {
		if on[i].EntryPlan != nil && on[i].EntryPlan.HasExecutablePrices() {
			priced++
		}
	}
	if priced == 0 {
		t.Fatalf("no plan in a BULL market carried an executable price, so this test is a "+
			"duplicate of the no-regime isolation proof. Statuses were: %v", statusesOf(on))
	}

	// The fixture's own claim, asserted rather than described: pipelineExtendedCandles is
	// supposed to read as OVERHEATED-with-the-trend-intact, which rocket.go turns into
	// PULLBACK_BUY. If the scanner's staging ever changes, this says so instead of leaving
	// the comment on that function quietly wrong.
	var found bool
	for i := range on {
		if on[i].A.Symbol != "6505" {
			continue
		}
		found = true
		if on[i].RocketStage != scanner.StageOverheated ||
			on[i].WatchAction != scanner.ActPullbackBuy {
			t.Errorf("6505 was read as stage=%s action=%s, but pipelineExtendedCandles "+
				"claims it is an overheated-but-intact PULLBACK_BUY — the fixture no longer "+
				"supplies the entry semantic this test is built on",
				on[i].RocketStage, on[i].WatchAction)
		}
	}
	if !found {
		t.Fatal("6505 is missing from the fixture result")
	}

	assertEntryPlanShadowOnlyDifference(t, off, on)

	diffs, _, err := shadowDifferences(off, on)
	if err != nil {
		t.Fatalf("comparison incomplete: %v", err)
	}
	if len(diffs) != len(on) {
		t.Fatalf("expected exactly one difference per entry (the EntryPlan field on %d "+
			"entries), got %d: %v", len(on), len(diffs), diffs)
	}
	for _, d := range diffs {
		if d.Path != "[i].EntryPlan" {
			t.Errorf("unexpected difference at %s", d)
		}
	}
}

// statusesOf is a failure-message helper: symbol → plan status.
func statusesOf(entries []scanner.WatchlistEntry) map[string]entryplan.EntryStatus {
	out := map[string]entryplan.EntryStatus{}
	for i := range entries {
		if entries[i].EntryPlan != nil {
			out[entries[i].A.Symbol] = entries[i].EntryPlan.Status
		}
	}
	return out
}
