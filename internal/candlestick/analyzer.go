package candlestick

import "sort"

// analyzer.go — Layer 8: Analyzer orchestration. Runs the full pipeline for the LATEST
// bar-position of a chronological (oldest-first) candle history:
//
//	Raw OHLC → Validation → Metrics → Geometry (single + two-bar) → Context → Interpret →
//	Score → Result
//
// Pure, deterministic, never panics. Returns an empty (no-signal) Result when the history
// is empty or the latest bar is degenerate/invalid.

// Analyzer status — lets a consumer (validator) tell "analyzer ran but found nothing" from
// "analyzer never ran / disabled" (SPEC §4/§5). A non-nil Result always carries one of these.
const (
	StatusOK         = "OK"          // ≥1 signal
	StatusNoPattern  = "NO_PATTERN"  // ran on a valid bar, no pattern
	StatusInvalidBar = "INVALID_BAR" // latest bar invalid/degenerate → no geometry
	StatusNoData     = "NO_DATA"     // no candles
)

// Result is the analyzer output for the latest bar-position (SPEC §10).
type Result struct {
	AsOfDate string          `json:"asOfDate"` // latest candle date (YYYY-MM-DD); "" if unset
	Status   string          `json:"status"`   // StatusOK | StatusNoPattern | StatusInvalidBar | StatusNoData
	Context  MarketContext   `json:"context"`
	Signals  []PatternSignal `json:"signals"` // sorted by Confidence desc (stable); empty if none
}

// Analyze runs the pipeline. stage is injected (may be ""); th is defaulted internally.
func Analyze(candles []Candle, stage Stage, th Thresholds) Result {
	th = th.Defaulted()
	var res Result
	n := len(candles)
	if n == 0 {
		res.Status = StatusNoData
		return res
	}

	latest := candles[n-1]
	if !latest.Date.IsZero() {
		res.AsOfDate = latest.Date.Format("2006-01-02")
	}

	// Context is built from the whole history (+ injected Stage) and is returned even when the
	// latest bar itself yields no geometry.
	res.Context = BuildContext(candles, stage, th)

	// Metrics for the latest bar. A degenerate/invalid bar → no geometry, no signals.
	m, err := ComputeMetrics(latest)
	if err != nil {
		res.Status = StatusInvalidBar
		return res
	}

	singleGeo := DetectSingleBar(m, th)

	// Two-bar geometry needs the previous bar; skip if absent/invalid.
	var twoGeo []Geometry
	var prevMetrics *Metrics
	if n >= 2 {
		if pm, perr := ComputeMetrics(candles[n-2]); perr == nil {
			pmCopy := pm
			prevMetrics = &pmCopy
			twoGeo = DetectTwoBar(pm, m)
		}
	}

	signals := Interpret(singleGeo, twoGeo, res.Context)

	// Score each signal (two-bar signals get the prev metrics for their quality checks).
	for i := range signals {
		var prev *Metrics
		if isTwoBar(signals[i].Type) {
			prev = prevMetrics
		}
		signals[i] = ScoreSignal(signals[i], m, prev, res.Context, th)
	}

	// Sort by Confidence desc; SliceStable preserves Interpret's two-bar-first order on ties.
	sort.SliceStable(signals, func(i, j int) bool {
		return signals[i].Confidence > signals[j].Confidence
	})
	res.Signals = signals
	if len(signals) == 0 {
		res.Status = StatusNoPattern
	} else {
		res.Status = StatusOK
	}
	return res
}

// isTwoBar reports whether a PatternType is a two-candle pattern.
func isTwoBar(t PatternType) bool {
	switch t {
	case PatternBullishEngulfing, PatternBearishEngulfing, PatternPiercingLine, PatternDarkCloudCover:
		return true
	default:
		return false
	}
}
