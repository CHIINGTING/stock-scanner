package r6backtest

// StopPolicy decides whether and where an entry gets stopped out. Eval scans bars
// (entryIdx, end] (end is bounded by the longest horizon / data end) and returns
// the FIRST stop. It must read only bars in that forward window plus entry-time
// context — never future-of-stop bars beyond the first hit.
type StopPolicy interface {
	Name() string
	Eval(s *Stock, entryIdx int, entry float64, p Params, end int) StopResult
}

func noHit() StopResult { return StopResult{HitStop: false, StopBar: -1} }

func hitAt(s *Stock, j int, reason string) StopResult {
	return StopResult{HitStop: true, StopBar: j, StopPrice: s.Close[j], StopDate: s.Candles[j].Date, Reason: reason}
}

// rulesStop replays the R6-2b multi-rule stop (BREAK_MA60 / BREAK_SWING_LOW /
// PCT_-8 / PCT_-10). Used both for the live baseline (RunSetup) and the BASELINE
// benchmark policy so they are byte-identical.
type rulesStop struct {
	name  string
	rules []string
}

func (r rulesStop) Name() string { return r.name }

func (r rulesStop) Eval(s *Stock, entryIdx int, entry float64, p Params, end int) StopResult {
	swingLow := minLow(s, entryIdx-1-p.SwingLowback+1, entryIdx-1)
	for j := entryIdx + 1; j <= end; j++ {
		for _, rule := range r.rules {
			switch rule {
			case "BREAK_MA60":
				if s.MA60[j] > 0 && s.Close[j] < s.MA60[j] {
					return hitAt(s, j, "BREAK_MA60")
				}
			case "BREAK_SWING_LOW":
				if swingLow > 0 && s.Close[j] < swingLow {
					return hitAt(s, j, "BREAK_SWING_LOW")
				}
			case "PCT_-8":
				if (s.Close[j]/entry-1)*100 <= -8 {
					return hitAt(s, j, "PCT_-8")
				}
			case "PCT_-10":
				if (s.Close[j]/entry-1)*100 <= -10 {
					return hitAt(s, j, "PCT_-10")
				}
			}
		}
	}
	return noHit()
}

// pctStop stops at a fixed percentage loss from entry.
type pctStop struct {
	name string
	pct  float64 // negative, e.g. -12
}

func (q pctStop) Name() string { return q.name }
func (q pctStop) Eval(s *Stock, entryIdx int, entry float64, _ Params, end int) StopResult {
	for j := entryIdx + 1; j <= end; j++ {
		if (s.Close[j]/entry-1)*100 <= q.pct {
			return hitAt(s, j, q.name)
		}
	}
	return noHit()
}

// atrStop stops when close falls below entry − k×ATR14, with the ATR fixed at the
// SIGNAL bar (entryIdx-1) so the level is known before the position is taken.
type atrStop struct {
	name string
	k    float64
}

func (a atrStop) Name() string { return a.name }
func (a atrStop) Eval(s *Stock, entryIdx int, entry float64, _ Params, end int) StopResult {
	sig := entryIdx - 1
	if sig < 0 || sig >= len(s.ATR14) || s.ATR14[sig] <= 0 {
		return noHit()
	}
	level := entry - a.k*s.ATR14[sig]
	for j := entryIdx + 1; j <= end; j++ {
		if s.Close[j] < level {
			return hitAt(s, j, a.name)
		}
	}
	return noHit()
}

// ma60ConfirmStop stops only after close < MA60 on TWO consecutive bars (a single
// dip that recovers next bar does not stop). Exit at the second day's close.
type ma60ConfirmStop struct{}

func (ma60ConfirmStop) Name() string { return "MA60_CONFIRM" }
func (ma60ConfirmStop) Eval(s *Stock, entryIdx int, _ float64, _ Params, end int) StopResult {
	streak := 0
	for j := entryIdx + 1; j <= end; j++ {
		if s.MA60[j] > 0 && s.Close[j] < s.MA60[j] {
			streak++
			if streak >= 2 {
				return hitAt(s, j, "MA60_CONFIRM")
			}
		} else {
			streak = 0
		}
	}
	return noHit()
}

// noStop never stops (hold-to-horizon control).
type noStop struct{}

func (noStop) Name() string { return "NO_STOP" }
func (noStop) Eval(_ *Stock, _ int, _ float64, _ Params, _ int) StopResult { return noHit() }

// BaselineStopPolicy is the policy RunSetup uses (identical to R6-2b).
func BaselineStopPolicy() StopPolicy {
	return rulesStop{name: "BASELINE", rules: []string{"BREAK_MA60", "PCT_-10"}}
}

// BenchmarkStopPolicies returns the R6-3 comparison set (baseline first, NO_STOP
// last as the hold control).
func BenchmarkStopPolicies() []StopPolicy {
	return []StopPolicy{
		BaselineStopPolicy(),
		pctStop{name: "PCT_ONLY_10", pct: -10},
		pctStop{name: "PCT_ONLY_12", pct: -12},
		pctStop{name: "PCT_ONLY_15", pct: -15},
		atrStop{name: "ATR_2", k: 2},
		atrStop{name: "ATR_3", k: 3},
		rulesStop{name: "SWING_LOW", rules: []string{"BREAK_SWING_LOW"}},
		ma60ConfirmStop{},
		noStop{},
	}
}
