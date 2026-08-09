package institution

// signal.go emits INSTITUTION-ONLY signals from a StockChipView. By design it depends on
// nothing outside this package — no BIAS, no price trend, no market regime, no scanner.
// Cross-domain confluence lives in internal/scanner/signal_confluence.go (SPEC §15).
//
// P0 signal set only. P1 (acceleration / STRONG_ACCUMULATION / STRONG_DISTRIBUTION) is
// deliberately out of scope here.

// Institution-only signal types.
const (
	SignalContinuousBuying  = "CONTINUOUS_BUYING"
	SignalContinuousSelling = "CONTINUOUS_SELLING"
	SignalBuyToSell         = "BUY_TO_SELL"
	SignalSellToBuy         = "SELL_TO_BUY"
)

// Signal is one institution-only signal for one display category.
type Signal struct {
	Category string // CategoryForeign | CategoryTrust | CategoryDealer | CategoryTotal
	Type     string
}

// SignalThresholds centralizes the (currently single) tunable. BASELINE, not calibrated.
type SignalThresholds struct {
	ContinuousDays int // consecutive same-sign days to flag CONTINUOUS_* (default 3)
}

// DefaultSignalThresholds is the P0 baseline.
func DefaultSignalThresholds() SignalThresholds { return SignalThresholds{ContinuousDays: 3} }

// Defaulted fills zero fields with defaults so a zero-value config is usable.
func (t SignalThresholds) Defaulted() SignalThresholds {
	if t.ContinuousDays <= 0 {
		t.ContinuousDays = 3
	}
	return t
}

// Classify returns the institution-only signals for a view. CONTINUOUS_* is emitted only
// when the streak is COMPLETE (§10) — incomplete data must never surface as a confirmed
// streak. Transition signals come straight from the deterministic Transition field.
func Classify(v StockChipView, th SignalThresholds) []Signal {
	th = th.Defaulted()
	var out []Signal
	cats := []struct {
		name  string
		stats LegStats
	}{
		{CategoryForeign, v.Foreign},
		{CategoryTrust, v.Trust},
		{CategoryDealer, v.Dealer},
		{CategoryTotal, v.Total},
	}
	for _, c := range cats {
		s := c.stats
		if s.StreakComplete && s.ConsecutiveBuyDays >= th.ContinuousDays {
			out = append(out, Signal{Category: c.name, Type: SignalContinuousBuying})
		}
		if s.StreakComplete && s.ConsecutiveSellDays >= th.ContinuousDays {
			out = append(out, Signal{Category: c.name, Type: SignalContinuousSelling})
		}
		switch s.Transition {
		case TransitionBuyToSell:
			out = append(out, Signal{Category: c.name, Type: SignalBuyToSell})
		case TransitionSellToBuy:
			out = append(out, Signal{Category: c.name, Type: SignalSellToBuy})
		}
	}
	return out
}
