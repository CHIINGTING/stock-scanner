package study

import (
	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/recon"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Observation is ONE reconstructed stock-session, everything the study reads from it, and the
// provenance every number on it has to be qualified by.
//
// It is the unit the whole study is built on, and it deliberately carries the SIGNAL-DAY
// facts (status, semantic, policy, published prices) rather than re-deriving them later: a
// segment computed from a re-derivation is a segment of something other than the plan.
type Observation struct {
	Symbol     string `json:"symbol"`
	SignalAsOf string `json:"signal_as_of"`

	Status   entryplan.EntryStatus   `json:"status"`
	Semantic entryplan.EntrySemantic `json:"semantic,omitempty"`
	// Policy is the published Plan policy name from the SIGNAL-DAY plan. "" when the regime
	// was not a model.Regime value at all — which is NOT the same as PolicyUnresolved, the
	// policy UNKNOWN does have (internal/entryplan/trace.go:141-143).
	Policy entryplan.PolicyName `json:"policy,omitempty"`

	Regime              model.Regime                          `json:"regime,omitempty"`
	RegimeProvenance    entryplanbacktest.RegimeProvenance    `json:"regime_provenance"`
	ValuationProvenance entryplanbacktest.ValuationProvenance `json:"valuation_provenance"`

	SignalClose float64 `json:"signal_close"`

	ZoneHigh     *float64 `json:"zone_high,omitempty"`
	ZoneLow      *float64 `json:"zone_low,omitempty"`
	MaxChase     *float64 `json:"max_chase,omitempty"`
	Invalidation *float64 `json:"invalidation,omitempty"`
	Target1      *float64 `json:"target_1,omitempty"`
	Target2      *float64 `json:"target_2,omitempty"`

	// Stratum is the adjustment stratum. Nothing is deleted on account of it; it segments.
	Stratum Stratum `json:"stratum"`

	// Reasons are the plan's own reason codes, copied. They are the only way a reader can
	// tell WHY the §31 MA60 answer is what it is: a level-reason count of zero is a fact
	// about where plans fail, not evidence that MA60 does not matter.
	Reasons []entryplan.Reason `json:"reasons,omitempty"`

	// MA60Block is the §31 counterfactual label. It is a COLUMN, never an input: no baseline
	// plan on this observation was computed with MA60.
	MA60Block MA60Label `json:"ma60_block,omitempty"`

	// bars is the symbol's FULL series and the index of the signal bar in it. Unexported: an
	// observation is a value the report reads, and a slice of every candle on it would be
	// serialized into every output file.
	bars     []fetcher.Candle
	sessions entryplanbacktest.SymbolSessions
	sigIdx   int
}

// Bars exposes the series for the fill/outcome step within this package's callers.
func (o *Observation) Bars() []fetcher.Candle { return o.bars }

// Sessions exposes the symbol's session identity.
func (o *Observation) Sessions() entryplanbacktest.SymbolSessions { return o.sessions }

// ObserveSession projects one reconstructed session's plans into observations.
//
// READ-ONLY. It copies the numbers it needs off each plan and mutates nothing; the allowlist
// entry in internal/scanner/entryplan_symbol_guard_test.go says so and
// TestTheResearchReadersNeverMutateAPlan proves it.
//
// It drops nothing for being uninteresting. A plan with no zone is still an observation — it
// is the denominator of every coverage line in EP-9 §8 — and the arms decide for themselves
// whether they had an instruction to place.
func ObserveSession(res recon.SessionResult, full *recon.Cache, regime model.Regime,
	prov entryplanbacktest.RegimeProvenance) []Observation {

	out := make([]Observation, 0, len(res.Entries))
	for i := range res.Entries {
		e := &res.Entries[i]
		p := e.EntryPlan
		if p == nil {
			continue
		}
		sym := full.Symbol(e.A.Symbol)
		if sym == nil {
			continue
		}
		idx, ok := sym.IndexOf(res.Date)
		if !ok {
			continue
		}
		o := Observation{
			Symbol: e.A.Symbol, SignalAsOf: res.Date,
			Status:   p.Status,
			Semantic: recon.PlanSemantic(p),
			Regime:   regime, RegimeProvenance: prov,
			// EP-6G's limitation, measured in Phase 2a on 700,881 plans: every plan that
			// reached the Target2 step reported VALUATION_UNAVAILABLE, because
			// valuation.LoadHistory filters on the archive DIRECTORY name and the three
			// dated directories that exist are all newer than every session this study can
			// grade. It is a fact about the archive, not about the stock.
			ValuationProvenance: entryplanbacktest.ValuationUnavailableHistorically,
			SignalClose:         e.A.Close,
			bars:                sym.Data.Candles,
			sessions:            sym,
			sigIdx:              idx,
		}
		if p.EntryTrace != nil {
			o.Policy = p.EntryTrace.Policy.Name
		}
		if p.IdealEntry != nil {
			lo, hi := p.IdealEntry.Low, p.IdealEntry.High
			o.ZoneLow, o.ZoneHigh = &lo, &hi
		}
		if p.MaxChasePrice != nil {
			v := *p.MaxChasePrice
			o.MaxChase = &v
		}
		if p.Invalidation != nil {
			v := p.Invalidation.Price
			o.Invalidation = &v
		}
		if p.Target1 != nil {
			v := p.Target1.Price
			o.Target1 = &v
		}
		if p.Target2 != nil {
			v := p.Target2.Price
			o.Target2 = &v
		}
		o.Reasons = append([]entryplan.Reason(nil), p.Reasons...)
		o.Stratum = ClassifyStratum(sym.Data.Candles, idx)
		o.MA60Block = ClassifyMA60Block(p, sym.Data.Candles, idx)
		out = append(out, o)
	}
	return out
}

// ExecutableStatuses is EP-9 §18's HEADLINE POPULATION: the four statuses that assert an entry
// may be taken, now or on a condition.
//
// The user's ruling, transcribed: a headline metric describes plans the layer PUBLISHED AS
// ENTRIES. `INSUFFICIENT_DATA` and `NO_VALID_ENTRY` are not that, even when they carry a zone
// — and 299 of them do, because the zone STEP succeeded and only the VERDICT could not be
// reached. Averaging them into the executable arms answers a different question from the one
// the tables claim to answer.
//
// They are NOT deleted. They are reported in their own named block (Run.NonExecutable) with
// their counts, fill rate and return distribution, and the reason they exist, because 298 of
// them are EVALUATOR-BLOCKED ENTRY CANDIDATES: entryplan's own
// ReasonStatusRequirementNotEvaluated (reasons.go:569-585) says NEAR_SUPPORT and
// ELEVATED_EVIDENCE are "conditions nothing in this repo evaluates", which is why SIDEWAYS and
// DISTRIBUTION cannot produce BUY_NOW today.
//
// # UNKNOWN IS NOT PASS
//
// Nothing here says what the missing evaluator WOULD have decided. These 298 are not would-be
// BUY_NOW plans, not near BUY_NOW, and not rejected BUY_NOW: no evaluation ran, so there is no
// verdict to report in either direction. The named block's outcome figures for them are
// POST_HOC_DISCOVERY — counterfactual results for orders the layer never published, computed
// after the fact, and in NO headline metric. The same wording is used at the other site that
// describes this population, cmd/ep9-study/followups.go.
var ExecutableStatuses = map[entryplan.EntryStatus]bool{
	entryplan.StatusBuyNow:       true,
	entryplan.StatusWaitPullback: true,
	entryplan.StatusWaitBreakout: true,
	entryplan.StatusTooExtended:  true,
}

// ExecutableStatus reports whether this observation belongs to the §18 headline population.
func (o *Observation) ExecutableStatus() bool { return ExecutableStatuses[o.Status] }

// ID is the observation's identity for one arm and wait window.
func (o *Observation) ID(arm Arm, wait int) entryplanbacktest.ObservationID {
	return entryplanbacktest.ObservationID{
		Symbol: o.Symbol, SignalAsOf: o.SignalAsOf,
		RuleVersion: entryplan.RuleVersion,
		Arm:         armIdentity(arm), WaitSessions: wait,
	}
}

// armIdentity maps a study arm onto the Phase 1 execution-arm vocabulary.
//
// The two vocabularies are deliberately not merged. Phase 1's ExecutionArm is the CONTRACT's
// set and it has no SIGNAL_CLOSE member on purpose (a same-bar fill is unexpressible there);
// this study's arm A is a benchmark that is not an EntryPlan fill at all. Mapping A onto
// NEXT_OPEN would be a lie; it gets its own key by taking ZONE_LIMIT's slot only for arms that
// really are limit orders, and A is separated by the wait window being 1 and the row's own Arm
// field. See TestTheIdentityKeepsTheFourArmsApart.
func armIdentity(a Arm) entryplanbacktest.ExecutionArm {
	switch a {
	case ArmZoneLimit:
		return entryplanbacktest.ArmZoneLimit
	case ArmChase:
		return entryplanbacktest.ArmChaseCeiling
	default:
		return entryplanbacktest.ArmNextOpen
	}
}
