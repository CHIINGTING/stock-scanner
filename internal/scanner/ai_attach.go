package scanner

import (
	"context"
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/ai"
	"github.com/deep-huang/stock-scanner/internal/institution"
)

// R11 AI attach — the shadow explanation POST-PASS.
//
// It runs AFTER computeRocket, so every score, action, probability and sort position is
// already final by the time this file executes. Nothing here writes to any of them; the only
// field it touches is WatchlistEntry.AI, which is deliberately a dedicated field OUTSIDE
// ShadowSignals — the C6b guardrail scoring reads ShadowSignals and nothing else, so this
// data cannot reach the scoring path even by accident.
//
// It also cannot fail the scan. AttachAI returns nothing: a missing key, a timeout, a 429, a
// 5xx or malformed model output all end up as a non-OK Status on the entry (or as no field
// at all), and the caller carries on.

// aiActionable is the scanner's own notion of a live candidate, reused rather than
// reinvented. Only these actions are worth spending a request on: WAIT and
// REMOVE_FROM_WATCHLIST have nothing to interpret, and TAKE_PROFIT is an exit note on a
// position rather than a candidate read.
func aiActionable(a WatchAction) bool {
	switch a {
	case ActPrepare, ActBreakoutBuy, ActPullbackBuy, ActWatchClose:
		return true
	}
	return false
}

// AttachAI runs the AI analyzer over the watchlist and attaches the results in place.
//
// enabled=false short-circuits before any network work; every entry simply keeps a nil AI
// field, and the report renders exactly as it did before this feature existed.
func AttachAI(ctx context.Context, entries []WatchlistEntry, cfg ai.Config, enabled bool,
	market AIMarketContext, logf func(string, ...any)) {

	if !enabled || len(entries) == 0 {
		return
	}
	analyzer := ai.NewAnalyzer(cfg, logf)

	candidates := make([]ai.Candidate, 0, len(entries))
	for i := range entries {
		candidates = append(candidates, ai.Candidate{
			Evidence:   buildAIEvidence(&entries[i], market),
			Actionable: aiActionable(entries[i].WatchAction),
			Rank:       entries[i].RocketScore,
		})
	}

	results := analyzer.Analyze(ctx, enabled, candidates)
	for i := range entries {
		if r, ok := results[entries[i].A.Symbol]; ok {
			entries[i].AI = r
		}
	}
}

// AIMarketContext is the top-level market read, passed in rather than imported so the
// scanner package keeps no dependency on internal/market.
//
// Available is what makes this struct honest. Without it, "the market dashboard has not run"
// and "the market is unremarkable" reach the model as the same thing — an absent field — and
// a model that cannot tell those apart will reason about the second when the truth is the
// first. R13-M3 makes the distinction explicit: a scan with no snapshot says so.
type AIMarketContext struct {
	// Available is false when no snapshot covered this trading date. Regime and Score are
	// then meaningless and must not be read.
	Available bool
	Regime    string
	Score     float64
}

// Market data status values sent to the model. Two states, both explicit — there is no
// third "probably fine".
const (
	MarketDataAvailable   = "AVAILABLE"
	MarketDataUnavailable = "UNAVAILABLE"
)

// buildAIEvidence projects one entry onto the evidence contract.
//
// It only COPIES values the scanner already computed — no indicator is recalculated here,
// and nothing absent is filled in. A field the scanner did not produce is left at its zero
// value, omitempty drops it from the JSON, and the model is instructed to treat a missing
// field as missing information rather than an invitation to guess.
func buildAIEvidence(e *WatchlistEntry, market AIMarketContext) ai.Evidence {
	a := e.A
	ev := ai.Evidence{
		Symbol:         a.Symbol,
		Name:           a.Name,
		Price:          a.Close,
		Stage:          string(e.RocketStage),
		WatchAction:    string(e.WatchAction),
		RocketScore:    e.RocketScore,
		ExplosionProb:  e.ExplosionProb,
		TechnicalScore: a.Score,
		Sector:         e.Sector,
		SectorFlow:     e.SectorFlowDir,
		SectorStage:    string(e.SectorStage),
		RiskLabel:      e.RiskLabel,
		RiskWarning:    e.RiskWarning,
		Reasons:        e.Reasons,
	}
	// Market context, stated either way. An unavailable market is a FACT about this scan, so
	// it is sent rather than omitted; omitting it would leave the model to assume.
	if market.Available {
		ev.MarketDataStatus = MarketDataAvailable
		ev.MarketRegime = market.Regime
		ev.MarketScore = market.Score
	} else {
		ev.MarketDataStatus = MarketDataUnavailable
	}
	if e.Consol.Days > 0 {
		ev.Consolidation = fmt.Sprintf("%s／整理 %d 天", e.Consol.Bucket, e.Consol.Days)
	}
	// ONLY MA20 is sent, because MA20 is the only moving average StockAnalysis carries.
	// MA60/MA120 exist elsewhere in the codebase but not on this struct, and computing them
	// here would mean the AI layer deriving an indicator the scanner did not — exactly what
	// this feature is not allowed to do. The distance is a division on a value already held,
	// which is presentation rather than analysis.
	ev.MA20DistancePct = pctFrom(a.Close, a.MA20)
	ev.MA20Trend = a.MA20Trend
	ev.VolumeRatio = a.VolumeRatio
	ev.PriceVolumeSignal = a.PriceVolumeSignal
	ev.PriceChangePct = a.PriceChangePct
	ev.PriceChangeATR = a.PriceChangeATR
	// MoveUnknown means there was no previous close to measure against; sending it would
	// invite the model to reason about a category that only means "we could not look".
	if a.PriceMove != "" && a.PriceMove != MoveUnknown {
		ev.PriceMove = a.PriceMove
		ev.PriceVolumeState = a.PriceVolumeState
	}

	// Institutional flow, with the same missing-vs-zero discipline. A nil view means the
	// layer did not run or had no snapshot for this date; that is NOT "the institutions did
	// not trade this stock", and the two must never arrive as the same evidence.
	if e.Institution == nil {
		ev.InstitutionalStatus = InstitutionDataUnavailable
	} else {
		ev.InstitutionalStatus = InstitutionDataAvailable
		ev.Institutional = aiInstitutionalLines(e.Institution)
	}
	return ev
}

// Institutional data status values. Same two-state discipline as the market context.
const (
	InstitutionDataAvailable   = "AVAILABLE"
	InstitutionDataUnavailable = "UNAVAILABLE"
)

// aiInstitutionalLines renders the three institutional legs as short factual lines.
//
// Only today's net and the 5-day sum are sent: they are what the report itself shows, and a
// longer window would invite the model to narrate a trend the scanner has not asserted.
// Units stay in 張 exactly as the institution layer stores them — no conversion, no rounding
// into a different scale that a reader might misattribute to the scanner.
func aiInstitutionalLines(v *institution.StockChipView) []string {
	if v == nil {
		return nil
	}
	legs := []struct {
		name string
		s    institution.LegStats
	}{
		{"外資", v.Foreign}, {"投信", v.Trust}, {"自營商", v.Dealer},
	}
	var out []string
	for _, l := range legs {
		if l.s.TodayNet == 0 && l.s.Sum5d == 0 {
			// A leg that netted exactly zero on both windows is indistinguishable, in this
			// projection, from a leg the snapshot never covered — so it is omitted rather
			// than reported as "0". The AVAILABLE status above already tells the model the
			// LAYER had data; silence on one leg means "not asserted", not "zero".
			continue
		}
		out = append(out, fmt.Sprintf("%s 今日 %+d 張、近5日 %+d 張", l.name, l.s.TodayNet, l.s.Sum5d))
	}
	return out
}

// pctFrom is the percent distance of price from a moving average, 0 when the MA is absent.
func pctFrom(price, ma float64) float64 {
	if ma <= 0 || price <= 0 {
		return 0
	}
	return (price/ma - 1) * 100
}
