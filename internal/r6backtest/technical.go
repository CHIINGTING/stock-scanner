package r6backtest

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/technical"
)

// ──────────────────────────────────────────────────────────────────────────────
// R14 outcome validation — technical indicators measured against forward returns.
//
// This is the part of R14 that decides whether any of it was worth anything. The premise is
// explicit: a textbook indicator is a HYPOTHESIS until this repo's own history says
// otherwise, and nothing here may be used to tune a threshold into looking good.
//
// It reuses the existing r6backtest engine rather than growing a second one, exactly as R12
// did. That buys the entry rule (signal at bar i, entry at i+1), the stop machinery, the
// horizon returns and the drawdown accounting unchanged — so a technical setup is measured on
// the same terms as every setup already validated in this repo, and the numbers are
// comparable.
//
// The conditions read the SHIPPED technical package with its SHIPPED defaults. Measuring a
// reimplementation, or a privately tuned parameter set, would validate a hypothesis nobody
// actually runs.
// ──────────────────────────────────────────────────────────────────────────────

// techCfg is the parameter set under validation: the same DefaultConfig the scanner attaches.
// TestBacktestUsesShippedTechnicalConfig fails if the two ever drift apart.
var techCfg = technical.DefaultConfig()

// TechnicalCondition is one named situation to measure. Detect is causal by construction: it
// is handed the bars up to and including i and nothing after.
type TechnicalCondition struct {
	Name   string
	Detect func(r *technical.Result) bool
}

// barsUpTo converts a stock's candles up to and including bar i into technical bars.
//
// Slicing here is what makes the whole comparison honest: the indicator never sees a bar the
// live scanner would not have had, so a "signal" in the backtest is one that could actually
// have been acted on. TestTechnicalConditionsAreCausal asserts it rather than trusting it.
func barsUpTo(s *Stock, i int) []technical.Bar {
	if i < 0 || i >= len(s.Candles) {
		return nil
	}
	out := make([]technical.Bar, i+1)
	for k := 0; k <= i; k++ {
		c := s.Candles[k]
		out[k] = technical.Bar{
			Date: c.Date, Open: c.Open, High: c.High, Low: c.Low, Close: c.Close,
			Volume: float64(c.Volume),
		}
	}
	return out
}

// TechnicalConditions is the comparison R14 is required to make.
//
// It deliberately includes a BASELINE and the single-indicator legs alongside the combined
// ones. Without the baseline a win rate is a number with nothing to beat; without the single
// legs, a combination that looks good cannot be told apart from one whose value comes
// entirely from one of its parts. Those are the same controls R12's validation used, and the
// reason it was able to falsify part of its own hypothesis.
//
// The names map directly onto the questions in the R14 specification.
func TechnicalConditions() []TechnicalCondition {
	ok := func(r *technical.Result) bool { return r != nil && r.Context != nil }

	adxOK := func(r *technical.Result) *technical.ADXResult {
		if r == nil || r.ADX == nil || !r.ADX.Status.OK() {
			return nil
		}
		return r.ADX
	}
	rsiOK := func(r *technical.Result) *technical.RSIResult {
		if r == nil || r.RSI == nil || !r.RSI.Status.OK() {
			return nil
		}
		return r.RSI
	}
	macdOK := func(r *technical.Result) *technical.MACDResult {
		if r == nil || r.MACD == nil || !r.MACD.Status.OK() {
			return nil
		}
		return r.MACD
	}
	kelOK := func(r *technical.Result) *technical.KeltnerResult {
		if r == nil || r.Keltner == nil || !r.Keltner.Status.OK() {
			return nil
		}
		return r.Keltner
	}
	pivOK := func(r *technical.Result) *technical.PivotResult {
		if r == nil || r.Pivot == nil || !r.Pivot.Status.OK() {
			return nil
		}
		return r.Pivot
	}

	strongTrend := func(a *technical.ADXResult) bool {
		return a != nil && (a.Strength == technical.StrengthStrong || a.Strength == technical.StrengthVeryStrong)
	}

	return []TechnicalCondition{
		// The control. Every bar with a computable reading — what any condition below has
		// to beat before it can claim to have found anything.
		{"BASELINE_ALL_BARS", func(r *technical.Result) bool { return ok(r) }},

		// ── ADX ────────────────────────────────────────────────────────────────────
		// "Is a strong, directional trend better than a weak one over 10/20 days?"
		{"ADX_STRONG_BULLISH", func(r *technical.Result) bool {
			a := adxOK(r)
			return strongTrend(a) && a.Direction == technical.DirBullish
		}},
		{"ADX_WEAK", func(r *technical.Result) bool {
			a := adxOK(r)
			return a != nil && a.Strength == technical.StrengthWeak
		}},
		// The control that keeps "strong" from being read as "bullish": if this performs
		// like ADX_STRONG_BULLISH, the strength band is measuring volatility, not edge.
		{"ADX_STRONG_BEARISH", func(r *technical.Result) bool {
			a := adxOK(r)
			return strongTrend(a) && a.Direction == technical.DirBearish
		}},

		// ── MACD ───────────────────────────────────────────────────────────────────
		// "Does the histogram trajectory add anything to the level?"
		{"MACD_ABOVE_SIGNAL", func(r *technical.Result) bool {
			m := macdOK(r)
			return m != nil && m.MACD > m.SignalVal
		}},
		{"MACD_ABOVE_SIGNAL_ACCELERATING", func(r *technical.Result) bool {
			m := macdOK(r)
			return m != nil && m.MACD > m.SignalVal && m.Momentum == technical.MomentumAccelerating
		}},
		{"MACD_GOLDEN_CROSS", func(r *technical.Result) bool {
			m := macdOK(r)
			return m != nil && m.Cross == technical.CrossGolden
		}},

		// ── RSI ────────────────────────────────────────────────────────────────────
		// The open question the specification refuses to prejudge: on this market and over
		// these horizons, is an overbought reading continuation or mean reversion? The two
		// variants separate it from the trend it sits in.
		{"RSI_OVERBOUGHT", func(r *technical.Result) bool {
			v := rsiOK(r)
			return v != nil && v.Zone == technical.RSIOverbought
		}},
		{"RSI_OVERBOUGHT_IN_STRONG_TREND", func(r *technical.Result) bool {
			v, a := rsiOK(r), adxOK(r)
			return v != nil && v.Zone == technical.RSIOverbought && strongTrend(a)
		}},
		{"RSI_OVERSOLD", func(r *technical.Result) bool {
			v := rsiOK(r)
			return v != nil && v.Zone == technical.RSIOversold
		}},

		// ── Keltner ────────────────────────────────────────────────────────────────
		// "Is a breakout worth anything on its own, or only when the trend confirms it?"
		{"KELTNER_BREAKOUT", func(r *technical.Result) bool {
			k := kelOK(r)
			return k != nil && k.Channel == technical.ChannelBreakout
		}},
		{"KELTNER_BREAKOUT_ADX_CONFIRMED", func(r *technical.Result) bool {
			k, a := kelOK(r), adxOK(r)
			return k != nil && k.Channel == technical.ChannelBreakout && strongTrend(a)
		}},
		{"KELTNER_BREAKOUT_UNCONFIRMED", func(r *technical.Result) bool {
			k, a := kelOK(r), adxOK(r)
			return k != nil && k.Channel == technical.ChannelBreakout &&
				a != nil && a.Strength == technical.StrengthWeak
		}},

		// ── Pivot ──────────────────────────────────────────────────────────────────
		// "Does proximity to resistance actually worsen the short-term risk/reward, and does
		// proximity to support actually improve the odds of a bounce?"
		{"PIVOT_NEAR_RESISTANCE", func(r *technical.Result) bool {
			p := pivOK(r)
			return p != nil && p.Location == technical.LocNearResistance
		}},
		{"PIVOT_NEAR_SUPPORT", func(r *technical.Result) bool {
			p := pivOK(r)
			return p != nil && p.Location == technical.LocNearSupport
		}},

		// ── the aggregated context ─────────────────────────────────────────────────
		// The combination the aggregator names. If this does not beat its own parts, the
		// aggregation is adding complexity without adding information.
		{"CONTEXT_STRONG_BULLISH_TREND", func(r *technical.Result) bool {
			return ok(r) && hasContextSignal(r, technical.SigStrongBullishTrend)
		}},
		{"CONTEXT_TREND_EXPANSION", func(r *technical.Result) bool {
			return ok(r) && hasContextSignal(r, technical.SigTrendExpansion)
		}},
		{"CONTEXT_UNCONFIRMED_BREAKOUT", func(r *technical.Result) bool {
			return ok(r) && hasContextSignal(r, technical.SigUnconfirmedBreakout)
		}},
	}
}

func hasContextSignal(r *technical.Result, want string) bool {
	if r == nil || r.Context == nil {
		return false
	}
	for _, s := range r.Context.Signals {
		if s == want {
			return true
		}
	}
	return false
}

// TechnicalPanel holds every stock's per-bar reading, computed ONCE.
//
// Without it the cost is quadratic and then multiplied by the number of conditions: the
// engine runs one pass per setup, so seventeen conditions would each re-derive the same
// Analyze for the same bar — on a 1700-stock universe with 500 bars that is billions of
// redundant operations, and a validation tool nobody can afford to run is a validation tool
// that never runs. Building the panel first mirrors what R12 does with BuildHeatPanel.
//
// It stores a COMPACT per-bar record rather than the full Result. Only the fields the
// conditions actually read are kept, which is what keeps the whole universe in memory
// instead of hundreds of megabytes of unused struct.
type TechnicalPanel struct {
	byStock map[string][]techBar
}

// techBar is one bar's reading, reduced to what the conditions consult.
type techBar struct {
	ok bool // a context existed at all — the baseline's condition

	adxOK       bool
	adx         float64
	adxStrength string
	adxDir      string

	rsiOK   bool
	rsi     float64
	rsiZone string

	macdOK       bool
	macd         float64
	macdSignal   float64
	macdCross    string
	macdMomentum string

	keltnerOK      bool
	keltnerChannel string

	pivotOK       bool
	pivotLocation string

	signals []string
}

func (p *TechnicalPanel) at(symbol string, i int) techBar {
	if p == nil {
		return techBar{}
	}
	rows, ok := p.byStock[symbol]
	if !ok || i < 0 || i >= len(rows) {
		return techBar{}
	}
	return rows[i]
}

// BuildTechnicalPanel computes every stock's readings for every bar.
//
// Causality is preserved exactly as before: bar i is derived from bars 0..i and nothing
// after, so precomputing changes the cost and not a single verdict.
// TestPanelMatchesDirectAnalysis holds the two paths to the same answer.
func BuildTechnicalPanel(u *Universe) *TechnicalPanel {
	p := &TechnicalPanel{byStock: make(map[string][]techBar, len(u.Stocks))}
	for _, s := range u.Stocks {
		rows := make([]techBar, len(s.Candles))
		for i := range s.Candles {
			rows[i] = summarise(technical.Analyze(barsUpTo(s, i), techCfg))
		}
		p.byStock[s.Symbol] = rows
	}
	return p
}

// summarise reduces a full Result to the fields the conditions read.
func summarise(r *technical.Result) techBar {
	if r == nil {
		return techBar{}
	}
	b := techBar{ok: r.Context != nil}
	if v := r.ADX; v != nil && v.Status.OK() {
		b.adxOK, b.adx, b.adxStrength, b.adxDir = true, v.ADX, v.Strength, v.Direction
	}
	if v := r.RSI; v != nil && v.Status.OK() {
		b.rsiOK, b.rsi, b.rsiZone = true, v.Value, v.Zone
	}
	if v := r.MACD; v != nil && v.Status.OK() {
		b.macdOK, b.macd, b.macdSignal = true, v.MACD, v.SignalVal
		b.macdCross, b.macdMomentum = v.Cross, v.Momentum
	}
	if v := r.Keltner; v != nil && v.Status.OK() {
		b.keltnerOK, b.keltnerChannel = true, v.Channel
	}
	if v := r.Pivot; v != nil && v.Status.OK() {
		b.pivotOK, b.pivotLocation = true, v.Location
	}
	if r.Context != nil {
		b.signals = r.Context.Signals
	}
	return b
}

// toResult rebuilds the minimal Result a condition needs from a panel row, so the SAME
// condition functions serve both the panel path and a direct analysis. Two copies of the
// rules would be two chances for the backtest to measure something the scanner does not do.
func (b techBar) toResult() *technical.Result {
	r := &technical.Result{Config: techCfg}
	if b.adxOK {
		r.ADX = &technical.ADXResult{Status: technical.StatusOK, ADX: b.adx,
			Strength: b.adxStrength, Direction: b.adxDir}
	}
	if b.rsiOK {
		r.RSI = &technical.RSIResult{Status: technical.StatusOK, Value: b.rsi, Zone: b.rsiZone}
	}
	if b.macdOK {
		r.MACD = &technical.MACDResult{Status: technical.StatusOK, MACD: b.macd,
			SignalVal: b.macdSignal, Cross: b.macdCross, Momentum: b.macdMomentum}
	}
	if b.keltnerOK {
		r.Keltner = &technical.KeltnerResult{Status: technical.StatusOK, Channel: b.keltnerChannel}
	}
	if b.pivotOK {
		r.Pivot = &technical.PivotResult{Status: technical.StatusOK, Location: b.pivotLocation}
	}
	if b.ok {
		r.Context = &technical.Context{Signals: b.signals}
	}
	return r
}

// technicalSetup adapts a condition to the engine's Setup interface, so the existing entry,
// stop, horizon and drawdown machinery is reused verbatim.
type technicalSetup struct {
	cond  TechnicalCondition
	panel *TechnicalPanel
}

func (t technicalSetup) Name() string { return t.cond.Name }

func (t technicalSetup) Detect(_ *Universe, _ *RSPanel, s *Stock, i int, _ Params) *Trigger {
	r := t.resultAt(s, i)
	if !t.cond.Detect(r) {
		return nil
	}
	note := ""
	if r.ADX != nil && r.RSI != nil {
		note = fmt.Sprintf("adx=%.1f rsi=%.1f", r.ADX.ADX, r.RSI.Value)
	}
	return &Trigger{Note: note}
}

// resultAt prefers the precomputed panel and falls back to a direct analysis, so a setup
// built without a panel (as the unit tests do) still behaves identically.
func (t technicalSetup) resultAt(s *Stock, i int) *technical.Result {
	if t.panel != nil {
		return t.panel.at(s.Symbol, i).toResult()
	}
	return technical.Analyze(barsUpTo(s, i), techCfg)
}

// TechnicalSetups wraps every condition for the engine. A nil panel means "analyse on
// demand", which is correct but quadratic — production callers pass BuildTechnicalPanel's
// result.
func TechnicalSetups(panel *TechnicalPanel) []Setup {
	conds := TechnicalConditions()
	out := make([]Setup, 0, len(conds))
	for _, c := range conds {
		out = append(out, technicalSetup{cond: c, panel: panel})
	}
	return out
}
