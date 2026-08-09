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

// Confidence is the LEGACY coarse confidence of the pre-Evidence dashboard flow.
//
// Deprecated: the authoritative confidence is Snapshot.Confidence, a 0..1 value computed
// from data completeness × analyzer agreement × conviction (M7). This enum survives only
// because the old Dashboard/Classify path still compiles against it; it is removed together
// with that path. Do not use it in new code, and do not invent a numeric mapping for it —
// the numbers belong to the M7 engine, not to a translation table.
type Confidence string

const (
	ConfidenceLow    Confidence = "LOW"
	ConfidenceMedium Confidence = "MEDIUM"
)

// RegimeInfo is the LEGACY regime verdict of the pre-Evidence dashboard flow.
//
// Deprecated: superseded by RegimeDecision (structure.go), which additionally records which
// decision-table rule fired and the structural view behind it. Retained until the M3 regime
// engine replaces Classify().
//
// RED LINE (still applies to both): none of these strings may contain trade instructions
// (買/賣/下單/進場價). The dashboard describes the environment; it never tells the user to trade.
type RegimeInfo struct {
	Regime      Regime     `json:"regime"`
	Confidence  Confidence `json:"confidence"`
	Description string     `json:"description"`
	Strategy    string     `json:"strategy"`
	Risk        string     `json:"risk"`
	Reasons     []string   `json:"reasons"`
	Caveats     []string   `json:"caveats"`
}
