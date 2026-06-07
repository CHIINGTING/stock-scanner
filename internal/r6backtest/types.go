// Package r6backtest is a standalone, read-only pullback / crash-low entry
// backtest engine (R6). It imports the scanner's EXPORTED signal APIs but never
// modifies live scanner / report / scoring code. It is decision-support only:
// it emits 候選 / 回測結果 / 勝率 / 風險 / 參考進場區, never order/trade actions.
package r6backtest

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// Stock is one cached symbol with precomputed series for fast as-of access.
type Stock struct {
	Symbol      string
	Name        string
	Sector      string
	IsWatchlist bool

	Candles []fetcher.Candle

	Close, High, Low, Vol         []float64
	MA5, MA10, MA20, MA60, VolMA20 []float64
	RSI14                         []float64 // causal Wilder RSI(14); rsi[i] uses bars <= i
	ATR14                         []float64 // causal ATR(14); atr[i] uses bars <= i

	idxOf map[string]int // date(YYYY-MM-DD) → bar index
}

// StopResult is the outcome of applying one stop policy to one entry. StopBar is
// the absolute bar index where the stop fired (-1 when no stop).
type StopResult struct {
	HitStop   bool
	StopBar   int
	StopPrice float64
	StopDate  time.Time
	Reason    string
}

// IndexOf returns the bar index for a date key, or (-1,false).
func (s *Stock) IndexOf(dateKey string) (int, bool) {
	i, ok := s.idxOf[dateKey]
	if !ok {
		return -1, false
	}
	return i, true
}

// Universe is the full cached population plus the shared trading-date axis.
type Universe struct {
	Stocks []*Stock
	Axis   []string // sorted unique date keys across all stocks
	bySym  map[string]*Stock
}

// Get returns a stock by symbol.
func (u *Universe) Get(sym string) (*Stock, bool) { s, ok := u.bySym[sym]; return s, ok }

// Params controls a backtest run. Defaults are calibration baselines, NOT final
// values; everything is overridable from the runner.
type Params struct {
	RSLookbackDays int // default 120
	RSMinHistory   int // default 100

	Warmup     int   // first eligible bar index (per stock)
	Horizons   []int // forward return horizons in trading days (e.g., 5/10/20/60)
	MinForward int   // smallest horizon that must be available to record a trade

	EntryMode string // "next_open" (primary) | "signal_close" (reference only)

	// Stop rules that are active for this run (first hit wins). Recognized:
	// "BREAK_MA60", "BREAK_SWING_LOW", "PCT_-8", "PCT_-10".
	StopRules    []string
	SwingLowback int // bars back to define the entry swing low (default 20)

	Cooldown int // bars to suppress re-entry on same stock+setup+bucket (default 10)

	// BaseLowLookback (Setup C base-low retest): trailing window whose min Low is
	// used as a PROXY for the VCP base / recent contraction low (default 40).
	// NOTE: this is a proxy — ComputeVCP does not expose contraction trough prices.
	BaseLowLookback int

	// ForceLowConfidence pins SetupStat.Confidence to LOW regardless of sample
	// count (Setup D: only one real crash event in the data).
	ForceLowConfidence bool
}

// DefaultParams returns the baseline parameters.
func DefaultParams() Params {
	return Params{
		RSLookbackDays: 120,
		RSMinHistory:   100,
		Warmup:         250,
		Horizons:       []int{5, 10, 20, 60},
		MinForward:     5,
		EntryMode:      "next_open",
		StopRules:       []string{"BREAK_MA60", "PCT_-10"},
		SwingLowback:    20,
		Cooldown:        10,
		BaseLowLookback: 40,
	}
}

// Trigger is what a Setup returns when bar i is an entry signal (as-of i).
// Setup-specific context the engine cannot derive on its own goes here.
type Trigger struct {
	Bucket          int     // Setup B pullback bucket (e.g., 10 for 10%); 0 = n/a
	PullbackPct     float64 // pullback % from recent high (setup-defined)
	VCPValid        bool
	VCPGrade        string  // Setup C: ComputeVCP grade (EARLY/STANDARD/HIGH_QUALITY)
	VCPQualityScore float64 // Setup C: ComputeVCP quality score
	MomentumFlow    string
	MTFSignal       string
	Note            string
}

// Setup is one entry strategy. Detect MUST only read bars with index <= i
// (no look-ahead). It returns nil when bar i is not an entry trigger.
type Setup interface {
	Name() string
	Detect(u *Universe, rs *RSPanel, s *Stock, i int, p Params) *Trigger
}

// Trade is one backtested entry → forward outcome. Field order here is the
// CSV schema (see output.go csvHeader); keep them in sync.
type Trade struct {
	SetupName              string
	StockCode             string
	StockName             string
	IsWatchlistMember     bool
	EntryDate             time.Time
	EntryPrice            float64
	SignalDate            time.Time // detection bar (i); entry is i+1
	SignalClose           float64   // close at detection bar — reference only

	// Stop-adjusted returns (MAIN statistic): if a stop is hit on or before the
	// horizon, the return uses the stop exit price; otherwise the horizon close.
	// NaN when neither a stop nor the horizon close is available.
	Return5d  float64
	Return10d float64
	Return20d float64
	Return60d float64

	// Hold-to-horizon returns (COMPARISON only): ignore stops entirely; NaN when
	// the horizon close is unavailable.
	HoldReturn5d  float64
	HoldReturn10d float64
	HoldReturn20d float64
	HoldReturn60d float64

	MaxDrawdownAfterEntry float64 // hold-path max adverse excursion over the maxH window (in CSV)
	RealizedDrawdown      float64 // stop-aware: min low up to the stop (or horizon) — NOT in CSV; for benchmark dd
	HitStop               bool
	StopReason            string
	StopDate              time.Time // zero when no stop hit
	StopPrice             float64   // 0 when no stop hit
	RSRankAtEntry         float64
	DistanceFrom52wHigh   float64
	PullbackPctFromHigh   float64
	MA20DistancePct       float64
	MA60DistancePct       float64
	VCPValid              bool
	VCPGrade              string  // Setup C: VCP grade (empty for A/B)
	VCPQualityScore       float64 // Setup C: VCP quality score (0 for A/B)
	MomentumFlow          string
	MTFSignal             string
	Sector                string
	Bucket                int // for Setup B grouping
}

// SetupStat aggregates trades for one setup (or setup+bucket).
type SetupStat struct {
	SetupName      string
	Bucket         int
	StopPolicy     string // empty for R6-2b runs; set in the R6-3 benchmark
	Subgroup       string // Setup C grade/quality subgroup label (else empty)
	SampleCount    int
	// Main statistics use stop-adjusted return.
	WinRate      map[int]float64 // horizon → win rate % (stop-adjusted)
	AvgReturn    map[int]float64 // horizon → avg return % (stop-adjusted)
	MedianReturn map[int]float64 // horizon → median return % (stop-adjusted)
	// Comparison statistics use hold-to-horizon return.
	HoldWinRate   map[int]float64
	HoldAvgReturn map[int]float64
	// StopDelta[h] = avg stop-adjusted − avg hold (positive = stop helped).
	StopDelta map[int]float64

	MaxDrawdownAvg float64 // hold-path (R6-2b summary)
	MaxDrawdownP90 float64
	RealizedDDAvg  float64 // stop-aware realized drawdown (R6-3 benchmark)
	RealizedDDP90  float64
	StopHitRate    float64
	Confidence     string // HIGH | MEDIUM | LOW
	BestCases      []string
	WorstCases     []string

	// Setup D regime metadata (empty for other setups).
	EventCount           int
	RegimeDateRange      string
	ProxySymbol          string
	MarketProxyReturn20d float64
}
