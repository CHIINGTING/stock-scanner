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
// scanner package keeps no dependency on internal/market. Both fields are optional; when the
// market dashboard has not run they stay empty and simply do not appear in the prompt.
type AIMarketContext struct {
	Regime string
	Score  float64
}

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
		MarketRegime:   market.Regime,
		MarketScore:    market.Score,
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

	if e.Institution != nil {
		ev.Institutional = aiInstitutionalLines(e.Institution)
	}
	return ev
}

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
			continue // nothing observed for this leg; say nothing rather than "0"
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
