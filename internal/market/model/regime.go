package model

// Regime is the market's structural state — the single most important output of the
// dashboard, and the ONE regime vocabulary in the codebase.
//
// The five values are the public, observable states. UNKNOWN is not a sixth market
// condition: it is the honest answer when the data cannot support a call, and it exists so
// that insufficient history never silently becomes SIDEWAYS.
//
// Values are the stable SCREAMING_SNAKE strings that get persisted in every snapshot.
type Regime string

const (
	RegimeBull         Regime = "BULL"          // healthy uptrend, broad participation
	RegimeBullPullback Regime = "BULL_PULLBACK" // primary uptrend intact, orderly correction
	RegimeSideways     Regime = "SIDEWAYS"      // no directional edge
	RegimeDistribution Regime = "DISTRIBUTION"  // index resilient, internals deteriorating
	RegimeBear         Regime = "BEAR"          // trend and structure both bearish
	RegimeUnknown      Regime = "UNKNOWN"       // insufficient / missing data
)

// Valid guards against a regime string that no rule can produce (e.g. decoded from a
// pre-rename snapshot).
func (r Regime) Valid() bool {
	switch r {
	case RegimeBull, RegimeBullPullback, RegimeSideways,
		RegimeDistribution, RegimeBear, RegimeUnknown:
		return true
	}
	return false
}
