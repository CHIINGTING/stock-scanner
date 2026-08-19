package technical

// Analyze runs every R14 indicator over one stock's bars and aggregates them into a Context.
//
// It is the ONLY entry point callers should use, so a consumer cannot accidentally build a
// context from a partially computed result. It is pure: no clock, no randomness, no network,
// no database — the same bars always produce the same Result, which is the precondition for
// R14's outcome validation to mean anything.
//
// Every sub-result is always present when Analyze runs at all; a sub-result that could not be
// computed carries a non-OK Status rather than being nil. Nil is reserved for one thing only:
// the caller never ran this because the feature is off. Those two states must stay
// distinguishable, so this function never returns nil.
func Analyze(bars []Bar, cfg Config) *Result {
	cfg = cfg.Defaulted()

	adx := ComputeADX(bars, cfg)
	rsi := ComputeRSI(bars, cfg)
	macd := ComputeMACD(bars, cfg)
	kel := ComputeKeltner(bars, cfg)
	piv := ComputePivot(bars, cfg)

	r := &Result{
		Config:  cfg,
		ADX:     &adx,
		RSI:     &rsi,
		MACD:    &macd,
		Keltner: &kel,
		Pivot:   &piv,
	}
	r.Context = BuildContext(*r)
	return r
}

// MinBars reports how many bars the LONGEST-window indicator needs before it can publish.
//
// Callers use it to explain an all-INSUFFICIENT_DATA result ("this stock has 30 bars, the
// slowest indicator needs 37") instead of leaving a user to guess which window fell short.
func MinBars(cfg Config) int {
	cfg = cfg.Defaulted()
	need := 2 * cfg.ADXPeriod             // ADX: two stacked Wilder windows
	if n := cfg.RSIPeriod + 2; n > need { // RSI: one window plus a bar for the trajectory
		need = n
	}
	if n := cfg.MACDSlow + cfg.MACDSignal + 1; n > need { // MACD: slow EMA, signal EMA, trajectory
		need = n
	}
	if n := cfg.KeltnerATRPeriod + 1; n > need {
		need = n
	}
	if n := cfg.KeltnerEMAPeriod; n > need {
		need = n
	}
	return need
}
