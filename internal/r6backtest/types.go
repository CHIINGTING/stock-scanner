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

	idxOf map[string]int // date(YYYY-MM-DD) → bar index
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
		StopRules:      []string{"BREAK_MA60", "PCT_-10"},
		SwingLowback:   20,
		Cooldown:       10,
	}
}

// Trigger is what a Setup returns when bar i is an entry signal (as-of i).
// Setup-specific context the engine cannot derive on its own goes here.
type Trigger struct {
	Bucket       int     // Setup B pullback bucket (e.g., 10 for 10%); 0 = n/a
	PullbackPct  float64 // pullback % from recent high (setup-defined)
	VCPValid     bool
	MomentumFlow string
	MTFSignal    string
	Note         string
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
	Exit5dReturn          float64 // NaN when horizon not available
	Exit10dReturn         float64
	Exit20dReturn         float64
	Exit60dReturn         float64
	MaxDrawdownAfterEntry float64
	HitStop               bool
	StopReason            string
	RSRankAtEntry         float64
	DistanceFrom52wHigh   float64
	PullbackPctFromHigh   float64
	MA20DistancePct       float64
	MA60DistancePct       float64
	VCPValid              bool
	MomentumFlow          string
	MTFSignal             string
	Sector                string
	Bucket                int // for Setup B grouping
}

// SetupStat aggregates trades for one setup (or setup+bucket).
type SetupStat struct {
	SetupName      string
	Bucket         int
	SampleCount    int
	WinRate        map[int]float64 // horizon → win rate %
	AvgReturn      map[int]float64 // horizon → avg return %
	MedianReturn   map[int]float64 // horizon → median return %
	MaxDrawdownAvg float64
	MaxDrawdownP90 float64
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
