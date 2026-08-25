package r6backtest

import "fmt"

// ──────────────────────────────────────────────────────────────────────────────
// Flat-price × volume — the SEMANTIC question, measured before it is decided.
//
// The scanner's price direction is a strict `close > prevClose`, which puts an exactly
// unchanged session in the "down" bucket. The LABEL was corrected outright, because calling a
// 0.00% close a decline is a false statement rather than a judgement. What that session
// should be worth to the SCORE is a different kind of question, and this file exists so it is
// answered by forward returns instead of by whoever is editing scorer.go.
//
// Three candidate treatments are on the table:
//
//	A. FLAT scores as DOWN   — today's behaviour, kept only because it is today's behaviour
//	B. FLAT scores as NEUTRAL — its own bucket, neither rewarded nor penalised
//	C. FLAT scores as UP      — an unchanged close held its ground
//
// None is assumed. The conditions below split the price×volume grid finely enough that the
// forward returns can say which of the three the data supports — or that the difference is
// too small to matter, which is also an answer and the one that would leave scorer.go alone.
//
// The bands are the SHIPPED ones (scanner.DefaultPriceMoveThresholds), so this measures the
// classification that actually runs rather than a private copy.
// ──────────────────────────────────────────────────────────────────────────────

// pvBands mirrors the live volume bands; see the scanner package for why 1.2 / 0.8.
const (
	pvVolExpansion = 1.2
	pvVolShrink    = 0.8
)

// PVCondition is one price×volume cell to measure.
type PVCondition struct {
	Name   string
	Detect func(chgPct, volRatio float64) bool
}

// dayChangePct is the session's close-to-close change at bar i, or (0,false) when there is no
// previous close to measure against. Causal by construction: it reads i and i-1 only.
func dayChangePct(s *Stock, i int) (float64, bool) {
	if i < 1 || i >= len(s.Close) || s.Close[i-1] <= 0 {
		return 0, false
	}
	return (s.Close[i]/s.Close[i-1] - 1) * 100, true
}

// dayVolRatio is the session's volume against its own 20-day mean, matching what the live
// scanner divides by. (0,false) when the mean is unavailable.
func dayVolRatio(s *Stock, i int) (float64, bool) {
	if i < 0 || i >= len(s.Vol) || i >= len(s.VolMA20) || s.VolMA20[i] <= 0 {
		return 0, false
	}
	return s.Vol[i] / s.VolMA20[i], true
}

// PVConditions is the grid. FLAT is EXACTLY zero — the same definition the label correction
// uses — so the rows answer the question that was actually asked.
func PVConditions() []PVCondition {
	up := func(c float64) bool { return c > 0 }
	down := func(c float64) bool { return c < 0 }
	flat := func(c float64) bool { return c == 0 }
	expand := func(v float64) bool { return v >= pvVolExpansion }
	shrink := func(v float64) bool { return v <= pvVolShrink }

	return []PVCondition{
		// The control every row below is read against.
		{"PV_BASELINE_ALL_BARS", func(c, v float64) bool { return true }},

		// The three candidate treatments meet here: if FLAT+expansion behaves like
		// DOWN+expansion, candidate A is supported; if it behaves like UP+expansion,
		// candidate C is; if it sits between them or matches the baseline, B is.
		{"PV_FLAT_VOLUME_EXPANSION", func(c, v float64) bool { return flat(c) && expand(v) }},
		{"PV_DOWN_VOLUME_EXPANSION", func(c, v float64) bool { return down(c) && expand(v) }},
		{"PV_UP_VOLUME_EXPANSION", func(c, v float64) bool { return up(c) && expand(v) }},

		// The same three on thin volume, because the flat-as-down defect affects both
		// halves of the grid and a treatment that only works on one half is not a treatment.
		{"PV_FLAT_VOLUME_SHRINK", func(c, v float64) bool { return flat(c) && shrink(v) }},
		{"PV_DOWN_VOLUME_SHRINK", func(c, v float64) bool { return down(c) && shrink(v) }},
		{"PV_UP_VOLUME_SHRINK", func(c, v float64) bool { return up(c) && shrink(v) }},

		// FLAT regardless of volume — the coarsest form of candidate B, and the row that
		// says whether an unchanged session is distinguishable from the market at all.
		{"PV_FLAT_ANY_VOLUME", func(c, v float64) bool { return flat(c) }},

		// The near-flat band the correction deliberately did NOT touch. If these behave like
		// exact-zero sessions, a wider neutral band is worth proposing; if they behave like
		// the declines they are currently called, the strict definition was right.
		{"PV_NEAR_FLAT_DOWN_0_TO_05", func(c, v float64) bool { return c < 0 && c >= -0.5 }},
		{"PV_NEAR_FLAT_UP_0_TO_05", func(c, v float64) bool { return c > 0 && c <= 0.5 }},
	}
}

// pvSetup adapts a condition to the engine's Setup interface, reusing the existing entry,
// stop, horizon and drawdown machinery unchanged so these rows are comparable with every
// other setup this repo has validated.
type pvSetup struct{ cond PVCondition }

func (p pvSetup) Name() string { return p.cond.Name }

func (p pvSetup) Detect(_ *Universe, _ *RSPanel, s *Stock, i int, _ Params) *Trigger {
	chg, okC := dayChangePct(s, i)
	if !okC {
		return nil
	}
	vr, okV := dayVolRatio(s, i)
	if !okV {
		return nil
	}
	if !p.cond.Detect(chg, vr) {
		return nil
	}
	return &Trigger{Note: fmt.Sprintf("chg=%.2f%% vol=%.2fx", chg, vr)}
}

// PVSetups wraps every condition for the engine.
func PVSetups() []Setup {
	conds := PVConditions()
	out := make([]Setup, 0, len(conds))
	for _, c := range conds {
		out = append(out, pvSetup{cond: c})
	}
	return out
}
