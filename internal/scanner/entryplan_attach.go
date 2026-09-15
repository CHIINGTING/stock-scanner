package scanner

import (
	"math"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-6 entry-plan attach — the scanner → entryplan BRIDGE, shadow-only, POST-PASS.
//
// This file is the ONLY place the two layers meet, and it is a PROJECTION: it copies
// evidence the scan already produced onto entryplan.Snapshot and copies the resulting Plan
// back onto the entry. It computes no indicator, derives no level and decides nothing.
//
// # The direction, and why it is enforced elsewhere
//
// entryplan answers "at what price" strictly AFTER the scanner has answered "is this worth
// owning at all" (internal/entryplan/doc.go). So this pass runs after the watchlist sort is
// final, writes exactly one field — WatchlistEntry.EntryPlan — and is read back by nothing.
// The field lives outside ShadowSignals so the C6b guardrail scoring cannot reach it even by
// accident, exactly as AI / TrendExt / Technical do.
//
// # THE ONE RULE OF THIS FILE: IT NEVER SUBSTITUTES
//
// There is no `atr = close * 0.025`, no `previousClose = close`, no `baseLow = ma20`, no
// `pivotHigh = close`. Each of those is a DIFFERENT RULE published through the field of the
// rule a reader believes produced it, and every one of them survives every downstream check:
// the number is finite, positive, ordered and on the tick grid, and it is not a level.
// entryplan.ComputePlan's own doc refuses them on the inside; this file is the outside, and
// it is the side where the temptation actually lives, because here a plausible number IS in
// scope. A value the scan did not produce is projected as UNAVAILABLE and the plan says so.
//
// # EP-6G SCOPE — five of EP-6A's six gaps are filled, one is still open
//
// EP-6A wired the bridge and proved the flag gates it, leaving six Snapshot fields honestly
// UNAVAILABLE. EP-6C projected four of them; EP-6G adds point-in-time BASE valuation evidence:
//
//	EntrySemantic   WatchAction, through the exhaustive mapping EP-2 wrote down in
//	                entryplan/policy.go. No new classifier — see entryPlanEntrySemantic.
//	Regime          the market dashboard's snapshot regime, handed in by the caller as
//	                EntryPlanMarket. This file does not compute a regime — see entryPlanRegime.
//	PreviousClose   candles[n-2].Close of the same series the analysis ran on.
//	AdjustmentAge   fetcher.LastAdjustmentAge(candles), carried verbatim.
//
// ONE REMAINS UNAVAILABLE, and it is not an oversight:
//
//	MA60        four sites in this package DO compute SMA(closes, 60) (horizonhint.go:187,
//	            holdinghorizon.go:122, trendext.go:353, rotation.go:371) — but none of them
//	            PUBLISHES the level: indicator.Result and StockAnalysis carry no MA60 field,
//	            and each site throws the price away. The bridge refuses to become a fifth
//	            in-place computation with a fifth private warm-up convention. See the field.
//	Valuation   cmd/scanner derives BASE target and suitability from the archived research
//	            views loaded for the exact entry session, then supplies them to this bridge.
//
// EP-6C DECIDES NOTHING NEW. It adds no threshold, no scoring, no second "should I buy"
// classifier: every value above is READ from a producer that already published it, and the
// rule that turns them into a price stays entirely inside internal/entryplan (EP-3 / EP-4 /
// EP-5, all frozen here). BUY is still not BUY_NOW — whether a plan becomes executable is
// decided by entryplan's decision table from the regime policy, the zone geometry and the
// chase ceiling, none of which this file can reach.
//
// With the entry semantic and the regime now in hand, SOME plans can publish a price where
// EP-6A's could publish none. Most still cannot, and that remains the honest reading rather
// than a defect to tune away: a projection with no MA60 has fewer pullback candidates, and a
// regime that was never snapshotted
// leaves the policy UNRESOLVED for every stock in the run.
// ──────────────────────────────────────────────────────────────────────────────

// entryPlanATRPeriod is the Wilder period behind StockAnalysis.ATR.
//
// MEASURED, not assumed: internal/indicator/calculator.go builds every Result with
// `ATR: ATR(highs, lows, closes, 14)`, and StockAnalysis.ATR is `ind.ATR[n-1]`. It is carried
// onto the evidence row because an ATR with no period is a number with no scale — the same
// reason entryplan.ATREvidence has the field at all.
const entryPlanATRPeriod = 14

// entryPlanAsOfLayout is the repo's session-date format, shared with entryplan.Snapshot.AsOf,
// the analysis-history archive and the valuation snapshots.
const entryPlanAsOfLayout = "2006-01-02"

// AttachEntryPlan projects each watchlist entry onto an entryplan.Snapshot, computes the
// plan, and attaches it in place.
//
// enabled=false short-circuits before any work and every entry keeps a NIL EntryPlan.
//
// Nil is the encoding of FEATURE ABSENCE and it is not an entryplan.Plan carrying some
// DISABLED status. The distinction is the same one EP-1 makes between MISSING and ZERO: a
// Plan is a statement about a stock, and "the operator did not switch this on" is not a
// statement about a stock. A DISABLED status would also have to be a value in EntryStatus,
// where it would become a sixth arm every consumer must switch on, and where the first
// consumer to forget it would render "no entry is valid here" for a feature nobody enabled.
//
// It cannot fail the scan: it returns nothing, ComputePlan is total (defined for every input
// including the zero Snapshot), and a stock whose evidence is missing gets a plan that says
// which evidence was missing.
//
// # Evidence arguments, and why they are arguments rather than lookups
//
// candlesByCode is the SAME series the analysis ran on, keyed by symbol, exactly as
// AttachInstitution takes it. It is here because two of EP-6C's four projections — the
// previous close and the adjustment age — are properties of the SERIES and of nothing on a
// WatchlistEntry. Passing nil is legal and means "no series was offered", which projects both
// as absent; it does not mean "there was no adjustment".
//
// market is the regime the market dashboard already computed, mirroring AIMarketContext.
// This pass must not derive one: cmd/market-fetch owns that rule, a scan that runs before it
// simply has no regime, and a regime invented here would key EP-2's policy table on a number
// nobody published.
//
// These inputs are not new sources of truth. They are already loaded or derived by the binary,
// so this signature moves no I/O into the bridge — it still reads no file, socket or clock.
type EntryPlanValuation struct {
	Status, Suitability, AsOf string
	BaseTarget                *float64
}

func AttachEntryPlan(entries []WatchlistEntry, candlesByCode map[string][]fetcher.Candle, market EntryPlanMarket, enabled bool, valuationViews ...map[string]EntryPlanValuation) {
	if !enabled || len(entries) == 0 {
		return
	}
	var valuations map[string]EntryPlanValuation
	if len(valuationViews) > 0 {
		valuations = valuationViews[0]
	}
	for i := range entries {
		snap := entryPlanSnapshotOf(&entries[i], candlesByCode[entries[i].A.Symbol], market)
		if v, ok := valuations[entries[i].A.Symbol]; ok && v.AsOf == snap.AsOf {
			snap.Valuation = entryplan.ValuationEvidence{
				Status: entryplan.Availability(v.Status), BaseTargetPrice: v.BaseTarget,
				Suitability: entryplan.ValuationSuitability(v.Suitability), PriceBasis: entryplan.PriceBasisRaw,
			}
		}
		p := entryplan.ComputePlan(snap)
		entries[i].EntryPlan = &p
	}
}

// EntryPlanMarket is the market read handed to the entry-plan bridge.
//
// Shaped after AIMarketContext (ai_attach.go), which solved the same problem for the same
// reason: Available is carried EXPLICITLY so "no dashboard snapshot covered this session"
// cannot arrive as a neutral market. There is no Score here — EP-2's policy table is keyed on
// the regime alone, and a field nothing reads is a field whose meaning is decided by the
// first accident that reads it.
type EntryPlanMarket struct {
	// Available is false when no market snapshot covered the analysis date. Regime is then
	// meaningless and must not be read.
	Available bool
	// Regime is model.Regime's string form, as persisted in the dashboard snapshot. It
	// arrives as a string because that is how research.MarketContext carries it through the
	// binary; entryPlanRegime is the one place it is turned back into a typed regime.
	Regime string
}

// entryPlanSnapshotOf projects ONE watchlist entry onto the entry-plan input contract.
//
// Every one of entryplan.Snapshot's seventeen fields is written explicitly, including the ones
// that are unavailable. Leaving a field at its zero value would produce the same struct and
// a worse file: the reader could not tell "nothing in the scan supplies this" from "somebody
// forgot", and those are the two halves of the census this projection exists to keep honest.
func entryPlanSnapshotOf(e *WatchlistEntry, candles []fetcher.Candle, market EntryPlanMarket) entryplan.Snapshot {
	a := e.A
	// ONE alignment check, done ONCE, before any field reads the series. See
	// entryPlanSeriesFor: a series that does not end on the session the analysis describes
	// is refused outright rather than indexed from the wrong end.
	series := entryPlanSeriesFor(e, candles)

	return entryplan.Snapshot{
		// 1. Symbol — the scan's own ticker, verbatim. Empty stays empty: ComputePlan
		// reports a malformed snapshot rather than inventing an identity for it.
		Symbol: a.Symbol,

		// 2. AsOf — the session the evidence describes, which is the date of the LAST
		// CANDLE the analysis ran on (StockAnalysis.Date = latest.Date), not the wall
		// clock and not the run date. Those differ on every weekend, holiday and
		// historical replay, and the plan is a statement about the session it was
		// computed from. A zero time is projected as "" — an unparsable placeholder date
		// would be WellFormed and mean nothing.
		AsOf: entryPlanAsOf(e),

		// 3. PriceBasis — RAW, and this is MEASURED rather than assumed.
		// internal/scanner/scanner.go builds StockAnalysis with `Close: latest.Close`,
		// the unadjusted exchange close, with no PriceForCalc in the path, so
		// UseAdjustedClose cannot change it. The MA20 and the ATR beside it come from
		// internal/indicator, which fills `closes[i] = c.Close` (calculator.go:33) — the
		// same series. This package never converts between bases and must not start here.
		//
		// TODO(EP-6C): the repo is MIXED on purpose — relstrength.go and vcp.go run on
		// PriceForCalc(c, UseAdjustedClose). Nothing from those paths is projected today;
		// anything that is must carry its own per-observation basis, never this one.
		PriceBasis: entryplan.PriceBasisRaw,

		// 4. AdjustmentAge — fetcher.LastAdjustmentAge(series), CARRIED VERBATIM.
		//
		// entryplan's own field doc requires exactly this: "carried verbatim. This package
		// does not and must not re-derive it". So does this one — the detection needs the raw
		// AND adjusted series together, and nothing here looks at either.
		//
		// ok == false becomes a NIL BarsAgo, never 0. LastAdjustmentAge returns 0 in that
		// case and 0 is simultaneously its "no answer" placeholder and its most alarming real
		// answer ("the newest bar IS the adjustment"), so the two must not share an encoding.
		// See entryPlanAdjustmentAge.
		AdjustmentAge: entryPlanAdjustmentAge(series),

		// 5. CurrentPrice — StockAnalysis.Close, the last close of the session named by
		// AsOf. The one piece of evidence with no substitute.
		CurrentPrice: entryPlanPrice(a.Close),

		// 6. ATR — StockAnalysis.ATR, Wilder(14) on the raw close. Non-positive is
		// projected as UNAVAILABLE rather than passed through: a zero ATR would claim a
		// stock that does not move, and EnrichWatchlist already requires 30 candles, so a
		// non-positive reading here is a gap in the data rather than a warm-up.
		ATR: entryPlanATR(a.ATR),

		// 7. Regime — the MARKET-LEVEL regime the dashboard already computed, handed in by
		// the caller. It is a market fact (internal/market/model.Regime, reaching this binary
		// as research.MarketContext), and SectorStage / RocketStage are still NOT it: a
		// sector's or a stock's stage substituted here would key EP-2's policy table on the
		// wrong axis and produce a permission nobody granted. See entryPlanRegime, which is
		// the only place this file touches a regime at all.
		Regime: entryPlanRegime(market),

		// 8. EntrySemantic — WatchAction, through EP-2's own written mapping.
		//
		// THE SCANNER ALREADY DECIDED THIS and the bridge only carries the answer: no chart
		// is re-read, no stage is consulted, no price is compared. See
		// entryPlanEntrySemantic for the arm-by-arm reasoning, including the two EXIT
		// actions and the OVERHEATED case.
		EntrySemantic: entryPlanEntrySemantic(e.WatchAction),

		// 9. MA20 — StockAnalysis.MA20, SMA(20) on the raw close
		// (internal/indicator/calculator.go). A pullback zone CANDIDATE; which candidate
		// becomes the centre is entryplan.SelectCenter's rule, not this file's.
		MA20: entryPlanPrice(a.MA20),

		// 10. MA60 — STILL UNAVAILABLE, and the reason is NOT that nobody computes a
		// 60-session average. FOUR PRODUCTION CALL SITES IN THIS VERY PACKAGE DO:
		//
		//	horizonhint.go:187      ma60 := indicator.SMA(closes, 60) — and it uses it as a
		//	                        PULLBACK SUPPORT for this same stock (A_MA60_PULLBACK),
		//	                        which is exactly what SelectCenter would use it for
		//	holdinghorizon.go:122   ma60Series := indicator.SMA(closes, 60) — slope + deviation
		//	trendext.go:353         indicator.SMA(closes, 60) — slope per day
		//	rotation.go:371         indicator.SMA(closes, 60) — sector-level slope / above-MA60
		//
		// WHAT IS ACTUALLY TRUE, and it is narrower: NO PRODUCER PUBLISHES THE MA60 LEVEL.
		// indicator.Result carries MA20 and VolumeMA and nothing longer, StockAnalysis has no
		// MA60 field, and every one of the four sites above computes the series IN PLACE and
		// throws the price away — what survives is a setup label, a slope, a deviation or a
		// boolean, never a level anyone could place an order at.
		//
		// So this is a REFUSAL TO BECOME THE FIFTH SITE, not a claim that the number is
		// unobtainable. EP-6C already holds the candle series (it needs it for the previous
		// close and the adjustment age), so `indicator.SMA(closes, 60)[n-1]` is one line away.
		// Two things make it the wrong line:
		//
		//   - It would be a FIFTH private warm-up and zero-value convention. The four above
		//     already disagree, on minimum length and on the zero test:
		//       horizonhint     len(closes) >= 60, reads only ma60[n-1], then m > 0
		//       holdinghorizon  n >= max(hh_min_history_days, 60+hhSlopeLookback) — 70 by
		//                       default (holdinghorizon.go:107) — reads ma60[n-1] and
		//                       ma60[n-11], then ma60 == 0 || ma60Prev == 0 (a negative
		//                       level would pass; the others reject it)
		//       rotation        n >= 60, reads ma60[n-1] and ma60[n-6], then both > 0
		//       trendext        no length guard of its own; MASlopePerDay rejects <= 0
		//     Only rotation (n in 60..64, ma60[n-6]) and trendext (short series) ever reach
		//     SMA's warm-up 0 and read it as "absent"; horizonhint and holdinghorizon rule the
		//     warm-up out by length, so their zero tests only catch bad close data. Four
		//     sites, four answers — the MISSING ≠ ZERO question this bridge must not answer
		//     a fifth way privately.
		//   - It would publish a LEVEL no producer stands behind, into a field whose reader
		//     (entryplan.SelectCenter) weighs it against MA20 as an equal candidate.
		//
		// Adding it is the indicator layer's change, with its own review: internal/indicator
		// produces it, StockAnalysis carries it, and THEN this file projects it — three
		// packages each doing its own job, instead of one bridge doing all three.
		//
		// The consequence is visible and accepted: a pullback entry is centred on MA20 or
		// the base low only, so a stock whose pullback support really is the 60-day line
		// gets no zone rather than a zone drawn from a level nobody published.
		//
		// TODO(separate Work Item, not yet numbered — indicator layer: publish MA60 via
		// internal/indicator + StockAnalysis; re-homed from EP-7, which is persistence): if the
		// 60-day line is wanted as a pullback candidate, add MA60 to
		// internal/indicator + StockAnalysis FIRST and reconcile it with the four existing
		// call sites above — horizonhint.go:187 is the prior art that matters, because it
		// already treats MA60 as a per-stock pullback support and therefore already owns a
		// warm-up and zero-value answer this repo should have exactly one of. Once it exists,
		// this line becomes entryPlanPrice(a.MA60) and nothing else here changes.
		MA60: entryplan.PriceObservation{Status: entryplan.Unavailable},

		// 11. BaseLow — Consolidation.BaseLow, the scan's own support level. Zero is
		// projected as UNAVAILABLE: analyzeConsolidation returns a zero-valued
		// Consolidation when there are fewer than 12 candles, and 0 is not a support price.
		BaseLow: entryPlanPrice(e.Consol.BaseLow),

		// 12. PivotHigh — Consolidation.PivotHigh, the scan's own 突破價.
		//
		// WHAT IT ACTUALLY IS, stated rather than implied and RE-VERIFIED under EP-6C
		// against consolidation.go as it stands today:
		//
		//	days >= 3 branch   consolidation.go:100-101  prevHigh, _ := windowHighLow(
		//	                   candles, n-1-min(60,n-1), n-1); c.PivotHigh = prevHigh
		//	days <  3 branch   consolidation.go:85       c.PivotHigh, _ = windowHighLowF(
		//	                   candles, n-1-min(60,n-1), n-1)
		//
		// BOTH branches are therefore the HIGHEST HIGH OF THE LAST min(60, n-1)+1 BARS. The
		// base window's own high IS computed in the first branch (`pivotHigh, baseLow :=
		// windowHighLow(candles, baseStart, n-1)`) but it is consumed only by RangePct and
		// by the compression score — it is never published on the struct, so this bridge
		// cannot read it without recomputing the base window, which it must not do.
		//
		// entryplan's field doc describes "the high of the base being broken". The 60-bar
		// high is the level the SCANNER ITSELF calls 突破價 and prices NearPreviousHigh
		// against, so it is the repo's existing breakout semantic and that is what EP-6C
		// projects. It is not identical to the base-window high: on a stock whose base sits
		// well below a 60-bar spike, this level is HIGHER, so a breakout zone drawn from it
		// is further above the market and the plan is more conservative, never less.
		//
		// The difference is recorded rather than papered over. entryplan publishes this
		// number under the evidence key LEVEL_BREAKOUT_PIVOT, and a reader who needs to know
		// which window produced it is reading this comment and consolidation.go, because
		// entryplan.Evidence has no field this bridge can put the caveat in — EP-6C does not
		// change entryplan's contract to add one.
		PivotHigh: entryPlanPrice(e.Consol.PivotHigh),

		// 13. PreviousClose — series[n-2].Close, READ rather than reconstructed.
		//
		// It exists for exactly one computation, pricerule.LimitUpPrice. It is still NOT
		// rebuilt from Close and PriceChangePct — that number is a rounded ratio and a price
		// limit derived from it would be a fabricated reference — and CurrentPrice is still
		// not a substitute, because a band computed from today's price moves with the thing
		// it is meant to bound.
		//
		// THE CAVEAT entryplan's field doc demands of its caller APPLIES AND IS NOT CHECKED
		// HERE: on an ex-dividend, ex-rights or post-capital-reduction session the exchange
		// computes the daily band from the ADJUSTED REFERENCE PRICE (除權息參考價 /
		// 減資換發參考價), not from the raw previous close projected here. This bridge has no
		// corporate-action calendar, so it cannot detect those sessions — what it CAN do is
		// publish the AdjustmentAge above beside it, which is the evidence a reader needs to
		// notice the risk. entryplan itself attaches the standing prose caveat whenever it
		// actually computes a limit-up clamp (planCaveats, `if chase.Trace.LimitUp != nil`),
		// so the warning travels with the number rather than living only here.
		//
		// Absent, the chase ceiling is simply not published and the entry zone survives; the
		// two availabilities are independent by design.
		PreviousClose: entryPlanPreviousClose(series),

		// 14. AvgVolume20 — StockAnalysis.AvgVolume20, SMA(20) of volume, in shares.
		// RECORDED AND NEVER A GATE (entryplan has no backtested fill threshold and must
		// not invent one). Non-positive is projected as absent rather than as "this stock
		// does not trade".
		AvgVolume20: entryPlanAvgVolume(a.AvgVolume20),

		// 15. BaseLowKind — the fact that decides whether BaseLow may be an INVALIDATION
		// rather than merely a zone centre. Read off Consolidation.Bucket, which is the
		// same switch analyzeConsolidation used to fill BaseLow. See entryPlanBaseLowKind.
		BaseLowKind: entryPlanBaseLowKind(e.Consol.Bucket),

		// 16. Valuation — initialized unavailable here. cmd/scanner may overlay evidence after
		// deriving the BASE target and independent suitability verdict from fundamental and
		// valuation archives loaded for the exact entry AsOf. AttachEntryPlan accepts that
		// narrow view as an argument so this bridge remains pure and healthcheck-independent.
		// AVAILABLE evidence caps Target2 when its suitability PermitsCeiling (input.go): any
		// known code other than UNSUITABLE — SUITABLE, CONDITIONAL, WEAK or INSUFFICIENT_DATA.
		// UNSUITABLE, "", an unknown code, or non-AVAILABLE evidence retains the structural target.
		// On an S1 (sell-side contradiction) plan no entry step runs, so there is no Target2 or
		// step trace, and the VALUATION_BASE_TARGET evidence row carries no value.
		Valuation: entryplan.ValuationEvidence{Status: entryplan.Unavailable},

		// 17. Thesis — EP-6D. Whether the scanner's PRIMARY Action (StockAnalysis.Action)
		// contradicts the entry thesis, reduced to entryplan's own vocabulary. The thesis
		// itself is the WatchAction already projected at 8; this is the protection the user
		// added on top of it: a SELL / REDUCE / TAKE PROFIT / STOP LOSS stock shows no entry
		// (entryplan answers NO_VALID_ENTRY with no price). Read verbatim off the finished
		// analysis — no chart is re-read, and what the standing DOES is entryplan's decision,
		// not this file's. See entryPlanThesis.
		Thesis: entryPlanThesis(a.Action),
	}
}

// entryPlanThesis reduces the scanner's primary Action to entryplan's thesis standing. EP-6D.
//
// # The user's decision, transcribed and not widened
//
// 2026-09-14: WatchAction PULLBACK_BUY / BREAKOUT_BUY IS the existing BUY thesis, and an
// exit-side primary Action contradicts it. The user first named SELL and REDUCE and then (EP-6D
// review) ruled TAKE PROFIT and STOP LOSS into the same set, so CONTRADICTED is exactly {ActionSell, ActionReduce,
// ActionTakeProfit, ActionStopLoss}. This function decides nothing else, and in
// particular it does not re-judge whether HOLD or WATCH "really" support a buy: the thesis is
// the WatchAction, and those Actions do not contradict it.
//
// # Every Action in types.go, explicitly
//
//	ActionStrongBuy  "STRONG BUY"   → NOT_CONTRADICTED
//	ActionBuy        "BUY"          → NOT_CONTRADICTED
//	ActionWatch      "WATCH"        → NOT_CONTRADICTED
//	ActionHold       "HOLD"         → NOT_CONTRADICTED
//	ActionReduce     "REDUCE"       → CONTRADICTED     (the user's decision)
//	ActionSell       "SELL"         → CONTRADICTED     (the user's decision)
//	ActionTakeProfit "TAKE PROFIT"  → CONTRADICTED     (the user's decision, EP-6D review)
//	ActionStopLoss   "STOP LOSS"    → CONTRADICTED     (the user's decision, EP-6D review)
//	""               (not analysed) → "" UNCLASSIFIED
//	anything else    (future value) → "" UNCLASSIFIED
//
// # "" and unknown values are UNCLASSIFIED, never NOT_CONTRADICTED
//
// MISSING ≠ ZERO. An Action nobody set, or one added after this switch was written, is not
// evidence that the scanner does not want out — whoever adds an Action classifies it here, the
// same discipline entryPlanEntrySemantic applies to WatchAction.
func entryPlanThesis(a Action) entryplan.ThesisStanding {
	switch a {
	case ActionStrongBuy, ActionBuy, ActionWatch, ActionHold:
		return entryplan.ThesisNotContradicted
	case ActionSell, ActionReduce, ActionTakeProfit, ActionStopLoss:
		return entryplan.ThesisContradicted
	case "":
		return ""
	default:
		return ""
	}
}

// entryPlanAsOf formats the session the entry's evidence describes.
//
// Returns "" for a zero date, which ComputePlan reports as a malformed snapshot. Formatting a
// zero time.Time would yield "0001-01-01", which parses as a date and would make an
// unidentifiable plan look well-formed.
func entryPlanAsOf(e *WatchlistEntry) string {
	if e.A.Date.IsZero() {
		return ""
	}
	return e.A.Date.Format(entryPlanAsOfLayout)
}

// entryPlanPrice projects one scanner float64 onto a price observation on the RAW basis.
//
// The scan stores "no value" as 0 in every one of these fields, so the guard is the same one
// entryplan and internal/technical apply: NaN, ±Inf and non-positive are not prices, they are
// gaps, and a gap is projected as UNAVAILABLE with no value attached.
//
// UNAVAILABLE rather than INSUFFICIENT_DATA: these fields are filled from a finished analysis
// of at least 30 candles, so a missing one is not something more data would arrive for. The
// two are kept apart by entryplan precisely so this choice has to be made deliberately.
func entryPlanPrice(v float64) entryplan.PriceObservation {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return entryplan.PriceObservation{Status: entryplan.Unavailable}
	}
	value := v // copied, so a later mutation of the caller's struct cannot reach the plan
	return entryplan.PriceObservation{
		Status:     entryplan.Available,
		Value:      &value,
		PriceBasis: entryplan.PriceBasisRaw,
	}
}

// entryPlanATR projects StockAnalysis.ATR, carrying the period and the basis it was computed
// on. A non-positive or non-finite reading is UNAVAILABLE with no value: a zero ATR would
// assert that the stock does not move, which is a claim, not a gap.
//
// The period travels even when the value does not, because "the caller did not say" and "the
// value is missing" are different rows on the evidence census.
func entryPlanATR(v float64) entryplan.ATREvidence {
	ev := entryplan.ATREvidence{
		Status:     entryplan.Unavailable,
		Period:     entryPlanATRPeriod,
		PriceBasis: entryplan.PriceBasisRaw,
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return ev
	}
	value := v
	ev.Status = entryplan.Available
	ev.Value = &value
	return ev
}

// entryPlanAvgVolume projects the 20-session average volume, in shares.
//
// Returns nil for a non-positive average. Volume carries no PriceBasis — adjustment factors
// restate prices, not share counts — which is why entryplan takes a plain *float64 here.
func entryPlanAvgVolume(v int64) *float64 {
	if v <= 0 {
		return nil
	}
	f := float64(v)
	return &f
}

// entryPlanBaseLowKind says WHICH QUESTION Consolidation.BaseLow answered, because
// analyzeConsolidation answers two with the same field:
//
//	days >= 3 (any named base bucket)  BaseLow = windowHighLow(candles, baseStart, n-1)
//	                                   → the low of a base defended for 3+ sessions
//	Bucket == NO_BASE                  BaseLow = latest.Low (or 0 on <12 candles)
//	                                   → one session's worst tick
//
// Both are plausible support prices and no downstream check can tell them apart, which is why
// entryplan refuses the second as a thesis-invalidation level.
//
// The switch is EXHAUSTIVE over the buckets consolidation.go declares, and a bucket this
// function has never heard of returns UNKNOWN rather than falling into the structural arm: a
// future bucket must be classified by whoever adds it, not promoted to "structural" by
// default. The empty bucket returns "" — the caller never said, which entryplan keeps
// distinct from UNKNOWN ("the producer looked and cannot say").
func entryPlanBaseLowKind(b ConsolBucket) entryplan.BaseLowKind {
	switch b {
	case MicroBase, ShortBase, SwingBase, MidBase, LongBase:
		return entryplan.BaseLowConsolidationBase
	case NoBase:
		return entryplan.BaseLowLatestBar
	case "":
		return ""
	default:
		return entryplan.BaseLowKindUnknown
	}
}

// entryPlanRegime projects the market read onto entryplan's regime evidence.
//
// THREE OUTCOMES, and the difference between them is the whole reason the function exists:
//
//	no snapshot covered this session  →  UNAVAILABLE          nobody computed a regime
//	"UNKNOWN" in the snapshot         →  AVAILABLE + UNKNOWN   computed, no call possible
//	a regime string nobody emits      →  UNAVAILABLE           we cannot read what was said
//
// The middle one is the one that is easy to get wrong and expensive to get wrong.
// entryplan.RegimeEvidence's doc states the distinction it needs: "Status != AVAILABLE — the
// regime was never computed / never handed to us" versus "Regime == UNKNOWN — it WAS computed,
// and the data could not support a call". ComputePlan reads the two differently (plan.go marks
// the second INSUFFICIENT_DATA and keeps the text), so collapsing them here would destroy a
// distinction the layer below is built to preserve — the same one analyzer.DecideRegime's R0
// refuses to collapse between UNKNOWN and SIDEWAYS.
//
// The third is refused rather than passed through. A string model.Regime.Valid() rejects is a
// snapshot this binary cannot interpret (an older vocabulary, a newer one, a typo), and
// labelling it AVAILABLE would hand entryplan a regime whose policy row does not exist. It is
// NOT mapped to UNKNOWN either: UNKNOWN asserts that the market engine looked and could not
// decide, and we have no idea whether it did.
//
// This function computes NO regime. It has no index data, no breadth, no moving average and
// no access to internal/market/analyzer, which is exactly the intended shape: cmd/market-fetch
// owns the regime rule, and a scan that runs before it has no regime rather than a guess.
func entryPlanRegime(m EntryPlanMarket) entryplan.RegimeEvidence {
	if !m.Available || m.Regime == "" {
		return entryplan.RegimeEvidence{Status: entryplan.Unavailable}
	}
	r := model.Regime(m.Regime)
	if !r.Valid() {
		return entryplan.RegimeEvidence{Status: entryplan.Unavailable}
	}
	return entryplan.RegimeEvidence{Status: entryplan.Available, Regime: r}
}

// entryPlanEntrySemantic maps the scanner's WatchAction onto entryplan's entry semantic.
//
// # This is a TRANSCRIPTION of a mapping that already exists, not a new rule
//
// internal/entryplan/policy.go writes the table down in prose, in the EntrySemantic doc, and
// says where it must be implemented: "entryplan may not import internal/scanner (the
// architecture test enforces it), and the adapter belongs on the scanner side of the wiring
// where the WatchAction is in scope". This is that adapter. Every arm below is the row EP-2
// published; none of them were chosen here.
//
//	ActPullbackBuy   PULLBACK_BUY           → PULLBACK              (EP-2's row)
//	ActBreakoutBuy   BREAKOUT_BUY           → BREAKOUT              (EP-2's row)
//	ActPrepare       PREPARE_ENTRY          → AVAILABLE + UNKNOWN   (EP-2's row)
//	ActWatchClose    WATCH_CLOSELY          → AVAILABLE + UNKNOWN   (EP-2's row)
//	ActWait          WAIT                   → AVAILABLE + UNKNOWN   (EP-2's row)
//	ActTakeProfit    TAKE_PROFIT            → UNAVAILABLE           (see below)
//	ActRemove        REMOVE_FROM_WATCHLIST  → UNAVAILABLE           (see below)
//	""               (never enriched)       → UNAVAILABLE
//	anything else    (a future action)      → UNAVAILABLE
//
// # AVAILABLE + UNKNOWN is not the same absence as UNAVAILABLE, and the split is deliberate
//
// PREPARE_ENTRY / WATCH_CLOSELY / WAIT are cases where THE SCANNER LOOKED AND NAMED NO ENTRY
// SHAPE — a setup is forming, or it is being watched, and neither says which side of the
// market the entry sits on. That is precisely EntrySemanticUnknown, whose doc calls it "a
// STATE, exactly like model.RegimeUnknown". Handing it over with Status = AVAILABLE says "we
// answered, and the answer is that the shape is not settled", which is a different sentence
// from "nobody asked us", and plan.go prints them differently (INSUFFICIENT_DATA + the text,
// versus UNAVAILABLE with nothing).
//
// Picking PULLBACK or BREAKOUT for PREPARE_ENTRY was considered and refused for EP-2's stated
// reason: it "says a setup is forming, not which side of the market the entry sits on, and
// picking one here would be entryplan acquiring the second opinion doc.go forbids".
//
// # The two EXIT actions
//
// entryplan/policy.go: TAKE_PROFIT and REMOVE_FROM_WATCHLIST mean "no plan should be requested
// at all", and it declines to add a fourth NOT_AN_ENTRY semantic for them, saying "the place
// to catch it is the adapter that would have to construct the value" — here.
//
// WHAT EP-6C ACTUALLY DOES WITH THAT, stated plainly because it is less than the doc imagines:
// this pass projects EVERY watchlist entry unconditionally (EP-6A's shape, which EP-6B's
// tests hold in place — ON means every entry carries a plan), so it does not get to decline
// the request. What it CAN do is refuse to manufacture an entry semantic for an exit, and it
// does: Status = UNAVAILABLE, no semantic, which routes through ComputePlan to
// INSUFFICIENT_DATA with ReasonEntrySemanticUnavailable. The resulting plan says "there is no
// entry evidence here", which is true.
//
// It is NOT mapped to UNKNOWN. UNKNOWN would claim the scanner was asked about an entry and
// could not decide, when in fact it proposed an EXIT — and that is the confusion EP-2 asked
// this adapter to prevent.
//
// It is also NOT a hard error or a log line: a TAKE_PROFIT stock on the watchlist is the
// normal state of a position the scan wants out of, not a caller bug, and the wiring that
// would make "a plan was requested for an exit" a detectable BUG is a selective attach this
// item does not build.
//
// EP-8 SETTLED THE DISPLAY HALF OF THAT, AND IT CHANGED NOTHING HERE. The user's rule is that
// report section ⑲ renders only for a plan that publishes a BUY-SIDE entry semantic, so an exit
// action shows no section (internal/report/report_entryplan.go, epBuySideSemantic). That test is
// applied to the PUBLISHED PLAN's ENTRY_SEMANTIC evidence row in the report, not here: this pass
// still projects every entry, every entry still carries its plan, and EP-7 still persists it
// (internal/research/entryplan_evidence_test.go,
// TestEntryPlanExitActionEntryStillPersistsItsRows). A selective attach — computing no plan for an
// exit action — remains unbuilt and is still not this file's decision.
//
// What the display rule does NOT establish: the evidence row says UNAVAILABLE for an exit action,
// for an entry the rocket pass never enriched, and for a WatchAction this switch has never heard
// of. Hiding the section is therefore not evidence that the entry was an exit.
//
// # The OVERHEATED case: this function must not special-case it
//
// The scanner's OVERHEATED stage is ALREADY resolved into a WatchAction by rocket.go before
// this bridge sees anything: watchActionFor returns ActTakeProfit when overheatExit is true
// (real distribution) and ActPullbackBuy when it is not ("simply extended in a healthy trend
// → wait for the pullback to enter"). So an overheated-but-intact stock arrives here as
// PULLBACK_BUY and is projected as PULLBACK, full stop.
//
// It is deliberately NOT forced to entryplan's TOO_EXTENDED. That status has its own NUMERIC
// condition (a chase ceiling exists and the market is strictly above it), and entryplan's own
// doc says it "is NOT an alias for the scanner's OVERHEATED caution ... aliasing the two would
// destroy the ability to notice they disagree". Reading RocketStage here to pre-empt the
// decision table would be this bridge deciding a status — which is EP-5's job — and it would
// silence exactly the disagreement the two layers exist to expose. RocketStage is not read by
// this file at all; the parameter list does not even include it.
func entryPlanEntrySemantic(a WatchAction) entryplan.EntrySemanticEvidence {
	switch a {
	case ActPullbackBuy:
		return entryplan.EntrySemanticEvidence{
			Status:   entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback,
		}
	case ActBreakoutBuy:
		return entryplan.EntrySemanticEvidence{
			Status:   entryplan.Available,
			Semantic: entryplan.EntrySemanticBreakout,
		}
	case ActPrepare, ActWatchClose, ActWait:
		// The scanner answered, and the answer names no entry shape.
		return entryplan.EntrySemanticEvidence{
			Status:   entryplan.Available,
			Semantic: entryplan.EntrySemanticUnknown,
		}
	case ActTakeProfit, ActRemove:
		// An EXIT decision. There is no entry semantic to hand over, and inventing one —
		// including UNKNOWN — would misreport what the scanner said.
		return entryplan.EntrySemanticEvidence{Status: entryplan.Unavailable}
	case "":
		// The entry was never enriched (computeRocket sets ActWait even for <30 candles, so
		// an empty action means the rocket pass did not run at all). Nobody said anything,
		// which is UNAVAILABLE rather than "looked and could not say".
		return entryplan.EntrySemanticEvidence{Status: entryplan.Unavailable}
	default:
		// An action this adapter has never heard of. It must NOT default into UNKNOWN: that
		// value asserts the upstream layer looked and could not decide, and about a code
		// added after this switch was written we know nothing at all. Whoever adds a
		// WatchAction classifies it here — the same discipline entryPlanBaseLowKind applies
		// to an unrecognised consolidation bucket.
		return entryplan.EntrySemanticEvidence{Status: entryplan.Unavailable}
	}
}

// entryPlanSeriesFor returns the candle series this entry's evidence may be read from, or nil.
//
// # The alignment check is the point of the function
//
// Two EP-6C projections index the series — the previous close and the adjustment age — and
// both are only meaningful if the series ENDS ON THE SESSION THE ANALYSIS DESCRIBES. If the
// caller hands over a longer series (a later fetch, a cached file, a replay whose slice was
// not cut), then series[n-2] is not the previous close of the plan's session and
// LastAdjustmentAge measures from a bar in the plan's future. Both would be LOOKAHEAD, both
// would produce plausible numbers, and no unit test on the resulting value could tell.
//
// So the series is refused outright unless candles[n-1].Date equals StockAnalysis.Date, which
// is itself `latest.Date` of the series the analysis ran on (scanner.go:455). Equality, not
// "within a day" — a tolerance is how one bar of lookahead gets permitted.
//
// This is the caller-side slicing entryplan.Snapshot's doc asks for ("the caller does the
// slicing, once, where the AsOf bar is in scope"), done in ONE place so the two fields below
// cannot disagree about which bar is the last one.
func entryPlanSeriesFor(e *WatchlistEntry, candles []fetcher.Candle) []fetcher.Candle {
	n := len(candles)
	if n == 0 || e.A.Date.IsZero() {
		return nil
	}
	if !candles[n-1].Date.Equal(e.A.Date) {
		return nil
	}
	return candles
}

// entryPlanPreviousClose projects the previous session's close off an ALIGNED series.
//
// Requires at least two bars: with one bar there is no previous session, and the current
// close is not a substitute (entryplan.Snapshot.PreviousClose says why — a price band built
// from today's price moves with the thing it bounds).
//
// The value goes through entryPlanPrice, so a zero or non-finite close in the data is an
// ABSENCE and never a price. MISSING ≠ ZERO: a previous close of 0 would make
// pricerule.LimitUpPrice compute a ceiling of 0, which is a number that passes every
// finite/positive/tick check downstream and forbids every purchase.
func entryPlanPreviousClose(series []fetcher.Candle) entryplan.PriceObservation {
	n := len(series)
	if n < 2 {
		return entryplan.PriceObservation{Status: entryplan.Unavailable}
	}
	return entryPlanPrice(series[n-2].Close)
}

// entryPlanAdjustmentAge carries fetcher.LastAdjustmentAge's answer, verbatim.
//
// ok == false means THERE IS NO ANSWER and it becomes a nil BarsAgo — never 0. LastAdjustmentAge
// returns 0 alongside ok == false, and 0 is also its most alarming real answer ("the newest bar
// IS the adjustment"), so a caller that dropped the bool would read "we could not tell" as "an
// adjustment landed today".
//
// THIS FILE APPLIES NO POLICY TO THE AGE. No `if age <= 63 { ... }` here: LastAdjustmentAge's
// own doc spends a page on why gating on it is wrong twice over, entryplan.AdjustmentAge says
// the threshold "belongs to the consumer's error budget", and the consumer is entryplan, which
// turns a recent age into a CAVEAT (planCaveats) and withdraws nothing. The bridge's job is to
// carry the number, not to decide what it means.
func entryPlanAdjustmentAge(series []fetcher.Candle) entryplan.AdjustmentAge {
	if len(series) == 0 {
		return entryplan.AdjustmentAge{}
	}
	barsAgo, ok := fetcher.LastAdjustmentAge(series)
	if !ok {
		return entryplan.AdjustmentAge{}
	}
	age := barsAgo // copied, so the projection owns its own storage
	return entryplan.AdjustmentAge{BarsAgo: &age}
}
