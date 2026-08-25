// Package fx is the USD/TWD market-context research layer.
//
// It answers one question and refuses to answer any other: what has the exchange rate done,
// on the exchange rate's own scale, using only information a Taiwan session could actually
// have had. Whether that is worth anything to a trading decision is decided by the backtest
// in internal/r6backtest, not here.
//
// # Quote convention
//
// USDTWD throughout: 1 USD = X TWD. Therefore
//
//	USDTWD rises  → more TWD buys one USD → TWD DEPRECIATION
//	USDTWD falls  → fewer TWD buy one USD → TWD APPRECIATION
//
// This is the single easiest thing to get backwards in the whole package, so the sign
// appears in exactly one function (Direction) and TestDirectionIsNotInverted holds it.
//
// # Causality — the load-bearing constraint
//
// Yahoo buckets FX bars by LONDON midnight: the bar labelled D opens 00:00 London on D and
// closes 23:59 London on D, which is 06:59 Taipei on D+1. Taiwan equities close at 13:30
// Taipei on D — seventeen and a half hours BEFORE the FX bar for D is finished.
//
// So the FX close for date D is NOT knowable at the Taiwan close on date D. Pairing them
// would be look-ahead, and on a currency whose moves lead equity flows it would be the
// flattering kind. Every metric in this package is therefore taken AsOf a Taiwan trading
// date and reads only FX bars strictly BEFORE it. MetricsAsOf is the only entry point, and
// TestMetricsAsOfIsCausal proves a future bar cannot change a past reading.
package fx

import (
	"math"
	"time"

	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// Status records how one reading ended, matching the vocabulary internal/technical and
// internal/candlestick already use rather than inventing a third.
type Status string

const (
	StatusOK               Status = "OK"
	StatusNoData           Status = "NO_DATA"
	StatusInsufficientData Status = "INSUFFICIENT_DATA"
)

// OK reports whether a status carries a usable reading.
func (s Status) OK() bool { return s == StatusOK }

// Context is the classified state, named after the currency a reader in Taipei thinks in.
//
// For USDTWD the names describe the LOCAL currency: TWD_DEPRECIATION means USDTWD went up.
// "USDTWD_UP" would invite exactly the inversion this package guards against.
const (
	ContextStrongAppreciation = "TWD_STRONG_APPRECIATION"
	ContextAppreciation       = "TWD_APPRECIATION"
	ContextStable             = "TWD_STABLE"
	ContextDepreciation       = "TWD_DEPRECIATION"
	ContextStrongDepreciation = "TWD_STRONG_DEPRECIATION"
	ContextUnknown            = "UNKNOWN"
)

// The dollar index needs its OWN vocabulary. Reusing the TWD names for it would state
// something false: DXY falling means the DOLLAR weakened against a basket that does not
// contain TWD, which says nothing about the local currency. A label that is wrong but
// plausible is worse than no label, because nothing downstream can detect it.
//
// Direction convention is the same underlying fact in both: quote UP = base currency (USD)
// stronger. Only the currency the name refers to differs.
const (
	ContextUSDStrongAppreciation = "USD_STRONG_APPRECIATION"
	ContextUSDAppreciation       = "USD_APPRECIATION"
	ContextUSDStable             = "USD_STABLE"
	ContextUSDDepreciation       = "USD_DEPRECIATION"
	ContextUSDStrongDepreciation = "USD_STRONG_DEPRECIATION"
)

// Bar is one FX session. Date is the bar's own label (a London trading day), NOT a Taiwan
// trading date — see the package comment for why the two must not be conflated.
type Bar struct {
	Date  string  `json:"date"` // YYYY-MM-DD
	Close float64 `json:"close"`
}

// Config holds the windows and bands. One struct, so a parameter is never a magic number
// buried in a rule and the backtest can state exactly what it measured.
type Config struct {
	// ChangeWindows are the lookbacks whose percentage change is reported, in FX sessions.
	ChangeWindows []int `yaml:"change_windows"`
	// ContextWindow is which change the classification reads. Must be in ChangeWindows.
	ContextWindow int `yaml:"context_window"`
	// MAPeriod is the moving average the deviation is measured from.
	MAPeriod int `yaml:"ma_period"`

	// PercentileWindow is the trailing sample the classification ranks against, and
	// PercentileMinSample the smallest sample it will rank against at all.
	PercentileWindow    int `yaml:"percentile_window"`
	PercentileMinSample int `yaml:"percentile_min_sample"`

	// StrongZ / MildZ are the classification bands, in units of the currency's OWN typical
	// move — not percentage points.
	//
	// Fixed percentage bands borrowed from equities do not work here and the data says so:
	// over five years of USD/TWD the median absolute 1-day move is 0.20%, and a ±3% band —
	// the sort R14 uses for stocks, where the ±10% daily limit makes it meaningful — fires
	// on 0.15% of days. A threshold that never triggers is not a conservative threshold, it
	// is a missing feature.
	//
	// The scale used instead is z = change / stdev(trailing changes of the same window).
	// It is deliberately NOT demeaned: demeaning would make a sustained trend read as
	// "normal", which is exactly the information the research is asking about. So z answers
	// "how many typical moves did the currency travel, and which way", and its sign is the
	// direction.
	StrongZ float64 `yaml:"strong_z"`
	MildZ   float64 `yaml:"mild_z"`
}

// DefaultConfig is the shipped baseline. Every value is an UNVALIDATED starting point: the
// research exists to find out whether any of it carries information, and nothing may be
// scored on it until that is answered.
func DefaultConfig() Config {
	return Config{
		ChangeWindows:       []int{1, 5, 20},
		ContextWindow:       20,
		MAPeriod:            20,
		PercentileWindow:    252, // one year of FX sessions
		PercentileMinSample: 120, // matches R12's BIAS percentile floor
		StrongZ:             1.5,
		MildZ:               0.5,
	}
}

// Defaulted fills zero values, matching the convention used across this repo.
func (c Config) Defaulted() Config {
	d := DefaultConfig()
	if len(c.ChangeWindows) == 0 {
		c.ChangeWindows = d.ChangeWindows
	}
	if c.ContextWindow <= 0 {
		c.ContextWindow = d.ContextWindow
	}
	if c.MAPeriod <= 0 {
		c.MAPeriod = d.MAPeriod
	}
	if c.PercentileWindow <= 0 {
		c.PercentileWindow = d.PercentileWindow
	}
	if c.PercentileMinSample <= 0 {
		c.PercentileMinSample = d.PercentileMinSample
	}
	if c.StrongZ <= 0 {
		c.StrongZ = d.StrongZ
	}
	if c.MildZ <= 0 {
		c.MildZ = d.MildZ
	}
	return c
}

// Metrics is one Taiwan trading date's FX reading.
//
// Every optional number is a pointer: a window the history could not cover is nil, never a
// zero. "The currency did not move" and "we could not measure the move" are different facts
// and this repo keeps them apart everywhere.
type Metrics struct {
	Status Status `json:"status"`

	// AsOfTaiwanDate is the Taiwan session this reading is FOR.
	AsOfTaiwanDate string `json:"as_of_taiwan_date"`
	// FXDate is the FX bar actually used — always strictly before AsOfTaiwanDate. The gap
	// between the two is the causality guarantee, made visible so it can be audited.
	FXDate string `json:"fx_date"`

	Close float64 `json:"close"`

	// ChangePct maps lookback (FX sessions) → percentage change of USDTWD. Positive = TWD
	// weakened over that window.
	ChangePct map[int]float64 `json:"change_pct,omitempty"`

	MA           *float64 `json:"ma,omitempty"`
	DeviationPct *float64 `json:"deviation_pct,omitempty"`

	// ContextZ is the context-window change measured in the currency's own typical moves.
	// Negative = TWD appreciated. nil when the trailing sample was too thin to scale against.
	ContextZ *float64 `json:"context_z,omitempty"`
	Context  string   `json:"context"`
}

// Change returns the change over n FX sessions, and whether it was measurable.
func (m *Metrics) Change(n int) (float64, bool) {
	if m == nil || m.ChangePct == nil {
		return 0, false
	}
	v, ok := m.ChangePct[n]
	return v, ok
}

// Direction turns a USDTWD change into a statement about the CURRENCY. This is the only
// place the sign is interpreted; everything else defers to it.
//
//	change > 0 → USDTWD rose → TWD depreciated
//	change < 0 → USDTWD fell → TWD appreciated
func Direction(changePct float64) string {
	switch {
	case changePct > 0:
		return "TWD_WEAKER"
	case changePct < 0:
		return "TWD_STRONGER"
	default:
		return "TWD_FLAT"
	}
}

// Series is a chronologically ordered run of FX bars.
type Series struct {
	Symbol string `json:"symbol"` // "USDTWD"
	Bars   []Bar  `json:"bars"`
}

// MetricsAsOf computes the reading available to a Taiwan session on taiwanDate.
//
// It uses only bars strictly BEFORE taiwanDate. See the package comment: the FX bar labelled
// with a given date does not finish until the following morning in Taipei, so including it
// would hand the scanner a number that did not exist yet.
func (s *Series) MetricsAsOf(taiwanDate string, cfg Config) Metrics {
	cfg = cfg.Defaulted()
	out := Metrics{
		Status: StatusNoData, AsOfTaiwanDate: taiwanDate,
		Context: ContextUnknown, ChangePct: map[int]float64{},
	}
	if s == nil || len(s.Bars) == 0 || taiwanDate == "" {
		return out
	}

	// The last bar whose own date is strictly before the Taiwan session.
	end := -1
	for i := range s.Bars {
		if s.Bars[i].Date < taiwanDate {
			end = i
			continue
		}
		break
	}
	if end < 0 {
		return out
	}

	closes := make([]float64, 0, end+1)
	for i := 0; i <= end; i++ {
		if c := s.Bars[i].Close; finite(c) && c > 0 {
			closes = append(closes, c)
		}
	}
	if len(closes) == 0 {
		return out
	}

	out.FXDate = s.Bars[end].Date
	out.Close = closes[len(closes)-1]
	out.Status = StatusInsufficientData

	for _, w := range cfg.ChangeWindows {
		if v, ok := changePct(closes, w); ok {
			out.ChangePct[w] = v
			out.Status = StatusOK
		}
	}

	if ma := indicator.SMA(closes, cfg.MAPeriod); len(ma) > 0 {
		if v := ma[len(ma)-1]; v > 0 && finite(v) {
			out.MA = &v
			if dev := (out.Close/v - 1) * 100; finite(dev) {
				out.DeviationPct = &dev
			}
		}
	}

	out.ContextZ, out.Context = classify(closes, cfg, s.Symbol)
	return out
}

// classify scales the context-window change by the currency's own recent volatility.
//
// The sample is a series of OVERLAPPING windowed changes, which inflates neither the sign nor
// the scale — it is used only as a dispersion estimate, never as a probability, so the
// dependence between neighbouring windows costs nothing it is being asked to provide.
func classify(closes []float64, cfg Config, symbol string) (*float64, string) {
	w := cfg.ContextWindow
	latest, ok := changePct(closes, w)
	if !ok {
		return nil, ContextUnknown
	}

	// Every windowed change available inside the trailing sample window.
	sample := make([]float64, 0, cfg.PercentileWindow)
	start := len(closes) - cfg.PercentileWindow
	if start < w {
		start = w
	}
	for i := start; i < len(closes); i++ {
		if closes[i-w] > 0 {
			if v := (closes[i]/closes[i-w] - 1) * 100; finite(v) {
				sample = append(sample, v)
			}
		}
	}
	if len(sample) < cfg.PercentileMinSample {
		return nil, ContextUnknown
	}

	sd := stdev(sample)
	// A currency that has not moved at all over the whole sample has no scale to measure
	// against. That is not "stable with high confidence", it is unmeasurable.
	if sd <= 0 || !finite(sd) {
		return nil, ContextUnknown
	}
	z := latest / sd
	if !finite(z) {
		return nil, ContextUnknown
	}
	return &z, contextFor(z, cfg, symbol)
}

// contextFor buckets a z-score, naming the result after the currency the SYMBOL is about.
//
// NEGATIVE z means the quote fell. For USDTWD that means TWD APPRECIATED; for DXY it means
// the DOLLAR weakened. Same arithmetic, different currency in the name — and the one place
// either could be inverted. TestDirectionIsNotInverted and TestDXYContextIsAboutTheDollar
// cover both.
func contextFor(z float64, cfg Config, symbol string) string {
	strong, mild, stable, mildUp, strongUp :=
		ContextStrongAppreciation, ContextAppreciation, ContextStable,
		ContextDepreciation, ContextStrongDepreciation
	if symbol == SymbolDXY {
		// The index rising IS dollar appreciation, so the up-side names invert relative to
		// USDTWD — where the quote rising is the LOCAL currency depreciating.
		strong, mild, stable, mildUp, strongUp =
			ContextUSDStrongDepreciation, ContextUSDDepreciation, ContextUSDStable,
			ContextUSDAppreciation, ContextUSDStrongAppreciation
	}
	switch {
	case z <= -cfg.StrongZ:
		return strong
	case z <= -cfg.MildZ:
		return mild
	case z >= cfg.StrongZ:
		return strongUp
	case z >= cfg.MildZ:
		return mildUp
	default:
		return stable
	}
}

// stdev is the population standard deviation, used as a dispersion scale.
func stdev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var ss float64
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(xs)))
}

// changePct is the percentage change over n bars ending at the last one.
func changePct(closes []float64, n int) (float64, bool) {
	if n <= 0 || len(closes) < n+1 {
		return 0, false
	}
	prev := closes[len(closes)-1-n]
	if prev <= 0 || !finite(prev) {
		return 0, false
	}
	v := (closes[len(closes)-1]/prev - 1) * 100
	if !finite(v) {
		return 0, false
	}
	return v, true
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// ParseDate is the shared date parser, so a malformed date fails in one place.
func ParseDate(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }
