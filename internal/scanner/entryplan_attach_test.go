package scanner

import (
	"math"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// epTestDate is the session every fixture below claims to describe.
var epTestDate = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

// epEntry builds a watchlist entry carrying the evidence EP-6A actually projects.
func epEntry(symbol string) WatchlistEntry {
	return WatchlistEntry{
		A: StockAnalysis{
			Symbol:      symbol,
			Name:        "測試",
			Date:        epTestDate,
			Close:       100,
			ATR:         2.5,
			MA20:        96,
			AvgVolume20: 12_000,
			Score:       71,
			Action:      ActionBuy,
		},
		Consol: Consolidation{
			Bucket:    SwingBase,
			Days:      14,
			BaseLow:   92,
			PivotHigh: 104,
		},
		RocketScore: 63,
		WatchAction: ActBreakoutBuy,
	}
}

// epNoMarket is "no dashboard snapshot covered this session" — the state every scan is in
// until cmd/market-fetch has run for the date. Written out rather than passed as a literal so
// the call sites below say WHICH market they are asserting against.
var epNoMarket = EntryPlanMarket{}

// epNoSeries is "the caller offered no candles", which projects the previous close and the
// adjustment age as absent. The tests that are about those two pass a real series.
var epNoSeries map[string][]fetcher.Candle

// evidenceOf returns the plan's row for a key, and whether there was one.
func evidenceOf(p *entryplan.Plan, key string) (entryplan.Evidence, bool) {
	for _, ev := range p.Evidence {
		if ev.Key == key {
			return ev, true
		}
	}
	return entryplan.Evidence{}, false
}

// OFF means the field is NIL — feature absence, not a plan carrying a "disabled" verdict.
//
// This is the whole EP-6A gate, and it is asserted on entries whose evidence is complete
// enough that a plan would certainly have been produced had the flag been read the other way:
// a test whose fixtures could not have produced a plan anyway would pass for the wrong reason.
func TestAttachEntryPlanDisabledLeavesEveryFieldNil(t *testing.T) {
	entries := []WatchlistEntry{epEntry("2330"), epEntry("2454"), epEntry("1101")}

	AttachEntryPlan(entries, epNoSeries, epNoMarket, false)

	for i := range entries {
		if entries[i].EntryPlan != nil {
			t.Errorf("%s: enable_entry_plan=false must leave EntryPlan nil, got %+v",
				entries[i].A.Symbol, entries[i].EntryPlan)
		}
	}
	// And the same entries DO get a plan when the flag is on, so the assertion above is
	// about the flag rather than about the fixture.
	AttachEntryPlan(entries, epNoSeries, epNoMarket, true)
	for i := range entries {
		if entries[i].EntryPlan == nil {
			t.Fatalf("%s: enable_entry_plan=true produced no plan — the OFF assertion above "+
				"would then hold for a reason that has nothing to do with the flag",
				entries[i].A.Symbol)
		}
	}
}

// ON means EVERY entry gets a plan, including ones with no evidence at all. A stock that
// could not be planned must arrive as a plan that SAYS SO, never as a nil field — nil is
// already spoken for by "the feature is off".
func TestAttachEntryPlanEnabledAttachesToEveryEntry(t *testing.T) {
	entries := []WatchlistEntry{
		epEntry("2330"),
		{}, // no symbol, no date, no prices
		epEntry("2454"),
	}

	AttachEntryPlan(entries, epNoSeries, epNoMarket, true)

	for i := range entries {
		if entries[i].EntryPlan == nil {
			t.Fatalf("entry %d: enable_entry_plan=true must attach a plan to every entry", i)
		}
		if entries[i].EntryPlan.RuleVersion != entryplan.RuleVersion {
			t.Errorf("entry %d: rule version %q, want %q",
				i, entries[i].EntryPlan.RuleVersion, entryplan.RuleVersion)
		}
	}
	if got := entries[0].EntryPlan.Symbol; got != "2330" {
		t.Errorf("symbol %q, want 2330", got)
	}
	if got := entries[0].EntryPlan.AsOf; got != "2026-09-10" {
		t.Errorf("as_of %q, want 2026-09-10 — the plan describes the session of the last "+
			"candle, not the day the scan ran", got)
	}
	// The empty entry is not a stock, and the plan says that rather than diagnosing prices
	// for a symbol nobody named.
	if s := entries[1].EntryPlan.Status; s != entryplan.StatusInsufficientData {
		t.Errorf("an empty entry produced status %q, want INSUFFICIENT_DATA", s)
	}
	if entries[1].EntryPlan.HasExecutablePrices() {
		t.Error("an entry with no symbol, no date and no prices produced an executable price")
	}
}

// EP-6A published no price for ANY input because it projected neither regime nor semantic.
// EP-6C projects both, so this test's subject narrows to the half that is still true: WITH NO
// MARKET SNAPSHOT there is still no price, whatever the stock looks like.
//
// It is not a restatement of the old assertion. The entry semantic here IS available
// (ActBreakoutBuy → BREAKOUT) and every level the scan produced is present, so the ONLY thing
// standing between this fixture and an executable price is the missing regime — which is what
// makes this a test of the regime gate rather than of a half-built bridge.
// TestEntryPlanRegimeUnlocksTheSameFixture is the other half and must stay paired with it.
func TestAttachEntryPlanPublishesNoPriceWithoutARegime(t *testing.T) {
	entries := []WatchlistEntry{epEntry("2330")}
	AttachEntryPlan(entries, epNoSeries, epNoMarket, true)

	p := entries[0].EntryPlan
	// ANTI-VACUITY: the semantic must really be in hand, or this proves nothing about the
	// regime.
	if sem, ok := evidenceOf(p, entryplan.EvidenceEntrySemantic); !ok ||
		sem.Status != entryplan.Available || sem.Text != string(entryplan.EntrySemanticBreakout) {
		t.Fatalf("fixture no longer supplies a resolved entry semantic (%+v); the assertion "+
			"below would then hold because of the semantic, not because of the regime", sem)
	}
	if p.HasExecutablePrices() {
		t.Errorf("no market snapshot covered this session, so EP-2's policy table cannot be "+
			"consulted and no entry may be permitted — yet the plan carries prices: %+v", p)
	}
	if p.Status != entryplan.StatusInsufficientData {
		t.Errorf("status %q, want INSUFFICIENT_DATA when the regime was never handed over",
			p.Status)
	}
}

// The evidence the scan DOES have is carried, on the RAW basis it was computed on, and the
// evidence it does NOT have is recorded as absent rather than filled in.
func TestAttachEntryPlanCarriesRealEvidenceAndFabricatesNone(t *testing.T) {
	entries := []WatchlistEntry{epEntry("2330")}
	AttachEntryPlan(entries, epNoSeries, epNoMarket, true)
	p := entries[0].EntryPlan

	price, ok := evidenceOf(p, entryplan.EvidenceCurrentPrice)
	if !ok {
		t.Fatal("no CURRENT_PRICE evidence row")
	}
	if price.Status != entryplan.Available || price.Value == nil || *price.Value != 100 {
		t.Errorf("current price row = %+v, want the scan's own close of 100", price)
	}
	if price.PriceBasis != entryplan.PriceBasisRaw {
		t.Errorf("current price basis %q, want RAW — StockAnalysis.Close is latest.Close",
			price.PriceBasis)
	}

	atr, ok := evidenceOf(p, entryplan.EvidenceATR)
	if !ok {
		t.Fatal("no ATR evidence row")
	}
	if atr.Status != entryplan.Available || atr.Value == nil || *atr.Value != 2.5 {
		t.Errorf("ATR row = %+v, want the scan's own 2.5", atr)
	}

	// THE REGIME IS THE ONE INPUT THIS BRIDGE MAY NEVER PRODUCE BY ITSELF. This call passed
	// epNoMarket, so an AVAILABLE regime row could only have come from somewhere inside the
	// scanner — a sector stage, a rocket stage, a default of BULL — and every one of those
	// would key EP-2's policy table on an axis nobody published.
	regime, ok := evidenceOf(p, entryplan.EvidenceMarketRegime)
	if ok && regime.Status == entryplan.Available {
		t.Errorf("no market snapshot was handed over and the plan still reports an AVAILABLE "+
			"regime (%+v) — somebody substituted a market state", regime)
	}

	// The two fields EP-6C leaves unprojected must be ABSENT on the SNAPSHOT, not defaulted.
	// (They have no evidence row of their own in entryplan, so this reads the projection.)
	snap := entryPlanSnapshotOf(&entries[0], nil, epNoMarket)
	if snap.MA60.Status != entryplan.Unavailable || snap.MA60.Value != nil {
		t.Errorf("MA60 = %+v, want UNAVAILABLE with no value. Four sites in this package "+
			"compute SMA(closes, 60) already, but NO PRODUCER PUBLISHES THE LEVEL — "+
			"indicator.Result and StockAnalysis have no MA60 field — so a value here is one "+
			"the bridge computed itself", snap.MA60)
	}
	if snap.Valuation.Status != entryplan.Unavailable || snap.Valuation.BaseTargetPrice != nil {
		t.Errorf("Valuation = %+v, want UNAVAILABLE with no target: the archive is not read "+
			"until after buildWatchlist returns", snap.Valuation)
	}
}

// A stock whose indicators have not warmed up must produce ABSENCES, never zeros dressed as
// prices. 0 survives every downstream check — finite, on the tick grid, below the market —
// and is the single most dangerous value this projection could pass through.
func TestEntryPlanProjectionRefusesNonPrices(t *testing.T) {
	for _, v := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		obs := entryPlanPrice(v)
		if obs.Status == entryplan.Available || obs.Value != nil {
			t.Errorf("entryPlanPrice(%v) = %+v, want an unavailable observation", v, obs)
		}
		if obs.Usable() {
			t.Errorf("entryPlanPrice(%v) is usable as a price", v)
		}
		atr := entryPlanATR(v)
		if atr.Status == entryplan.Available || atr.Value != nil {
			t.Errorf("entryPlanATR(%v) = %+v, want unavailable", v, atr)
		}
	}
	// A real price is carried verbatim, with its basis and (for the ATR) its period.
	if obs := entryPlanPrice(87.25); !obs.Usable() || *obs.Value != 87.25 ||
		obs.PriceBasis != entryplan.PriceBasisRaw {
		t.Errorf("entryPlanPrice(87.25) = %+v", obs)
	}
	if atr := entryPlanATR(3.5); atr.Status != entryplan.Available || *atr.Value != 3.5 ||
		atr.Period != entryPlanATRPeriod || atr.PriceBasis != entryplan.PriceBasisRaw {
		t.Errorf("entryPlanATR(3.5) = %+v", atr)
	}
	if v := entryPlanAvgVolume(0); v != nil {
		t.Errorf("a zero average volume must be absent, got %v", *v)
	}
	if v := entryPlanAvgVolume(-5); v != nil {
		t.Errorf("a negative average volume must be absent, got %v", *v)
	}
	if v := entryPlanAvgVolume(1200); v == nil || *v != 1200 {
		t.Errorf("a real average volume must be carried, got %v", v)
	}
}

// An entry whose consolidation never ran must not acquire a support price. This is the
// projection's version of MISSING ≠ ZERO: Consolidation{} has BaseLow 0, and 0 is not a level.
func TestEntryPlanProjectionLeavesAnUnanalysedBaseAbsent(t *testing.T) {
	e := epEntry("2330")
	e.Consol = Consolidation{}
	snap := entryPlanSnapshotOf(&e, nil, epNoMarket)

	if snap.BaseLow.Usable() {
		t.Errorf("an unanalysed consolidation produced a base low: %+v", snap.BaseLow)
	}
	if snap.PivotHigh.Usable() {
		t.Errorf("an unanalysed consolidation produced a pivot high: %+v", snap.PivotHigh)
	}
	if snap.BaseLowKind != "" {
		t.Errorf("base low kind %q, want \"\" — the producer never said, which entryplan "+
			"keeps distinct from UNKNOWN (it looked and cannot say)", snap.BaseLowKind)
	}
}

// The base low answers two different questions in the same field, and only one of them may
// ever become a thesis-invalidation level. The mapping is read off consolidation.go's own
// branches: every named bucket comes from a 3+ session window, NO_BASE comes from latest.Low.
func TestEntryPlanBaseLowKindMatchesTheBucketThatProducedIt(t *testing.T) {
	structural := []ConsolBucket{MicroBase, ShortBase, SwingBase, MidBase, LongBase}
	for _, b := range structural {
		got := entryPlanBaseLowKind(b)
		if got != entryplan.BaseLowConsolidationBase {
			t.Errorf("bucket %q → %q, want CONSOLIDATION_BASE", b, got)
		}
		if !got.Structural() {
			t.Errorf("bucket %q produced a kind entryplan refuses as an invalidation", b)
		}
	}
	if got := entryPlanBaseLowKind(NoBase); got != entryplan.BaseLowLatestBar {
		t.Errorf("NO_BASE → %q, want LATEST_BAR_LOW", got)
	}
	if entryPlanBaseLowKind(NoBase).Structural() {
		t.Error("NO_BASE's base low is one session's worst tick and must never be structural")
	}
	if got := entryPlanBaseLowKind(""); got != "" {
		t.Errorf("an unset bucket → %q, want \"\"", got)
	}
	// A bucket nobody has classified yet must NOT default into the structural arm. This is
	// the direction that costs money: an unclassified level used as a stop.
	if got := entryPlanBaseLowKind(ConsolBucket("SOME_FUTURE_BASE")); got.Structural() {
		t.Errorf("an unrecognised bucket → %q, which entryplan would accept as an "+
			"invalidation level; it must be classified by whoever adds it", got)
	}
}

// The projection must not retain a pointer into the caller's data: a plan is a statement
// about a session, and a later edit to the entry must not rewrite an already-computed one.
func TestAttachEntryPlanCopiesItsInputs(t *testing.T) {
	entries := []WatchlistEntry{epEntry("2330")}
	AttachEntryPlan(entries, epNoSeries, epNoMarket, true)
	before, ok := evidenceOf(entries[0].EntryPlan, entryplan.EvidenceCurrentPrice)
	if !ok || before.Value == nil {
		t.Fatal("no current price evidence to compare")
	}
	captured := *before.Value

	entries[0].A.Close = 999

	after, _ := evidenceOf(entries[0].EntryPlan, entryplan.EvidenceCurrentPrice)
	if after.Value == nil || *after.Value != captured {
		t.Errorf("mutating the entry after the fact changed an already-computed plan: %v → %v",
			captured, after.Value)
	}
}

// Neither flag state may panic on an empty watchlist.
func TestAttachEntryPlanHandlesAnEmptyWatchlist(t *testing.T) {
	AttachEntryPlan(nil, epNoSeries, epNoMarket, true)
	AttachEntryPlan(nil, epNoSeries, epNoMarket, false)
	AttachEntryPlan([]WatchlistEntry{}, epNoSeries, epNoMarket, true)
}
