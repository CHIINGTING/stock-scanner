// Package research is the R13 adapter between the existing scanner and the SQLite research
// store. It converts a scan's canonical IN-MEMORY results into snapshots and evidence.
//
// It computes nothing. Every value written here was already derived by the scanner, the
// indicator package, or one of the shadow layers; this package only projects them. Two rules
// follow from that and are the whole point of the file:
//
//   - a fact the scanner did not produce is written as NO ROW, never as a zero. That keeps
//     the store's MISSING ≠ NEUTRAL contract intact all the way from the indicator to the
//     agent that later reads it;
//   - nothing is derived to fill a gap. MA60 and MA120 do not exist per stock anywhere in
//     this repo (StockAnalysis carries MA20 only; MA120 exists solely for the market index),
//     so they are simply absent here. Deriving them would create a second, differently
//     calibrated indicator set — exactly what R13 is forbidden to do.
//
// This layer is also strictly WRITE-ONLY with respect to the scanner: it takes results by
// value, returns evidence, and touches no score, action, ranking or report.
package research

import (
	"fmt"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/news"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
	"github.com/deep-huang/stock-scanner/internal/technical"
)

// Source labels name the layer a value came from, so a surprising number in the store can be
// traced back to the code that produced it without guessing.
const (
	srcAnalysis    = "scanner.StockAnalysis"
	srcRocket      = "scanner.computeRocket"
	srcConsol      = "scanner.Consolidation"
	srcShadow      = "scanner.ShadowSignals"
	srcBias        = "scanner.BiasView"
	srcTrendExt    = "scanner.TrendExtensionView"
	srcHeat        = "scanner.SectorHeatView"
	srcRotation    = "scanner.SectorRotation"
	srcInstitution = "institution.StockChipView"
	srcCandle      = "candlestick.Result"
	srcNews        = "news.StockNewsView"
	srcMarket      = "market.Snapshot"
	srcTechnical   = "technical.Result"
)

// MarketContext is the top-level market read at scan time.
//
// It is passed in as data rather than imported, so this package keeps no dependency on
// internal/market — the same discipline scanner.AIMarketContext already uses. When the
// market dashboard has not run, every field is empty and no market evidence is written.
type MarketContext struct {
	Regime     string
	Score      *float64
	Confidence *float64
	AsOfDate   string
}

// collector accumulates evidence rows.
//
// The method names are the contract. `num` writes unconditionally and is only for values
// where 0 is a real reading (a score, a return, a slope). `level` refuses 0, because a price
// or a moving average of exactly zero means the scanner had nothing, not that the stock is
// worthless. Getting that distinction wrong is precisely how a store fills up with confident
// zeros, so it is encoded in the API rather than left to each call site.
type collector struct{ items []store.Evidence }

// num records a value where zero is meaningful.
func (c *collector) num(cat, key string, v float64, unit, src string) {
	c.items = append(c.items, store.Num(cat, key, v, unit, src))
}

// level records a price-like value, treating 0 as "not produced".
func (c *collector) level(cat, key string, v float64, unit, src string) {
	if v == 0 {
		return
	}
	c.num(cat, key, v, unit, src)
}

// ptr records an optional value; nil leaves no row. This is the common case for every
// shadow layer, all of which use pointers for exactly this reason.
func (c *collector) ptr(cat, key string, v *float64, unit, src string) {
	if v == nil {
		return
	}
	c.num(cat, key, *v, unit, src)
}

// count records an integer measurement (zero is meaningful — zero consecutive buy days is a
// fact, not a gap).
func (c *collector) count(cat, key string, v int64, unit, src string) {
	c.num(cat, key, float64(v), unit, src)
}

// text records a label; an empty label leaves no row.
func (c *collector) text(cat, key, v, src string) {
	if v == "" {
		return
	}
	c.items = append(c.items, store.Text(cat, key, v, src))
}

// flag records a boolean as TRUE/FALSE text. Booleans are always written: false is a real
// observation here, not an absence — absence is expressed by not calling this at all.
func (c *collector) flag(cat, key string, v bool, src string) {
	c.text(cat, key, boolText(v), src)
}

func boolText(v bool) string {
	if v {
		return "TRUE"
	}
	return "FALSE"
}

// buildAnalysisEvidence projects the fields every scanned stock has, whichever tab it came
// from. Market- and portfolio-tab stocks carry only this much, because the shadow layers are
// attached to watchlist entries and nowhere else.
func buildAnalysisEvidence(c *collector, a scanner.StockAnalysis) {
	c.level(store.CategoryTechnical, "close", a.Close, "TWD", srcAnalysis)
	c.count(store.CategoryTechnical, "volume", a.Volume, "shares", srcAnalysis)
	c.count(store.CategoryTechnical, "avg_volume_20", a.AvgVolume20, "shares", srcAnalysis)
	c.level(store.CategoryTechnical, "volume_ratio", a.VolumeRatio, "x", srcAnalysis)

	// MA20 is the ONLY moving average StockAnalysis holds. See the package comment for why
	// MA60 / MA120 are absent rather than derived.
	c.level(store.CategoryTechnical, "ma20", a.MA20, "TWD", srcAnalysis)
	c.text(store.CategoryTechnical, "ma20_trend", a.MA20Trend, srcAnalysis)

	c.level(store.CategoryTechnical, "rsi", a.RSI, "", srcAnalysis)
	c.level(store.CategoryTechnical, "atr", a.ATR, "TWD", srcAnalysis)
	c.level(store.CategoryTechnical, "kdj_k", a.KDJK, "", srcAnalysis)
	c.level(store.CategoryTechnical, "kdj_d", a.KDJD, "", srcAnalysis)
	c.level(store.CategoryTechnical, "kdj_j", a.KDJJ, "", srcAnalysis)
	c.level(store.CategoryTechnical, "bb_upper", a.BBUpper, "TWD", srcAnalysis)
	c.level(store.CategoryTechnical, "bb_lower", a.BBLower, "TWD", srcAnalysis)
	c.level(store.CategoryTechnical, "bb_width", a.BBWidth, "%", srcAnalysis)

	c.num(store.CategoryTechnical, "technical_score", float64(a.Score), "", srcAnalysis)
	c.num(store.CategoryTechnical, "bfp_points", float64(a.BFPPoints), "of 5", srcAnalysis)
	c.text(store.CategoryTechnical, "price_volume_signal", a.PriceVolumeSignal, srcAnalysis)
	// The magnitude beside the direction. `num` (not `level`) because a genuine 0.0% session
	// is a real reading; a stock with no previous close carries PriceMove == MoveUnknown and
	// is filtered out below rather than stored as a confident zero.
	if a.PriceMove != "" && a.PriceMove != scanner.MoveUnknown {
		c.num(store.CategoryTechnical, "price_change_pct", a.PriceChangePct, "%", srcAnalysis)
		c.text(store.CategoryTechnical, "price_move", a.PriceMove, srcAnalysis)
		c.text(store.CategoryTechnical, "price_volume_state", a.PriceVolumeState, srcAnalysis)
		// Zero means the ATR was unavailable, so it stays out rather than reading as
		// "the move was worth zero ATRs".
		c.level(store.CategoryTechnical, "price_change_atr", a.PriceChangeATR, "ATR", srcAnalysis)
	}
	c.text(store.CategoryTechnical, "limit_status", a.LimitStatus, srcAnalysis)

	c.level(store.CategoryTechnical, "stop_loss", a.StopLoss, "TWD", srcAnalysis)
	c.level(store.CategoryTechnical, "target_1", a.Target1, "TWD", srcAnalysis)
	c.level(store.CategoryTechnical, "target_2", a.Target2, "TWD", srcAnalysis)
}

// buildWatchlistEvidence projects everything a watchlist entry carries beyond the base
// analysis: the rocket decision, the consolidation base, and each shadow layer that was
// enabled. A layer that was off contributes nothing at all.
func buildWatchlistEvidence(c *collector, e scanner.WatchlistEntry, rot *scanner.SectorRotation, mkt MarketContext) {
	buildAnalysisEvidence(c, e.A)

	// ── rocket decision + base ──────────────────────────────────────────────────────
	c.num(store.CategoryTechnical, "rocket_score", float64(e.RocketScore), "", srcRocket)
	c.text(store.CategoryTechnical, "rocket_stage", string(e.RocketStage), srcRocket)
	c.text(store.CategoryTechnical, "watch_action", string(e.WatchAction), srcRocket)
	c.text(store.CategoryTechnical, "explosion_probability", e.ExplosionProb, srcRocket)
	c.text(store.CategoryTechnical, "days_to_watch", e.DaysToWatch, srcRocket)
	c.level(store.CategoryTechnical, "breakout_price", e.BreakoutPrice, "TWD", srcRocket)
	c.level(store.CategoryTechnical, "support_price", e.SupportPrice, "TWD", srcRocket)

	if e.Consol.Days > 0 {
		c.num(store.CategoryTechnical, "consolidation_days", float64(e.Consol.Days), "days", srcConsol)
		c.text(store.CategoryTechnical, "consolidation_bucket", string(e.Consol.Bucket), srcConsol)
		c.num(store.CategoryTechnical, "consolidation_range_pct", e.Consol.RangePct, "%", srcConsol)
		c.num(store.CategoryTechnical, "base_quality_score", e.Consol.BaseQualityScore, "", srcConsol)
		c.flag(store.CategoryTechnical, "near_previous_high", e.Consol.NearPreviousHigh, srcConsol)
	}

	// ── risk ────────────────────────────────────────────────────────────────────────
	// The risk agent's domain. Kept in its own category so it can be handed over without
	// the technical rows, the same way every other specialist is scoped.
	c.text(store.CategoryRisk, "risk_label", e.RiskLabel, srcRocket)
	c.text(store.CategoryRisk, "risk_warning", e.RiskWarning, srcRocket)
	c.text(store.CategoryRisk, "mtf_risk_note", e.MTFRiskNote, srcShadow)

	buildShadowEvidence(c, e)
	buildBiasEvidence(c, e)
	buildTrendExtEvidence(c, e)
	buildTechnicalEvidence(c, e.Technical)
	buildCandleEvidence(c, e)
	buildInstitutionEvidence(c, e.Institution)
	buildSectorEvidence(c, e, rot)
	buildNewsEvidence(c, e.News)
	buildMarketEvidence(c, mkt)
}

// buildShadowEvidence projects the C6a shadow signals. Each sub-struct is nil unless its
// flag is on, and each carries its own Computed guard for "enabled but not enough history".
func buildShadowEvidence(c *collector, e scanner.WatchlistEntry) {
	s := e.Shadow
	if s == nil {
		return
	}
	if r := s.RS; r != nil && r.Computed {
		// Relative strength is the sector agent's material, not the technical agent's.
		c.num(store.CategorySector, "rs_rank_percentile", r.RSRankPercentile, "pct", srcShadow)
		c.num(store.CategorySector, "rs_return_pct", r.RSReturnPct, "%", srcShadow)
	}
	if nh := s.NewHigh; nh != nil && nh.Computed {
		c.num(store.CategoryTechnical, "distance_from_52w_high_pct", nh.DistanceFrom52wHighPct, "%", srcShadow)
		c.num(store.CategoryTechnical, "new_high_score", nh.NewHighScore, "", srcShadow)
		c.flag(store.CategoryTechnical, "near_52w_high", nh.Near52wHigh, srcShadow)
		c.flag(store.CategoryTechnical, "breakout_watch", nh.BreakoutWatch, srcShadow)
		// A period flag is only meaningful when that window had enough history; an
		// unvalidated window is absent rather than false.
		if nh.H20Valid {
			c.flag(store.CategoryTechnical, "new_high_20d", nh.H20, srcShadow)
		}
		if nh.H60Valid {
			c.flag(store.CategoryTechnical, "new_high_60d", nh.H60, srcShadow)
		}
		if nh.H250Valid {
			c.flag(store.CategoryTechnical, "new_high_250d", nh.H250, srcShadow)
		}
	}
	if v := s.VCP; v != nil && v.Computed {
		c.flag(store.CategoryTechnical, "vcp_valid", v.Valid, srcShadow)
		c.num(store.CategoryTechnical, "vcp_quality_score", v.QualityScore, "", srcShadow)
		c.num(store.CategoryTechnical, "vcp_contractions", float64(v.ContractionCount), "", srcShadow)
		c.text(store.CategoryTechnical, "vcp_grade", string(v.Grade), srcShadow)
	}
	if m := s.Momentum; m != nil && m.Computed {
		c.text(store.CategoryTechnical, "momentum_flow", string(m.Flow), srcShadow)
		c.num(store.CategoryTechnical, "momentum_score", m.Score, "", srcShadow)
		c.flag(store.CategoryTechnical, "momentum_divergence", m.Divergence, srcShadow)
		c.text(store.CategoryTechnical, "structure_trend", m.StructureTrend, srcShadow)
	}
	if mtf := s.MultiTimeframe; mtf != nil {
		c.num(store.CategoryTechnical, "mtf_alignment_score", mtf.AlignmentScore, "", srcShadow)
		c.text(store.CategoryTechnical, "mtf_alignment", mtf.AlignmentLabel, srcShadow)
		c.text(store.CategoryTechnical, "mtf_signal_strength", mtf.SignalStrength, srcShadow)
		c.text(store.CategoryTechnical, "mtf_long_term_filter", mtf.LongTermFilter, srcShadow)
	}
}

// buildBiasEvidence projects R10-1. Every window is a pointer: an insufficient history
// leaves that window with no row rather than a 0% deviation.
func buildBiasEvidence(c *collector, e scanner.WatchlistEntry) {
	b := e.Bias
	if b == nil {
		return
	}
	c.ptr(store.CategoryTechnical, "bias5", b.Bias5, "%", srcBias)
	c.ptr(store.CategoryTechnical, "bias10", b.Bias10, "%", srcBias)
	c.ptr(store.CategoryTechnical, "bias20", b.Bias20, "%", srcBias)
	c.ptr(store.CategoryTechnical, "bias60", b.Bias60, "%", srcBias)
	// BIAS risk is chase/oversold RISK, not a trade signal — it belongs to the risk agent.
	c.text(store.CategoryRisk, "bias_risk", b.Risk, srcBias)
}

// buildTrendExtEvidence projects R12: the MA slopes and the per-stock BIAS percentile.
//
// These are the ONLY MA-slope values that exist per stock in this repo, and they exist only
// when enable_trend_extension is on. With the flag off there are no slope rows — the agents
// then see that the measurement is unavailable, which is the truth.
func buildTrendExtEvidence(c *collector, e scanner.WatchlistEntry) {
	t := e.TrendExt
	if t == nil {
		return
	}
	c.text(store.CategoryTechnical, "trend_ext_state", string(t.State), srcTrendExt)
	c.ptr(store.CategoryTechnical, "ma20_slope_5d", t.MA20Slope5D, "%/day", srcTrendExt)
	c.ptr(store.CategoryTechnical, "ma60_slope_10d", t.MA60Slope10D, "%/day", srcTrendExt)
	c.ptr(store.CategoryTechnical, "bias20_percentile", t.Bias20Percentile, "pct", srcTrendExt)
	// Heat travels with the sector rows, not the technical ones.
	buildHeatEvidence(c, t.Heat)
}

// buildHeatEvidence projects the R12 sector heat panel, including its denominators.
//
// A heat reading below the member floor publishes its STATUS and its counts but no score —
// mirroring ComputeSectorHeat, which deliberately leaves the components at zero rather than
// producing a precise-looking number from two stocks.
func buildHeatEvidence(c *collector, h *scanner.SectorHeatView) {
	if h == nil {
		return
	}
	c.text(store.CategorySector, "heat_status", h.Status, srcHeat)
	c.num(store.CategorySector, "heat_member_count", float64(h.MemberCount), "", srcHeat)
	c.num(store.CategorySector, "heat_valid_member_count", float64(h.ValidMemberCount), "", srcHeat)
	if h.Status != scanner.SectorHeatOK {
		return
	}
	c.num(store.CategorySector, "heat", h.Heat, "", srcHeat)
	c.num(store.CategorySector, "heat_breadth", h.Breadth, "%", srcHeat)
	c.num(store.CategorySector, "heat_relative_momentum", h.RelativeMomentum, "pct", srcHeat)
	c.num(store.CategorySector, "heat_participation", h.Participation, "%", srcHeat)
	c.num(store.CategorySector, "heat_volume_confirmation", h.VolumeConfirmation, "%", srcHeat)
	c.num(store.CategorySector, "heat_coverage", h.Coverage, "%", srcHeat)
	c.num(store.CategorySector, "heat_median_return_20", h.MedianReturn20, "%", srcHeat)
}

// buildTechnicalEvidence projects R14 into the EXISTING R13 evidence table. There is no
// second persistence system: the same scan_runs → stock_snapshots → evidence chain carries
// it, so a technical reading is traceable to its run, stock, timestamp and parameters like
// every other fact.
//
// Two disciplines are enforced here rather than left to the reader:
//
//   - the PARAMETERS are stored alongside the values. A row saying "ADX 31.8" is worthless
//     in two years if nobody can tell whether it was a 14- or a 20-period ADX, and R14's
//     whole purpose is to make historical rows answerable later.
//   - a non-OK indicator contributes its STATUS and nothing else. Writing 0 for an ADX that
//     could not be computed would put a fabricated "no trend" into the very dataset meant to
//     decide whether ADX is worth anything.
//
// Values are machine-readable numbers and labels, never formatted display strings.
func buildTechnicalEvidence(c *collector, r *technical.Result) {
	if r == nil {
		return
	}

	if v := r.ADX; v != nil {
		c.text(store.CategoryTechnical, "tech_adx_status", string(v.Status), srcTechnical)
		if v.Status.OK() {
			c.num(store.CategoryTechnical, "tech_adx_period", float64(v.Period), "bars", srcTechnical)
			c.num(store.CategoryTechnical, "tech_adx", v.ADX, "", srcTechnical)
			c.num(store.CategoryTechnical, "tech_plus_di", v.PlusDI, "", srcTechnical)
			c.num(store.CategoryTechnical, "tech_minus_di", v.MinusDI, "", srcTechnical)
			c.text(store.CategoryTechnical, "tech_adx_direction", v.Direction, srcTechnical)
			c.text(store.CategoryTechnical, "tech_adx_strength", v.Strength, srcTechnical)
		}
	}

	if v := r.RSI; v != nil {
		c.text(store.CategoryTechnical, "tech_rsi_status", string(v.Status), srcTechnical)
		if v.Status.OK() {
			c.num(store.CategoryTechnical, "tech_rsi_period", float64(v.Period), "bars", srcTechnical)
			c.num(store.CategoryTechnical, "tech_rsi", v.Value, "", srcTechnical)
			c.text(store.CategoryTechnical, "tech_rsi_zone", v.Zone, srcTechnical)
			c.flag(store.CategoryTechnical, "tech_rsi_rising", v.Rising, srcTechnical)
			c.flag(store.CategoryTechnical, "tech_rsi_falling", v.Falling, srcTechnical)
		}
	}

	if v := r.MACD; v != nil {
		c.text(store.CategoryTechnical, "tech_macd_status", string(v.Status), srcTechnical)
		if v.Status.OK() {
			c.num(store.CategoryTechnical, "tech_macd_fast", float64(v.Fast), "bars", srcTechnical)
			c.num(store.CategoryTechnical, "tech_macd_slow", float64(v.Slow), "bars", srcTechnical)
			c.num(store.CategoryTechnical, "tech_macd_signal_period", float64(v.Signal), "bars", srcTechnical)
			c.num(store.CategoryTechnical, "tech_macd", v.MACD, "", srcTechnical)
			c.num(store.CategoryTechnical, "tech_macd_signal", v.SignalVal, "", srcTechnical)
			c.num(store.CategoryTechnical, "tech_macd_histogram", v.Histogram, "", srcTechnical)
			c.text(store.CategoryTechnical, "tech_macd_cross", v.Cross, srcTechnical)
			c.text(store.CategoryTechnical, "tech_macd_momentum", v.Momentum, srcTechnical)
		}
	}

	if v := r.Keltner; v != nil {
		c.text(store.CategoryTechnical, "tech_keltner_status", string(v.Status), srcTechnical)
		if v.Status.OK() {
			c.num(store.CategoryTechnical, "tech_keltner_ema_period", float64(v.EMAPeriod), "bars", srcTechnical)
			c.num(store.CategoryTechnical, "tech_keltner_atr_period", float64(v.ATRPeriod), "bars", srcTechnical)
			c.num(store.CategoryTechnical, "tech_keltner_multiplier", v.Multiplier, "x", srcTechnical)
			c.level(store.CategoryTechnical, "tech_keltner_middle", v.Middle, "TWD", srcTechnical)
			c.level(store.CategoryTechnical, "tech_keltner_upper", v.Upper, "TWD", srcTechnical)
			c.level(store.CategoryTechnical, "tech_keltner_lower", v.Lower, "TWD", srcTechnical)
			c.num(store.CategoryTechnical, "tech_keltner_atr", v.ATR, "TWD", srcTechnical)
			c.num(store.CategoryTechnical, "tech_keltner_width_pct", v.WidthPct, "%", srcTechnical)
			c.text(store.CategoryTechnical, "tech_keltner_position", v.Position, srcTechnical)
			c.text(store.CategoryTechnical, "tech_keltner_channel", v.Channel, srcTechnical)
		}
	}

	if v := r.Pivot; v != nil {
		c.text(store.CategoryTechnical, "tech_pivot_status", string(v.Status), srcTechnical)
		if v.Status.OK() {
			// The basis date is what proves no look-ahead: the levels came from the session
			// BEFORE the one being evaluated, and the row says which.
			c.text(store.CategoryTechnical, "tech_pivot_basis_date", v.BasisDate, srcTechnical)
			c.level(store.CategoryTechnical, "tech_pivot", v.Pivot, "TWD", srcTechnical)
			c.level(store.CategoryTechnical, "tech_pivot_r1", v.R1, "TWD", srcTechnical)
			c.level(store.CategoryTechnical, "tech_pivot_r2", v.R2, "TWD", srcTechnical)
			c.level(store.CategoryTechnical, "tech_pivot_r3", v.R3, "TWD", srcTechnical)
			c.level(store.CategoryTechnical, "tech_pivot_s1", v.S1, "TWD", srcTechnical)
			c.level(store.CategoryTechnical, "tech_pivot_s2", v.S2, "TWD", srcTechnical)
			c.level(store.CategoryTechnical, "tech_pivot_s3", v.S3, "TWD", srcTechnical)
			c.text(store.CategoryTechnical, "tech_pivot_nearest", v.NearestLevel, srcTechnical)
			c.num(store.CategoryTechnical, "tech_pivot_distance_pct", v.DistancePct, "%", srcTechnical)
			c.text(store.CategoryTechnical, "tech_pivot_location", v.Location, srcTechnical)
		}
	}

	// The aggregated context — the part an agent reads first. Signals are stored as one
	// delimited label list rather than one row per signal: they are a set describing a
	// single reading, and splitting them would make "which signals fired together" a
	// reassembly job for every consumer.
	if v := r.Context; v != nil {
		c.text(store.CategoryTechnical, "tech_context_direction", v.Direction, srcTechnical)
		c.text(store.CategoryTechnical, "tech_context_trend_strength", v.TrendStrength, srcTechnical)
		c.text(store.CategoryTechnical, "tech_context_momentum", v.Momentum, srcTechnical)
		c.text(store.CategoryTechnical, "tech_context_volatility", v.Volatility, srcTechnical)
		c.text(store.CategoryTechnical, "tech_context_price_location", v.PriceLocation, srcTechnical)
		c.text(store.CategoryTechnical, "tech_context_signals", strings.Join(v.Signals, "|"), srcTechnical)
	}
}

// buildCandleEvidence projects R10-2. Only the TOP signal is recorded: the analyzer already
// sorted by confidence, so "the strongest pattern on the latest bar" is a selection, not a
// judgement. The full list stays in the report.
func buildCandleEvidence(c *collector, e scanner.WatchlistEntry) {
	r := e.Candlestick
	if r == nil {
		return
	}
	c.text(store.CategoryTechnical, "candlestick_status", r.Status, srcCandle)
	if len(r.Signals) == 0 {
		return
	}
	top := r.Signals[0]
	c.text(store.CategoryTechnical, "candlestick_pattern", string(top.Type), srcCandle)
	c.text(store.CategoryTechnical, "candlestick_direction", string(top.Direction), srcCandle)
	c.num(store.CategoryTechnical, "candlestick_confidence", top.Confidence, "0-1", srcCandle)
	c.num(store.CategoryTechnical, "candlestick_strength", float64(top.Strength), "1-5", srcCandle)
	c.flag(store.CategoryTechnical, "candlestick_context_matched", top.ContextMatched, srcCandle)
}

// buildInstitutionEvidence projects R10-1's three-legs-plus-total chip view.
func buildInstitutionEvidence(c *collector, v *institution.StockChipView) {
	if v == nil {
		return
	}
	c.text(store.CategoryInstitution, "as_of_date", v.AsOfDate, srcInstitution)
	// Completeness first: every number below is only as trustworthy as this flag.
	c.flag(store.CategoryInstitution, "data_complete", v.Completeness.DataComplete, srcInstitution)
	c.num(store.CategoryInstitution, "observed_days", float64(v.Completeness.ObservedDays), "days", srcInstitution)
	c.num(store.CategoryInstitution, "expected_days", float64(v.Completeness.ExpectedDays), "days", srcInstitution)
	if v.TotalMismatch {
		c.flag(store.CategoryInstitution, "total_mismatch", true, srcInstitution)
	}

	for _, leg := range []struct {
		name  string
		stats institution.LegStats
	}{
		{"foreign", v.Foreign},
		{"trust", v.Trust},
		{"dealer", v.Dealer},
		{"total", v.Total},
	} {
		p := leg.name + "_"
		c.count(store.CategoryInstitution, p+"today_net", leg.stats.TodayNet, "shares", srcInstitution)
		c.count(store.CategoryInstitution, p+"sum_3d", leg.stats.Sum3d, "shares", srcInstitution)
		c.count(store.CategoryInstitution, p+"sum_5d", leg.stats.Sum5d, "shares", srcInstitution)
		c.count(store.CategoryInstitution, p+"sum_10d", leg.stats.Sum10d, "shares", srcInstitution)
		c.count(store.CategoryInstitution, p+"sum_20d", leg.stats.Sum20d, "shares", srcInstitution)
		c.num(store.CategoryInstitution, p+"consecutive_buy_days", float64(leg.stats.ConsecutiveBuyDays), "days", srcInstitution)
		c.num(store.CategoryInstitution, p+"consecutive_sell_days", float64(leg.stats.ConsecutiveSellDays), "days", srcInstitution)
		c.text(store.CategoryInstitution, p+"transition", leg.stats.Transition, srcInstitution)
		// nil when same-day volume was unavailable (§12) — absent, not 0%.
		c.ptr(store.CategoryInstitution, p+"net_buy_ratio", leg.stats.NetBuyRatio, "%", srcInstitution)
		c.flag(store.CategoryInstitution, p+"streak_complete", leg.stats.StreakComplete, srcInstitution)
	}
}

// buildSectorEvidence projects the stock's sector linkage and, when the sector was ranked
// this run, its rotation panel.
func buildSectorEvidence(c *collector, e scanner.WatchlistEntry, rot *scanner.SectorRotation) {
	c.text(store.CategorySector, "sector", e.Sector, srcRotation)
	if !e.HasSector {
		return
	}
	c.text(store.CategorySector, "sector_flow_dir", e.SectorFlowDir, srcRotation)
	c.text(store.CategorySector, "sector_mid_label", e.SectorMidLabel, srcRotation)
	c.text(store.CategorySector, "sector_stage", string(e.SectorStage), srcRotation)

	if rot == nil {
		return
	}
	c.num(store.CategorySector, "sector_score", rot.Score, "", srcRotation)
	c.num(store.CategorySector, "sector_opp_score", rot.OppScore, "", srcRotation)
	c.num(store.CategorySector, "sector_rel_strength", rot.RelStrength, "", srcRotation)
	c.num(store.CategorySector, "sector_new_high_ratio", rot.NewHighRatio, "", srcRotation)
	c.num(store.CategorySector, "sector_breakout_ratio", rot.BreakoutRatio, "", srcRotation)
	c.num(store.CategorySector, "sector_vol_expansion", rot.VolExpansion, "", srcRotation)
	c.num(store.CategorySector, "sector_ma60_slope", rot.MA60Slope, "", srcRotation)
	c.num(store.CategorySector, "sector_avg_return_20", rot.AvgReturn20, "%", srcRotation)
	c.num(store.CategorySector, "sector_money_flow", rot.MoneyFlow, "-1..1", srcRotation)
	c.num(store.CategorySector, "sector_short_term_flow_score", rot.ShortTermFlowScore, "", srcRotation)
	c.text(store.CategorySector, "sector_short_term_flow_stage", rot.ShortTermFlowStage, srcRotation)
	c.num(store.CategorySector, "sector_up_ratio", rot.UpRatio, "%", srcRotation)
	c.num(store.CategorySector, "sector_above_ma60_ratio", rot.AboveMA60Ratio, "%", srcRotation)
	c.num(store.CategorySector, "sector_trend_strength", rot.TrendStrength, "", srcRotation)
	c.text(store.CategorySector, "sector_trend_label", rot.TrendLabel, srcRotation)
	// Heat is a component of the rotation entry (R12). When trend extension is off it is
	// nil here, and buildTrendExtEvidence contributed nothing either.
	buildHeatEvidence(c, rot.Heat)
}

// buildNewsEvidence projects the Phase-1 news view.
//
// Only counts and the single strongest stock-level signal are recorded. The strongest is
// chosen by Strength, tie-broken by Confidence — a SELECTION among classified signals, not a
// re-scoring of them. Nothing here converts a news polarity into a trade direction; that
// mapping is explicitly not this layer's to make.
func buildNewsEvidence(c *collector, v *news.StockNewsView) {
	if v == nil {
		return
	}
	c.flag(store.CategoryNews, "computed", v.Computed, srcNews)
	c.num(store.CategoryNews, "stock_item_count", float64(len(v.StockItems)), "", srcNews)
	c.num(store.CategoryNews, "sector_item_count", float64(len(v.SectorItems)), "", srcNews)
	c.flag(store.CategoryNews, "divergence", v.Divergence, srcNews)

	top, ok := strongestSignal(v.StockItems)
	if !ok {
		return
	}
	c.text(store.CategoryNews, "top_signal", string(top.Signal), srcNews)
	c.num(store.CategoryNews, "top_strength", float64(top.Strength), "0-10", srcNews)
	c.num(store.CategoryNews, "top_confidence", float64(top.Confidence), "0-10", srcNews)
	c.flag(store.CategoryNews, "top_conflict", top.Conflict, srcNews)
	if !top.PublishedAt.IsZero() {
		c.text(store.CategoryNews, "top_published_at", top.PublishedAt.UTC().Format("2006-01-02"), srcNews)
	}
}

// strongestSignal picks the highest-Strength signal, tie-broken by Confidence, then by the
// earliest PublishedAt so the choice is deterministic for a fixed input.
func strongestSignal(items []news.NewsSignal) (news.NewsSignal, bool) {
	if len(items) == 0 {
		return news.NewsSignal{}, false
	}
	best := items[0]
	for _, s := range items[1:] {
		switch {
		case s.Strength > best.Strength:
			best = s
		case s.Strength < best.Strength:
		case s.Confidence > best.Confidence:
			best = s
		case s.Confidence < best.Confidence:
		case s.PublishedAt.Before(best.PublishedAt):
			best = s
		}
	}
	return best, true
}

// buildMarketEvidence projects the top-level market read. It is per-stock context, not a
// per-stock measurement: every snapshot in a run carries the same regime, which is what lets
// R13-M7 slice agent accuracy by regime without a second join.
func buildMarketEvidence(c *collector, m MarketContext) {
	c.text(store.CategoryMarket, "regime", m.Regime, srcMarket)
	c.ptr(store.CategoryMarket, "score", m.Score, "0-100", srcMarket)
	c.ptr(store.CategoryMarket, "confidence", m.Confidence, "0-1", srcMarket)
	c.text(store.CategoryMarket, "as_of_date", m.AsOfDate, srcMarket)
}

// countByCategory tallies a projection into a running total.
func countByCategory(into map[string]int, items []store.Evidence) {
	for _, e := range items {
		into[e.Category]++
	}
}

// describeCounts renders a category tally for logs.
func describeCounts(byCat map[string]int) string {
	return fmt.Sprintf("technical=%d institution=%d sector=%d news=%d market=%d risk=%d",
		byCat[store.CategoryTechnical], byCat[store.CategoryInstitution],
		byCat[store.CategorySector], byCat[store.CategoryNews],
		byCat[store.CategoryMarket], byCat[store.CategoryRisk])
}
