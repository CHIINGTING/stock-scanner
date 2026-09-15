package entryplan

import (
	"math"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Availability mirrors internal/valuation's vocabulary, which mirrors internal/fundamental's,
// so a consumer reads one contract across the repo.
type Availability string

const (
	// Available — the value is present and usable.
	Available Availability = "AVAILABLE"
	// Unavailable — there is no source for this value here. Nothing to wait for.
	Unavailable Availability = "UNAVAILABLE"
	// InsufficientData — computable in principle, but the evidence does not reach yet.
	// Distinct from UNAVAILABLE: one is "wait", the other is "there is nothing to wait for".
	InsufficientData Availability = "INSUFFICIENT_DATA"
)

// OK reports whether a status carries a usable reading. The zero value ("") does not.
func (a Availability) OK() bool { return a == Available }

// PriceBasis is which price series a number was computed from.
//
// A TYPE, not a comment, and it is here because this repo is CURRENTLY MIXED — measured, not
// assumed:
//
//	internal/indicator/calculator.go:33   closes[i] = c.Close
//	  → MA20 / ATR / RSI / Bollinger all run on the RAW, unadjusted close.
//	internal/scanner/relstrength.go:124   fetcher.PriceForCalc(c, cfg.UseAdjustedClose)
//	internal/scanner/vcp.go:236           fetcher.PriceForCalc(c, cfg.UseAdjustedClose)
//	  → relative strength and VCP run on whichever basis the config selects.
//
// So "the MA20" and "the 20-day RS" may not be measured on the same series, and an entry zone
// built by subtracting one from the other would be arithmetic performed across two different
// price universes. Carrying the basis on every price evidence makes that mismatch DETECTABLE
// by the layer that would otherwise commit it.
//
// This package NEVER CONVERTS between bases. It cannot: converting needs the adjustment
// factors, which are the fetcher's, and a plausible-looking conversion here would be a
// fabricated price. It also does not fix the existing mix — changing what internal/indicator
// reads would move every strategy score in production, which the repo's "Production Scoring:
// NO MIGRATION" rule forbids. It records, and refuses when the record is missing.
type PriceBasis string

const (
	// PriceBasisRaw — the exchange's unadjusted closing price. What a limit order is
	// actually placed at, and what internal/indicator computes on today.
	PriceBasisRaw PriceBasis = "RAW"
	// PriceBasisAdjusted — back-adjusted for corporate actions. Comparable across an
	// ex-dividend date, and NOT the number an order is priced at.
	PriceBasisAdjusted PriceBasis = "ADJUSTED"
)

// Supported reports whether a basis is one this package can interpret.
//
// The zero value "" is NOT a basis. It is a caller who did not say, which is exactly the
// situation the type exists to catch, so it is refused rather than defaulted to RAW —
// defaulting would silently assert the very fact the field was added to establish.
func (b PriceBasis) Supported() bool {
	return b == PriceBasisRaw || b == PriceBasisAdjusted
}

// AdjustmentAge is how long ago the most recent corporate-action adjustment landed, in bars.
//
// SOURCE: internal/fetcher.LastAdjustmentAge(candles) (barsAgo int, ok bool), carried
// verbatim. This package does not and must not re-derive it — the detection needs the raw and
// adjusted series, which are deliberately not in the Snapshot.
//
// BarsAgo is a pointer because LastAdjustmentAge's ok == false means THERE IS NO ANSWER, and
// it returns 0 in that case: 0 is simultaneously the "no answer" placeholder and the most
// alarming possible answer ("the newest bar IS the adjustment"). A nil pointer is the only
// encoding of that distinction which a caller cannot read past by accident. Three different
// situations produce nil, and only one of them is "no recent adjustment" — see the doc on
// LastAdjustmentAge.
//
// # 63, not 65
//
// When a consumer eventually gates on this age, the number is 63.
//
// A real ex-dividend RESTATES every close before the event bar k by the same ratio, so every
// delta below k keeps both endpoints on one basis and is merely rescaled. Exactly ONE delta
// mixes bases: d[k] = c[k] - c[k-1]. Its age is len(candles)-1-k, which is precisely what
// LastAdjustmentAge returns. The age in hand is therefore already a DELTA age, and the
// delta-age threshold is the one that applies: ((14-1)/14)^63 < 1%, the same 63 as ATR(14),
// for the same reason.
//
// 65 is the RSI answer for an ISOLATED BUMP TO ONE CLOSE — a real number for a real question,
// and the wrong question here, because an ex-dividend is not that action. It must not be
// substituted for 63 against this field. (internal/fetcher/adjustment.go says all of this at
// length; it is repeated here because the mistake is made at the CONSUMER, which is this side.)
//
// EP-1 APPLIES NO POLICY TO THIS FIELD. No "age > N is usable" rule lives here: the threshold
// belongs to the consumer's error budget, and the consumer is the work item that computes on
// ATR-derived widths, not the one that defines the struct.
type AdjustmentAge struct {
	BarsAgo *int `json:"bars_ago,omitempty"`
}

// Known reports whether LastAdjustmentAge had an answer at all.
func (a AdjustmentAge) Known() bool { return a.BarsAgo != nil }

// PriceObservation is one observed price with its own availability and its own basis.
type PriceObservation struct {
	Status Availability `json:"status"`
	// Value is nil whenever Status is not AVAILABLE, and may be nil even then only as a
	// caller bug — Usable() treats that as unavailable rather than trusting the label.
	Value *float64 `json:"value,omitempty"`
	// PriceBasis of THIS observation, carried per-item rather than per-snapshot because
	// the repo mixes bases per-indicator. See PriceBasis.
	PriceBasis PriceBasis `json:"price_basis"`
}

// Usable reports whether the observation can be read as a NUMBER: available, present, finite
// and strictly positive.
//
// A non-positive close is not a price, it is a gap in the data — the same judgement
// internal/technical.newSeries makes about its bars.
//
// It deliberately does NOT check the basis. "There is no price" and "the price is on a basis
// we cannot name" are different failures with different reason codes, and folding the second
// into this method would report a present price as missing.
func (p PriceObservation) Usable() bool {
	return p.Status.OK() && p.Value != nil && finitePositive(*p.Value)
}

// BasisOr returns the observation's own basis, falling back to the snapshot-level basis when
// the observation did not declare one.
//
// Per-item basis EXISTS because the repo mixes bases per-indicator (see PriceBasis); it is
// OPTIONAL because most snapshots read every price off one series and repeating the label on
// each item would invite the two copies to disagree. An unset item basis means "the
// snapshot's", never "RAW".
func (p PriceObservation) BasisOr(def PriceBasis) PriceBasis {
	if p.PriceBasis != "" {
		return p.PriceBasis
	}
	return def
}

// finitePositive is the price guard, matching internal/technical: NaN, ±Inf and non-positive
// values are all "not a price".
func finitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

// finite reports whether a value may be published. Every number this package emits passes
// through here, so a non-finite input can never reach JSON, HTML, SQLite or an agent prompt.
func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// ATREvidence is the volatility reading, carried as EVIDENCE METADATA only.
//
// EP-1 does not consume Value for anything, and — deliberately — an absent ATR does NOT make
// ComputePlan decide anything about the entry. Requiring ATR is a POLICY, it belongs to the
// work item that actually multiplies by it (zone width, chase limits), and pre-deciding it
// here would put a hard requirement in the type layer where the affected item cannot see or
// revise it. What EP-1 owes ATR is a place to be recorded, including its Period and its
// PriceBasis, because internal/indicator computes ATR on the RAW close (calculator.go:33)
// while other evidence in the same Snapshot may not be.
type ATREvidence struct {
	Status Availability `json:"status"`
	// Value is the ATR in price units. Nil when unavailable; never 0-for-missing, since a
	// zero ATR would claim a stock that does not move.
	Value *float64 `json:"value,omitempty"`
	// Period is the Wilder period the value was computed over (14, 20, ...). 0 means the
	// caller did not say, which is only meaningful when Value is nil.
	Period int `json:"period,omitempty"`
	// PriceBasis the ATR was computed on.
	PriceBasis PriceBasis `json:"price_basis,omitempty"`
}

// RegimeEvidence is the market's structural state at AsOf.
//
// It reuses model.Regime rather than declaring a parallel vocabulary, because that type's doc
// is explicit that it is "the ONE regime vocabulary in the codebase" — and because
// model.RegimeUnknown is the precedent this package's INSUFFICIENT_DATA is built on.
//
// Two independent ways of being absent are kept apart on purpose:
//
//	Status != AVAILABLE     the regime was never computed / never handed to us
//	Regime == UNKNOWN       it WAS computed, and the data could not support a call
//
// Both leave an entry plan without a regime, and they are not the same fact.
type RegimeEvidence struct {
	Status Availability `json:"status"`
	Regime model.Regime `json:"regime,omitempty"`
}

// Usable reports whether a regime call is actually in hand.
func (r RegimeEvidence) Usable() bool {
	return r.Status.OK() && r.Regime.Valid() && r.Regime != model.RegimeUnknown
}

// EntrySemanticEvidence is WHICH KIND of entry the upstream layer is proposing, with its own
// availability.
//
// # Why this field exists at all
//
// The confidence census in plan.go counts "an entry evaluation" as REQUIRED item #4, and until
// EP-2 the Snapshot had no field that could ever carry it — so "required" meant "never
// satisfiable", the required floor could never clear, and every plan was INSUFFICIENT_DATA for
// a reason no input could change. A census item nothing can fill is not a requirement, it is a
// permanently-failing assertion, and it made the whole confidence axis dead.
//
// EP-2 fills the SLOT and nothing else. The semantic is an INPUT, produced by internal/scanner
// (see EntrySemantic for the mapping EP-5 is expected to implement); this package does not
// derive it, does not second-guess it, and still computes no price from it. The remaining gap
// — the RULE that turns evidence into a price — is EP-3's, and it is reported as
// ReasonEntryEvaluationNotImplemented rather than disguised as missing data.
//
// The two ways of being absent are kept apart exactly as RegimeEvidence keeps them:
//
//	Status != AVAILABLE   nobody handed us a semantic
//	Semantic == UNKNOWN   the upstream layer looked and could not say which kind of entry
type EntrySemanticEvidence struct {
	Status   Availability  `json:"status"`
	Semantic EntrySemantic `json:"semantic,omitempty"`
}

// Usable reports whether an entry semantic a policy can be consulted about is in hand.
func (e EntrySemanticEvidence) Usable() bool {
	return e.Status.OK() && e.Semantic.Valid() && e.Semantic.Resolved()
}

// ThesisStanding is whether the scanner's PRIMARY recommendation contradicts the entry thesis.
//
// # EP-6D, and what "the thesis" is here
//
// The user decided (2026-09-14) that the scanner's WatchAction PULLBACK_BUY / BREAKOUT_BUY IS
// the existing BUY thesis — EntrySemantic already carries it. On top of it, a stock whose
// primary Action contradicts that thesis (SELL, REDUCE, TAKE PROFIT, STOP LOSS — all four by the
// user's decisions at and after the EP-6D review) shows NO entry at all.
// This field is how that fact reaches the decision layer. It is NOT a second thesis, NOT a
// grade, and NOT a copy of the scanner's Action vocabulary: entryplan may not import
// internal/scanner, so the adapter on the scanner side reduces the Action to one of the values
// below and this package never sees the Action string itself.
//
// # Three readings, and the zero value is NOT "fine"
//
//	NOT_CONTRADICTED   the adapter classified the Action and it does not contradict the entry
//	CONTRADICTED       the adapter classified the Action and it does (SELL / REDUCE /
//	                   TAKE PROFIT / STOP LOSS)
//	"" or unknown      nobody classified it — MISSING, never read as NOT_CONTRADICTED
//
// How DecideStatus reads it, once an entry shape is resolved (S0 comes first — no shape, no
// thesis to contradict):
//
//	CONTRADICTED       S1 → NO_VALID_ENTRY on every input, and ComputePlan publishes no
//	                   executable price (no zone, ceiling, stop, targets or risk/reward).
//	"" or unknown      only the two BUY_NOW arms (S10, S14) read it: BUY_NOW becomes
//	                   INSUFFICIENT_DATA, and nothing else changes.
//	NOT_CONTRADICTED   no effect.
type ThesisStanding string

const (
	// ThesisNotContradicted — the primary Action was classified and does not contradict
	// the entry thesis.
	ThesisNotContradicted ThesisStanding = "NOT_CONTRADICTED"
	// ThesisContradicted — the primary Action was classified and contradicts the entry thesis.
	ThesisContradicted ThesisStanding = "CONTRADICTED"
)

// AllThesisStandings is the registry, pinned as literals by golden_test.go.
var AllThesisStandings = []ThesisStanding{ThesisNotContradicted, ThesisContradicted}

// Valid reports whether the standing is a declared, classified value. "" is not.
func (t ThesisStanding) Valid() bool {
	for _, k := range AllThesisStandings {
		if k == t {
			return true
		}
	}
	return false
}

// Snapshot is everything entryplan is allowed to know.
//
// # Why there is no candle series in here
//
// The obvious shape for this input would be []fetcher.Candle plus an index. It is rejected,
// because with a series in hand, reading the future costs ONE off-by-one: candles[i+1],
// len(candles)-1 where the caller meant the AsOf bar, a slice bound computed from the wrong
// end. Nothing in a unit test distinguishes a lookahead bug from a correct result — both
// produce plausible prices — and a backtest built on it reports the lookahead as skill.
//
// So the Snapshot carries POINT VALUES ALREADY REDUCED AS OF AsOf. The caller does the
// slicing, once, where the AsOf bar is in scope and the mistake is visible; this package is
// then structurally incapable of the error, rather than merely tested against it. Future work
// items that need more evidence add more point fields — never the series.
//
// The zero value is a fully-unavailable snapshot, which is the honest default: every
// sub-struct's zero Status is "" and "" is not AVAILABLE, so a caller who forgets to populate
// something gets INSUFFICIENT_DATA rather than a plan built on zeros.
type Snapshot struct {
	// Symbol is the exchange ticker, e.g. "2330". Required.
	Symbol string `json:"symbol"`
	// AsOf is the session this evidence describes, YYYY-MM-DD. Required, and validated as a
	// date: it is the only thing standing between "the plan is about 2026-09-09" and "the
	// plan is about whatever string the caller had lying around".
	AsOf string `json:"as_of"`

	// PriceBasis is the basis of the snapshot's QUOTE stream — the series CurrentPrice was
	// read from, and the basis any level evidence that does not declare its own is on.
	// Required and never defaulted; see PriceBasis.
	PriceBasis PriceBasis `json:"price_basis"`

	// AdjustmentAge is fetcher.LastAdjustmentAge's answer for this symbol's series. Evidence
	// only under EP-1. See AdjustmentAge for why 63 and not 65.
	AdjustmentAge AdjustmentAge `json:"adjustment_age"`

	// CurrentPrice is the last price at AsOf. The one piece of evidence with no substitute:
	// every output of this package is a price relative to it.
	CurrentPrice PriceObservation `json:"current_price"`

	// ATR is volatility evidence. Optional under EP-1 by construction — see ATREvidence.
	ATR ATREvidence `json:"atr"`

	// Regime is the market state the plan is being made inside.
	Regime RegimeEvidence `json:"regime"`

	// EntrySemantic is the kind of entry the upstream layer is proposing — the evidence that
	// makes required census item #4 fillable. See EntrySemanticEvidence.
	//
	// EP-2 ADDED THIS ONE FIELD AND STOPPED, on the rule that "a field nothing reads is a
	// field whose meaning is decided by the first accident that reads it". EP-3b is the item
	// that reads the rest, so the rest arrives below — and every one of them is read by
	// ComputeIdealEntryZone or ComputeMaxChase in the same commit that declares it.
	EntrySemantic EntrySemanticEvidence `json:"entry_semantic"`

	// ── EP-3b: the level evidence a zone can be centred on ───────────────────────────
	//
	// PriceObservation, not float64, for every one of them. A 0 would be a real price and
	// "the MA20 is 0" is a sentence with no true reading — the same argument CurrentPrice
	// makes — and each carries its OWN PriceBasis because this repo computes MA20 on the raw
	// close (internal/indicator/calculator.go:33) while other evidence in the same snapshot
	// may have come through fetcher.PriceForCalc. See PriceBasis.
	//
	// They are EVIDENCE, not a ranking. Which of them ends up being the zone's centre is
	// decided by SelectCenter, and the rule is written down in one place there.

	// MA20 is the 20-session moving average at AsOf. A pullback candidate.
	MA20 PriceObservation `json:"ma20"`
	// MA60 is the 60-session moving average at AsOf. A pullback candidate.
	MA60 PriceObservation `json:"ma60"`
	// BaseLow is the low of the consolidation base the current move started from. A pullback
	// candidate.
	BaseLow PriceObservation `json:"base_low"`
	// PivotHigh is the level a breakout entry is measured from — the high of the base being
	// broken. THE ONLY breakout centre this package accepts; see SelectCenter.
	PivotHigh PriceObservation `json:"pivot_high"`

	// PreviousClose is the previous session's close, and it exists for exactly ONE
	// computation: pricerule.LimitUpPrice, which needs the previous close and nothing else.
	//
	// IT IS NOT A SUBSTITUTE FOR CurrentPrice AND CurrentPrice IS NOT A SUBSTITUTE FOR IT.
	// A daily price limit is a band around the PREVIOUS session's reference price; computing
	// it from today's price yields a ceiling that moves with the thing it is supposed to
	// bound. ComputeMaxChase therefore returns no chase limit at all when this is absent, and the
	// zone survives — the two availabilities are independent, and the test that holds that
	// line is TestChaseAndZoneAvailabilityAreIndependent.
	//
	// The CALLER's contract from pricerule.LimitUpPrice applies to what is put here: on an
	// ex-dividend, ex-rights or post-capital-reduction session the exchange computes the band
	// from the ADJUSTED REFERENCE PRICE (除權息參考價 / 減資換發參考價), not from the raw
	// previous close. This package cannot check that — it has no calendar and no
	// corporate-action feed — so the clamp travels with a caveat saying what it assumed.
	PreviousClose PriceObservation `json:"previous_close"`

	// AvgVolume20 is the 20-session average volume, in shares.
	//
	// A PLACEHOLDER, AND NOT AN EXECUTION GATE. It is recorded on the evidence census so a
	// reader can see the liquidity behind a chase limit, and NOTHING in this package reads it
	// for a decision: there is no backtested "minimum volume at which a chase fills" contract
	// in this repo, and inventing a threshold here would be a fabricated tradeability claim
	// wearing an evidence row's clothes. TestLiquidityIsRecordedAndNeverGates asserts that
	// removing it changes nothing but its own row.
	//
	// A plain *float64 rather than a PriceObservation because it is not a price and has no
	// PriceBasis: adjustment factors restate prices, not share counts.
	AvgVolume20 *float64 `json:"avg_volume_20,omitempty"`

	// ── EP-4: the risk side of the plan ──────────────────────────────────────────────
	//
	// TWO FIELDS, and each is read by ComputeInvalidation or ComputeTargets in the same
	// commit that declares it — EP-2's rule that "a field nothing reads is a field whose
	// meaning is decided by the first accident that reads it".
	//
	// NOTE WHAT IS NOT HERE. There is no swing low, no "recent low", no lowest close of the
	// last N sessions, and there is no candle series to derive one from (see the Snapshot
	// doc). A swing low is the field through which lookahead enters this package: the
	// obvious implementation reads backwards from the end of a slice, one off-by-one
	// produces a low from AFTER the AsOf bar, and the resulting stop is both plausible and
	// unfalsifiable in a unit test. EP-4's structural levels are the ones the caller has
	// already reduced.

	// BaseLowKind says whether BaseLow is a consolidation base low or today's single-bar
	// low. REQUIRED for BaseLow to be usable as an INVALIDATION and irrelevant to its use
	// as a zone centre; see BaseLowKind for the measurement that makes the distinction
	// necessary.
	BaseLowKind BaseLowKind `json:"base_low_kind,omitempty"`

	// Valuation is the projected BASE-scenario target price and its model-applicability
	// verdict. OPTIONAL SANITY, NEVER A DEPENDENCY: its absence must not remove Target2 —
	// see ComputeTargets. It is a PROJECTION because entryplan must not import
	// internal/valuation, which reaches `os` and `net/http`; see ValuationSuitability.
	Valuation ValuationEvidence `json:"valuation"`

	// ── EP-6D: the thesis standing ───────────────────────────────────────────────────
	//
	// Thesis is whether the scanner's primary Action contradicts the entry thesis. Read by
	// DecideStatus at S1 (CONTRADICTED) and at the two BUY_NOW arms (unclassified), and NOT
	// counted by the confidence census (see ComputePlan). The zero value "" means NOT
	// CLASSIFIED and can never produce BUY_NOW — see ThesisStanding.
	Thesis ThesisStanding `json:"thesis,omitempty"`
}

// asOfLayout is the repo's session-date format, shared with the analysis-history archive and
// the valuation snapshots.
const asOfLayout = "2006-01-02"

// WellFormed reports whether the snapshot can be interpreted at all, independently of whether
// its evidence is available.
//
// The split matters and is the same one technical.Config.Validate makes: a snapshot with no
// symbol and no parsable date is not a stock with missing data, it is not a stock. Producing
// a per-evidence diagnosis for it would dress up a caller bug as a market condition.
//
// time.Parse is a FORMAT CHECK. This package reads no clock; see the purity test.
func (s Snapshot) WellFormed() bool {
	if s.Symbol == "" || s.AsOf == "" {
		return false
	}
	if _, err := time.Parse(asOfLayout, s.AsOf); err != nil {
		return false
	}
	return true
}

// ── EP-4: the two projected inputs the risk side of the plan needs ────────────────────

// BaseLowKind says WHAT KIND OF LEVEL Snapshot.BaseLow actually is.
//
// # This field exists because the producer of BaseLow answers two different questions with it
//
// Measured in internal/scanner/consolidation.go, not assumed:
//
//	analyzeConsolidation, days >= 3:  pivotHigh, baseLow := windowHighLow(candles, baseStart, n-1)
//	                                  → the LOW OF THE BASE WINDOW, over at least three sessions
//	analyzeConsolidation, days <  3:  c.Bucket = NoBase; c.BaseLow = latest.Low
//	                                  → THE LOW OF TODAY'S SINGLE BAR
//
// Both arrive in the same float64 field, both are plausible support prices, and no downstream
// check can tell them apart — the second one is finite, positive, below the market and on the
// tick grid exactly like the first.
//
// # Why the difference decides whether it may be an INVALIDATION
//
// An invalidation is the price at which the REASON FOR THE TRADE stops being true (see
// Invalidation). A consolidation base low is such a price: buyers defended that level for
// three or more sessions, so a break of it says the supply/demand balance the setup rests on
// has changed. Today's single-session low says nothing of the kind — it is one session's worst
// tick, it is re-drawn every morning, and price trades through it inside perfectly healthy
// pullbacks. A "thesis has failed" level derived from it declares failure on noise.
//
// So ComputeInvalidation refuses a BaseLow that is not a CONSOLIDATION_BASE, and it refuses an
// UNSTATED kind rather than assuming the good case — the same treatment PriceBasis gives "":
// defaulting would assert the very fact the field was added to establish, on behalf of the
// caller who did not know there was a choice.
//
// # It does NOT change EP-3's zone
//
// SelectCenter still accepts BaseLow as a zone CENTRE whatever its kind, and that is not an
// oversight. The two questions are different: "where is price likely to be bought" tolerates a
// short-term low (it is a real place trades happened), while "where is the idea wrong" does
// not. EP-3's IdealEntryZone semantics are frozen and TestTheBaseLowKindDoesNotMoveTheZone
// holds that line from the other side.
type BaseLowKind string

const (
	// BaseLowConsolidationBase — the low of a consolidation window of at least three
	// sessions: scanner.Consolidation with Bucket != NO_BASE. A STRUCTURAL level.
	BaseLowConsolidationBase BaseLowKind = "CONSOLIDATION_BASE"

	// BaseLowLatestBar — the low of the most recent single bar, which is what
	// analyzeConsolidation puts in BaseLow when it finds NO_BASE. NOT a structural level.
	BaseLowLatestBar BaseLowKind = "LATEST_BAR_LOW"

	// BaseLowKindUnknown — the producer looked and cannot say which of the two this is.
	//
	// A STATE, kept distinct from "" (the caller never said) on the evidence row, for the
	// reason RegimeEvidence keeps its two absences apart. Neither is structural.
	BaseLowKindUnknown BaseLowKind = "UNKNOWN"
)

// AllBaseLowKinds is the registry, checked from both ends by the stop tests.
var AllBaseLowKinds = []BaseLowKind{
	BaseLowConsolidationBase,
	BaseLowLatestBar,
	BaseLowKindUnknown,
}

// KnownBaseLowKind reports whether a kind is in the registry.
func KnownBaseLowKind(k BaseLowKind) bool {
	for _, known := range AllBaseLowKinds {
		if known == k {
			return true
		}
	}
	return false
}

// Structural reports whether a base low of this kind may be used as a thesis-invalidation
// level.
//
// ONLY CONSOLIDATION_BASE. "" and UNKNOWN are refused rather than assumed, and LATEST_BAR_LOW
// is refused because it is a single session's low; see the type doc.
func (k BaseLowKind) Structural() bool { return k == BaseLowConsolidationBase }

// ValuationSuitability is whether a P/E-based target price is a MEANINGFUL number for this
// company at all.
//
// A PROJECTION of internal/valuation.Suitability, carried as this package's own type and
// value-compatible with it. internal/entryplan MUST NOT import internal/valuation: measured
// with `go list -deps ./internal/valuation`, that package reaches `os` and `net/http`, and
// this one is a pure domain package whose purity is asserted as a source fact
// (TestTheEntryPlanLayerStaysPure bans the whole `os.` prefix). So the two numbers EP-4 needs
// — the BASE scenario's target price and the suitability verdict — arrive already reduced, the
// same way the regime, the ATR and the previous close do.
//
// The five values are valuation's five, verbatim, because a projection that renamed them would
// force every caller to write a mapping table and the first wrong row would be invisible.
type ValuationSuitability string

const (
	// SuitabilitySuitable — P/E is structurally usable for this company.
	SuitabilitySuitable ValuationSuitability = "SUITABLE"
	// SuitabilityConditional — usable with stated conditions.
	SuitabilityConditional ValuationSuitability = "CONDITIONAL"
	// SuitabilityWeak — usable but poorly supported.
	SuitabilityWeak ValuationSuitability = "WEAK"
	// SuitabilityUnsuitable — a P/E target is NOT a meaningful number for this company.
	//
	// The ONE value that blocks the ceiling. valuation's own doc is emphatic that UNSUITABLE
	// is not "bad company", not "overpriced" and not "sell": it is a statement that the MODEL
	// does not apply, and a number produced by a model that does not apply must not cap a
	// target that was derived from price structure.
	SuitabilityUnsuitable ValuationSuitability = "UNSUITABLE"
	// SuitabilityInsufficientData — the model could not be evaluated. Distinct from
	// UNSUITABLE: one is "we could not look", the other is "we looked, and P/E is the wrong
	// instrument here".
	SuitabilityInsufficientData ValuationSuitability = "INSUFFICIENT_DATA"
)

// AllValuationSuitabilities is the registry, checked from both ends by the target tests.
var AllValuationSuitabilities = []ValuationSuitability{
	SuitabilitySuitable,
	SuitabilityConditional,
	SuitabilityWeak,
	SuitabilityUnsuitable,
	SuitabilityInsufficientData,
}

// KnownValuationSuitability reports whether a verdict is in the registry.
func KnownValuationSuitability(s ValuationSuitability) bool {
	for _, known := range AllValuationSuitabilities {
		if known == s {
			return true
		}
	}
	return false
}

// PermitsCeiling reports whether a target price carrying this verdict may cap Target2.
//
// TWO conditions, not one. `!= UNSUITABLE` is the rule EP-4 was specified with, and the
// KNOWN-CODE requirement is added on the same argument PriceBasis.Supported makes about "": a
// verdict this package cannot interpret — the zero value, or a code from a newer valuation
// rule set — is a caller who did not say, and reading "did not say" as "not unsuitable" would
// apply a ceiling on grounds nobody stated. INSUFFICIENT_DATA does permit it, deliberately:
// the gate is on the MODEL'S APPLICABILITY, and "we could not evaluate applicability" is not a
// finding that P/E is the wrong instrument. What protects that case is that the ceiling only
// ever LOWERS a target and is always recorded as having been applied.
func (s ValuationSuitability) PermitsCeiling() bool {
	return KnownValuationSuitability(s) && s != SuitabilityUnsuitable
}

// ValuationEvidence is the projected valuation target and its own availability.
//
// It carries the BASE scenario only. The BEAR and BULL scenarios are deliberately absent: a
// target ceiling is a sanity bound and the base case is the one a reader is asked to defend,
// while capping a structural target with a BULL number would make the bound non-binding by
// construction and capping it with a BEAR number would replace the plan's own arithmetic with
// the valuation model's worst case.
type ValuationEvidence struct {
	// Status is the valuation layer's own availability for this symbol.
	Status Availability `json:"status"`

	// BaseTargetPrice is valuation.TargetPrice's BASE scenario TargetPrice, in price units.
	// A POINTER for the reason every other price here is one: the scenario itself carries
	// *float64 precisely because "no target could be computed" must not arrive as 0.
	BaseTargetPrice *float64 `json:"base_target_price,omitempty"`

	// Suitability is the projected model-applicability verdict. "" means the caller did not
	// say, which PermitsCeiling refuses.
	Suitability ValuationSuitability `json:"suitability,omitempty"`

	// PriceBasis is the series the target price is expressed on. A P/E target is EPS times a
	// multiple, so it is a RAW-quote-scale number in practice; it is carried and CHECKED
	// rather than assumed, because this package never converts between bases and a ceiling
	// taken off the wrong series is a plausible number with nothing behind it.
	PriceBasis PriceBasis `json:"price_basis,omitempty"`
}

// Usable reports whether a target price a ceiling could be computed from is in hand.
//
// It deliberately does NOT check Suitability or the basis: "there is no target", "the model
// does not apply" and "the target is on another series" are three different facts with three
// different trace codes, and folding them together would report a present target as missing.
func (v ValuationEvidence) Usable() bool {
	return v.Status.OK() && v.BaseTargetPrice != nil && finitePositive(*v.BaseTargetPrice)
}
