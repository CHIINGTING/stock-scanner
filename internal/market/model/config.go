package model

// MarketConfig is the centralized Market Dashboard configuration, nested under the scanner
// config as `market:`. Every threshold and weight lives here — never hard-coded in logic —
// so calibration is a config change, not a code change (same discipline as news/R6/R9).
//
// Two-layer gating: Enabled (compute the dashboard at all) and Show (render it). Phase 1
// wires neither into the scanner, so both default false and the scanner is unaffected.
type MarketConfig struct {
	Enabled bool `yaml:"enabled"`
	Show    bool `yaml:"show"`

	// Weights are the per-module relative weights (baseline sums to 100). A missing module
	// is dropped and the rest renormalize, so these need not sum to 100 after Defaulted.
	Weights map[string]float64 `yaml:"weights"`

	// Benchmark names what represents "the market" for price-structure analysis.
	// MVP default is 0050 (two years already in .cache); it is an ETF proxy, never
	// labelled TAIEX. Empty → Benchmark0050.
	Benchmark Benchmark `yaml:"benchmark"`

	Futures   FuturesThresholds   `yaml:"futures"`
	Structure StructureThresholds `yaml:"structure"`

	// Deprecated: Regime holds the Market-Score cutoffs of the pre-Evidence classifier.
	// The regime engine (M3) is structural and reads Structure instead; this field is
	// removed with Classify().
	Regime RegimeThresholds `yaml:"regime"`

	// ConfidenceMinModules: at least this many available modules → MEDIUM confidence, else LOW.
	ConfidenceMinModules int `yaml:"confidence_min_modules"`

	// StorageDir is where market_YYYY-MM-DD.json is written. Empty → "data/market".
	StorageDir string `yaml:"storage_dir"`
}

// FuturesThresholds encode the Foreign Futures module's Trend (by daily change) and Extreme
// Indicator (by net position) cutoffs, in contracts. Baseline values from the spec; these
// are BASELINE, NOT CALIBRATED. Trend/Extreme evaluate top-down, first match wins.
type FuturesThresholds struct {
	// Trend by DailyChange (contracts).
	AggressiveShortChange float64 `yaml:"aggressive_short_change"` // <= this → Aggressive Short
	MoreShortChange       float64 `yaml:"more_short_change"`       // <= this → More Short
	StableChange          float64 `yaml:"stable_change"`           // <= this → Stable
	CoverShortChange      float64 `yaml:"cover_short_change"`      // <= this → Cover Short; above → Aggressive Cover
	// Extreme Indicator by NetPosition (contracts). Evaluated most-negative first.
	ExtremeShortNet float64 `yaml:"extreme_short_net"` // < this → Extreme Short
	HeavyShortNet   float64 `yaml:"heavy_short_net"`   // < this → Heavy Short
	ShortNet        float64 `yaml:"short_net"`         // < this → Short
	LightShortNet   float64 `yaml:"light_short_net"`   // <= this → Light Short; above → Neutral
}

// RegimeThresholds are the Market-Score cutoffs of the LEGACY score-driven classifier.
//
// Deprecated: mapping a score onto a regime is exactly what the approved design forbids —
// score describes directional strength, regime describes structure, and the two can
// legitimately disagree (score 67 while the structure is BULL_PULLBACK). Replaced by
// StructureThresholds; removed when Classify() goes.
type RegimeThresholds struct {
	BullMin         float64 `yaml:"bull_min"`          // >= → Bull Market (or Distribution if deteriorating)
	BullPullbackMin float64 `yaml:"bull_pullback_min"` // >= → Bull Pullback
	BearMax         float64 `yaml:"bear_max"`          // < → Bear Market; between → Sideways
}

// Defaulted returns a copy with every zero-valued knob filled with spec baselines, so a
// minimal (or empty) YAML block still produces a fully specified, runnable engine.
func (c MarketConfig) Defaulted() MarketConfig {
	out := c
	if out.Weights == nil {
		out.Weights = map[string]float64{}
	}
	for name, w := range defaultWeights {
		if _, ok := out.Weights[name]; !ok {
			out.Weights[name] = w
		}
	}
	if out.Benchmark == "" {
		out.Benchmark = Benchmark0050
	}
	out.Futures = out.Futures.withDefaults()
	out.Structure = out.Structure.withDefaults()
	out.Regime = out.Regime.withDefaults()
	if out.ConfidenceMinModules <= 0 {
		out.ConfidenceMinModules = 5
	}
	if out.StorageDir == "" {
		out.StorageDir = "data/market"
	}
	return out
}

// defaultWeights is the approved MVP baseline (sums to 100).
//
// Rationale for the split (all BASELINE, NOT CALIBRATED — M2/M3 validate them against the
// two years of history already in .cache):
//
//   - Price 20 + Breadth 15 = 35 for what the market itself is DOING. It gets the largest
//     share because it is the only dimension with full history on day one, so it is the
//     only one whose weight can be validated immediately. Price leads breadth because
//     breadth is derived from the same universe and is noisier at the edges (its validity
//     depends on how many stocks have enough bars).
//   - ForeignCash 25 — the single institutional series most co-directional with the index.
//   - ForeignFutures 20 — deliberately BELOW the cash weight even though the spec sketch
//     had them equal: futures and cash are two faces of the SAME participant, not
//     independent evidence. At 25+25 a single actor would decide half the score.
//   - Margin 20 — retail leverage, genuinely independent of the foreign flows.
//
// Options / Currency / VIX / SectorCycle carry NO default weight: no official TWSE or
// TAIFEX source exists for them (see the data-source research in the plan), and a module
// with no data must not hold weight it can never earn.
var defaultWeights = map[string]float64{
	ModulePrice:          20,
	ModuleBreadth:        15,
	ModuleForeignCash:    25,
	ModuleForeignFutures: 20,
	ModuleMargin:         20,
}

// Defaulted fills every zero-valued knob with its baseline, for analyzers that receive a
// hand-built threshold set.
func (f FuturesThresholds) Defaulted() FuturesThresholds { return f.withDefaults() }

func (f FuturesThresholds) withDefaults() FuturesThresholds {
	if f.AggressiveShortChange == 0 {
		f.AggressiveShortChange = -3000
	}
	if f.MoreShortChange == 0 {
		f.MoreShortChange = -500
	}
	if f.StableChange == 0 {
		f.StableChange = 500
	}
	if f.CoverShortChange == 0 {
		f.CoverShortChange = 3000
	}
	if f.ExtremeShortNet == 0 {
		f.ExtremeShortNet = -70000
	}
	if f.HeavyShortNet == 0 {
		f.HeavyShortNet = -50000
	}
	if f.ShortNet == 0 {
		f.ShortNet = -30000
	}
	if f.LightShortNet == 0 {
		f.LightShortNet = -10000
	}
	return f
}

func (r RegimeThresholds) withDefaults() RegimeThresholds {
	if r.BullMin == 0 {
		r.BullMin = 65
	}
	if r.BullPullbackMin == 0 {
		r.BullPullbackMin = 55
	}
	if r.BearMax == 0 {
		r.BearMax = 40
	}
	return r
}

// StructureThresholds are the cutoffs of the three-layer structural regime engine.
//
// EVERY VALUE HERE IS A HYPOTHESIS, not a proven constant. They are configurable precisely
// so M2/M3 can sweep them against the ~487 bars of benchmark history and ~1,986 stocks
// already sitting in .cache. Each field names the rule it serves so no number here is a
// magic number.
type StructureThresholds struct {
	// ── data sufficiency ───────────────────────────────────────────────────────────
	// MinBenchmarkBars is the history required before a structure may be called at all;
	// below it the answer is StructureUnknown, never a guess.
	//
	// 120, NOT 200. The longest moving average any decision rule actually reads is MA120
	// (STRONG_UPTREND's close > MA20 > MA60 > MA120, DOWNTREND's close < MA120, and the
	// trendIntact predicate). MA200 is recorded in metrics as context but no rule branches
	// on it, so demanding 200 bars would return UNKNOWN for 80 extra sessions while buying
	// no analytical certainty. Derived quantities that need more history (the 20-session
	// breadth high, the 252-day volatility percentile) degrade INDIVIDUALLY — a missing
	// percentile drops the HighVolatility flag, it does not invalidate the regime.
	MinBenchmarkBars int `yaml:"min_benchmark_bars"`
	// MinBreadthUniverse is the minimum number of stocks with enough history to be counted
	// before breadth is meaningful; below it BreadthQuality is UNKNOWN.
	MinBreadthUniverse int `yaml:"min_breadth_universe"`

	// ── BreadthQuality cutoffs (percent of universe above its own MA20) ───────────
	BreadthHealthyPct   float64 `yaml:"breadth_healthy_pct"`    // >= → HEALTHY
	BreadthNarrowPct    float64 `yaml:"breadth_narrow_pct"`     // < this AND near a high → NARROWING
	BreadthCollapsePct  float64 `yaml:"breadth_collapse_pct"`   // < → COLLAPSING
	BreadthDropCoolPP   float64 `yaml:"breadth_drop_cool_pp"`   // pp off the 20-session high → COOLING
	BreadthDropNarrowPP float64 `yaml:"breadth_drop_narrow_pp"` // pp off the 20-session high → NARROWING

	// ── price predicates (percent below the 60-day closing high) ──────────────────
	// NearHighDDPct: <= this is "near the 60-day closing high".
	//
	// SHARED BY THREE RULES — changing it moves all of them:
	//   §3.2 STRONG_UPTREND — the proximity requirement of the top structure
	//   §3.3 NARROWING      — the nearHigh input that makes it a divergence test
	//   §5.3 R7             — BULL from a plain UPTREND
	// If calibration ever needs them separated, add a distinct field (e.g.
	// StrongUptrendDDPct) rather than compromising on one value for three jobs.
	NearHighDDPct    float64 `yaml:"near_high_dd_pct"`
	ResilientDDPct   float64 `yaml:"resilient_dd_pct"`   // <= → priceResilient (or still above MA60)
	CorrectionMinPct float64 `yaml:"correction_min_pct"` // orderlyCorrection lower bound
	CorrectionMaxPct float64 `yaml:"correction_max_pct"` // orderlyCorrection upper bound
	// CapitulationDayPct: a single session worse than this disqualifies "orderly".
	CapitulationDayPct float64 `yaml:"capitulation_day_pct"`

	// ── failed highs ──────────────────────────────────────────────────────────────
	// A failed high = touched the 60-day high, then closed below MA20 within
	// FailedHighWindowDays. FailedHighsMin of them inside FailedHighLookbackDays is the
	// "repeated failure near highs" signature of distribution.
	FailedHighWindowDays   int `yaml:"failed_high_window_days"`
	FailedHighLookbackDays int `yaml:"failed_high_lookback_days"`
	FailedHighsMin         int `yaml:"failed_highs_min"`

	// MASlopeDays is the lookback used to decide whether a moving average is "rising":
	// MA(today) > MA(MASlopeDays sessions ago). The spec's decision table says "MA60 上揚",
	// which is a SLOPE, not yesterday-vs-today — a one-day tick in a 60-day average is
	// noise. Five sessions = one trading week. Layer 1's DOWNTREND / STRONG_UPTREND /
	// UPTREND rows and the trendIntact predicate all branch on this, so it is a decision
	// threshold and belongs here rather than buried in the analyzer.
	//
	// Note the resulting asymmetry: the spec's "MA60 下彎" is implemented as NOT rising,
	// which also admits a perfectly flat MA60. Deliberate — a flat 60-day average in a
	// market that has broken its long-term floor is not evidence of health.
	MASlopeDays int `yaml:"ma_slope_days"`

	// ── orthogonal flags ──────────────────────────────────────────────────────────
	VolPercentile   float64 `yaml:"vol_percentile"`    // realized vol20 >= this percentile → HighVolatility
	VolLookbackDays int     `yaml:"vol_lookback_days"` // window the percentile is built from
	CrashRet20Pct   float64 `yaml:"crash_ret20_pct"`   // benchmark 20d return <= this → crash candidate
}

// Defaulted fills every zero-valued knob with its baseline. Analyzers call this on entry
// so a caller that constructs StructureThresholds{} by hand still gets the documented
// behaviour instead of a table of zeros that would classify everything as UNKNOWN.
func (t StructureThresholds) Defaulted() StructureThresholds { return t.withDefaults() }

func (t StructureThresholds) withDefaults() StructureThresholds {
	if t.MinBenchmarkBars == 0 {
		t.MinBenchmarkBars = 120
	}
	if t.MinBreadthUniverse == 0 {
		t.MinBreadthUniverse = 500
	}
	if t.BreadthHealthyPct == 0 {
		t.BreadthHealthyPct = 55
	}
	if t.BreadthNarrowPct == 0 {
		t.BreadthNarrowPct = 45
	}
	if t.BreadthCollapsePct == 0 {
		t.BreadthCollapsePct = 30
	}
	if t.BreadthDropCoolPP == 0 {
		t.BreadthDropCoolPP = 10
	}
	if t.BreadthDropNarrowPP == 0 {
		t.BreadthDropNarrowPP = 20
	}
	if t.NearHighDDPct == 0 {
		t.NearHighDDPct = 3
	}
	if t.ResilientDDPct == 0 {
		t.ResilientDDPct = 5
	}
	if t.CorrectionMinPct == 0 {
		t.CorrectionMinPct = 3
	}
	if t.CorrectionMaxPct == 0 {
		t.CorrectionMaxPct = 15
	}
	if t.CapitulationDayPct == 0 {
		t.CapitulationDayPct = 4
	}
	if t.FailedHighWindowDays == 0 {
		t.FailedHighWindowDays = 3
	}
	if t.FailedHighLookbackDays == 0 {
		t.FailedHighLookbackDays = 20
	}
	if t.FailedHighsMin == 0 {
		t.FailedHighsMin = 2
	}
	if t.MASlopeDays == 0 {
		t.MASlopeDays = 5
	}
	if t.VolPercentile == 0 {
		t.VolPercentile = 80
	}
	if t.VolLookbackDays == 0 {
		t.VolLookbackDays = 252
	}
	if t.CrashRet20Pct == 0 {
		t.CrashRet20Pct = -8
	}
	return t
}
