package technical

// Context is the high-level reading an agent or a human should look at first.
//
// It exists because handing anyone `ADX=31.8 RSI=61.3 MACD=2.1 Signal=1.7 ATR=15.1` and
// expecting a judgement is a bad interface: the numbers are only meaningful in combination,
// and if every consumer recombines them the combinations will drift apart. So every
// cross-indicator rule in R14 lives in THIS FILE and nowhere else — not in the scanner, not
// in the report, not in a prompt.
//
// It is still evidence. The vocabulary is deliberately descriptive (Direction / strength /
// momentum / volatility / location), and Signals name situations, never actions. There is no
// path from here to BUY, SELL, a score, or a sort order; R13's judge and the scanner decide
// what to do, and R14's outcome validation decides whether any of it was worth anything.
type Context struct {
	Direction     string `json:"direction"`
	TrendStrength string `json:"trend_strength"`
	Momentum      string `json:"momentum"`
	Volatility    string `json:"volatility"`
	PriceLocation string `json:"price_location"`

	// Signals are named situations, ordered from the structural (trend) to the local
	// (price location) so the first entry is the most durable observation.
	Signals []string `json:"signals,omitempty"`
}

// Signal vocabulary. Every one of these describes a STATE. None of them is an instruction,
// and TestSignalsNeverInstruct asserts that no verb ever appears here.
const (
	SigStrongBullishTrend    = "STRONG_BULLISH_TREND"
	SigStrongBearishTrend    = "STRONG_BEARISH_TREND"
	SigTrendForming          = "TREND_FORMING"
	SigNoTrend               = "NO_TREND"
	SigMomentumAccelerating  = "BULLISH_MOMENTUM_ACCELERATING"
	SigMomentumDecelerating  = "BULLISH_MOMENTUM_DECELERATING"
	SigBearishAccelerating   = "BEARISH_TREND_ACCELERATING"
	SigMACDGoldenCross       = "MACD_GOLDEN_CROSS"
	SigMACDDeathCross        = "MACD_DEATH_CROSS"
	SigKeltnerUpperBreakout  = "KELTNER_UPPER_BREAKOUT"
	SigKeltnerLowerBreakdown = "KELTNER_LOWER_BREAKDOWN"
	SigTrendExpansion        = "TREND_EXPANSION"
	SigUnconfirmedBreakout   = "UNCONFIRMED_BREAKOUT"
	SigOverboughtStrongTrend = "OVERBOUGHT_IN_STRONG_TREND"
	SigOversoldStrongTrend   = "OVERSOLD_IN_STRONG_TREND"
	SigNearResistance        = "NEAR_RESISTANCE"
	SigNearSupport           = "NEAR_SUPPORT"
	SigAboveAllResistance    = "ABOVE_ALL_RESISTANCE"
	SigBelowAllSupport       = "BELOW_ALL_SUPPORT"
)

// BuildContext combines whatever indicators produced a usable reading.
//
// Every dimension independently falls back to UNKNOWN when its inputs are missing. That is
// the whole MISSING ≠ NEUTRAL contract at the interpretation level: an absent ADX must not
// make a stock look like it has a WEAK trend, because "no trend measured" and "measured, and
// it is weak" are different facts that would otherwise be indistinguishable downstream.
func BuildContext(r Result) *Context {
	c := &Context{
		Direction:     DirUnknown,
		TrendStrength: StrengthUnknown,
		Momentum:      MomentumUnknown,
		Volatility:    VolUnknown,
		PriceLocation: LocationUnknown,
	}

	adx := okADX(r.ADX)
	rsi := okRSI(r.RSI)
	macd := okMACD(r.MACD)
	kel := okKeltner(r.Keltner)
	piv := okPivot(r.Pivot)

	c.TrendStrength = contextStrength(adx)
	c.Direction = contextDirection(adx, macd, rsi)
	c.Momentum = contextMomentum(macd, rsi)
	c.Volatility = contextVolatility(kel)
	c.PriceLocation = contextLocation(piv)
	c.Signals = buildSignals(c, adx, rsi, macd, kel, piv)
	return c
}

// The ok* helpers return nil for anything that is absent or non-OK, so every rule below can
// treat a nil pointer as "this dimension has nothing to say" without repeating status checks.

func okADX(v *ADXResult) *ADXResult {
	if v == nil || !v.Status.OK() {
		return nil
	}
	return v
}

func okRSI(v *RSIResult) *RSIResult {
	if v == nil || !v.Status.OK() {
		return nil
	}
	return v
}

func okMACD(v *MACDResult) *MACDResult {
	if v == nil || !v.Status.OK() {
		return nil
	}
	return v
}

func okKeltner(v *KeltnerResult) *KeltnerResult {
	if v == nil || !v.Status.OK() {
		return nil
	}
	return v
}

func okPivot(v *PivotResult) *PivotResult {
	if v == nil || !v.Status.OK() {
		return nil
	}
	return v
}

// contextStrength comes from ADX alone: it is the only indicator here that measures how
// directional a move is rather than which way it points.
func contextStrength(adx *ADXResult) string {
	if adx == nil {
		return StrengthUnknown
	}
	return adx.Strength
}

// contextDirection prefers ADX's directional indicators, then MACD, then RSI's zone.
//
// The order is a claim about reliability, not a vote: +DI/-DI are built specifically to
// answer "which way", MACD answers it as a side effect of measuring convergence, and an RSI
// zone is the weakest evidence of the three. When the two strongest disagree the answer is
// MIXED — an honest "the indicators do not agree" is more useful than a casting vote.
func contextDirection(adx *ADXResult, macd *MACDResult, rsi *RSIResult) string {
	switch {
	case adx != nil && macd != nil:
		macdDir := macdDirection(macd)
		if adx.Direction == DirNeutral {
			return macdDir
		}
		if macdDir == DirNeutral || macdDir == adx.Direction {
			return adx.Direction
		}
		return DirMixed
	case adx != nil:
		return adx.Direction
	case macd != nil:
		return macdDirection(macd)
	case rsi != nil:
		switch rsi.Zone {
		case RSIBullish, RSIOverbought:
			return DirBullish
		case RSIWeak, RSIOversold:
			return DirBearish
		default:
			return DirNeutral
		}
	default:
		return DirUnknown
	}
}

// macdDirection reads the line's position relative to its signal, with the zero line as a
// tie-break for the case where they coincide.
func macdDirection(macd *MACDResult) string {
	switch {
	case macd.MACD > macd.SignalVal:
		return DirBullish
	case macd.MACD < macd.SignalVal:
		return DirBearish
	case macd.MACD > 0:
		return DirBullish
	case macd.MACD < 0:
		return DirBearish
	default:
		return DirNeutral
	}
}

// contextMomentum combines the histogram trajectory with the level.
//
// The trajectory wins where it is decisive: a histogram falling from 0.8 to 0.2 is
// DECELERATING even though MACD is still above its signal. That is exactly the reading a
// level-only view cannot produce, and it is why the histogram is consulted first.
func contextMomentum(macd *MACDResult, rsi *RSIResult) string {
	if macd == nil {
		if rsi == nil {
			return MomentumUnknown
		}
		switch {
		case rsi.Rising && (rsi.Zone == RSIBullish || rsi.Zone == RSIOverbought):
			return MomentumPositive
		case rsi.Falling && (rsi.Zone == RSIWeak || rsi.Zone == RSIOversold):
			return MomentumNegative
		default:
			return MomentumNeutral
		}
	}
	switch macd.Momentum {
	case MomentumAccelerating:
		return MomentumAccelerating
	case MomentumDecelerating:
		return MomentumDecelerating
	}
	// A flat trajectory: fall back to which side of the signal line the histogram sits on.
	switch {
	case macd.Histogram > 0:
		return MomentumPositive
	case macd.Histogram < 0:
		return MomentumNegative
	default:
		return MomentumNeutral
	}
}

// contextVolatility reads the Keltner envelope.
//
// EXPANDING / CONTRACTING deliberately do NOT appear here. Deciding that the channel is
// widening needs its width history, which this single-bar view does not have; guessing from
// one reading would be exactly the fabrication R14 forbids. Until a width series is carried,
// an inside-channel reading is NORMAL and nothing claims to know the trajectory.
func contextVolatility(kel *KeltnerResult) string {
	if kel == nil {
		return VolUnknown
	}
	switch kel.Channel {
	case ChannelBreakout:
		return VolBreakout
	case ChannelBreakdown:
		return VolBreakdown
	case ChannelInside:
		return VolNormal
	default:
		return VolUnknown
	}
}

func contextLocation(piv *PivotResult) string {
	if piv == nil {
		return LocationUnknown
	}
	return piv.Location
}

// buildSignals names the situations the combination describes.
//
// Order is structural first (trend), then momentum, then volatility, then location, so the
// first entry is always the most durable observation and a reader who stops early stops on
// the most important thing.
func buildSignals(c *Context, adx *ADXResult, rsi *RSIResult, macd *MACDResult,
	kel *KeltnerResult, piv *PivotResult) []string {

	var out []string
	add := func(s string) { out = append(out, s) }

	// ── trend ───────────────────────────────────────────────────────────────────────
	// A named strong trend requires BOTH a strong ADX and agreement between the direction
	// sources. One indicator on its own is a reading, not a confirmation.
	strong := adx != nil && (adx.Strength == StrengthStrong || adx.Strength == StrengthVeryStrong)
	switch {
	case strong && c.Direction == DirBullish:
		add(SigStrongBullishTrend)
	case strong && c.Direction == DirBearish:
		add(SigStrongBearishTrend)
	case adx != nil && adx.Strength == StrengthForming:
		add(SigTrendForming)
	case adx != nil && adx.Strength == StrengthWeak:
		add(SigNoTrend)
	}

	// ── momentum ────────────────────────────────────────────────────────────────────
	if macd != nil {
		switch macd.Cross {
		case CrossGolden:
			add(SigMACDGoldenCross)
		case CrossDeath:
			add(SigMACDDeathCross)
		}
	}
	switch {
	case c.Direction == DirBullish && c.Momentum == MomentumAccelerating:
		add(SigMomentumAccelerating)
	case c.Direction == DirBullish && c.Momentum == MomentumDecelerating:
		add(SigMomentumDecelerating)
	case c.Direction == DirBearish && c.Momentum == MomentumDecelerating:
		// A bearish trend whose histogram keeps falling is the downside getting stronger.
		add(SigBearishAccelerating)
	}

	// RSI extremes are reported WITH their trend context, because that is the pairing the
	// validation work needs to separate: an overbought reading inside a strong trend and one
	// in a directionless tape are different events, and collapsing them into "OVERBOUGHT"
	// would make the question unanswerable.
	if rsi != nil && strong {
		switch rsi.Zone {
		case RSIOverbought:
			add(SigOverboughtStrongTrend)
		case RSIOversold:
			add(SigOversoldStrongTrend)
		}
	}

	// ── volatility ──────────────────────────────────────────────────────────────────
	if kel != nil {
		switch kel.Channel {
		case ChannelBreakout:
			add(SigKeltnerUpperBreakout)
			// A breakout with a strong, rising trend behind it is an expansion; the same
			// breakout with ADX under 20 is a move with nothing behind it. Naming both
			// cases is what lets the backtest ask which one actually pays.
			switch {
			case strong:
				add(SigTrendExpansion)
			case adx != nil && adx.Strength == StrengthWeak:
				add(SigUnconfirmedBreakout)
			}
		case ChannelBreakdown:
			add(SigKeltnerLowerBreakdown)
		}
	}

	// ── location ────────────────────────────────────────────────────────────────────
	if piv != nil {
		switch piv.Location {
		case LocNearResistance:
			add(SigNearResistance)
		case LocNearSupport:
			add(SigNearSupport)
		case LocAboveResistance:
			add(SigAboveAllResistance)
		case LocBelowSupport:
			add(SigBelowAllSupport)
		}
	}
	return out
}
