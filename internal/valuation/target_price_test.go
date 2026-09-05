package valuation

import (
	"math"
	"strings"
	"testing"
)

func fp(v float64) *float64 { return &v }

// histValuation builds a valuation carrying a usable P/E distribution.
func histValuation(trailingPE float64, samples int, p25, median, p75 float64) Valuation {
	return Valuation{
		Symbol:         "2330.TW",
		Status:         Available,
		TrailingPE:     fp(trailingPE),
		TrailingStatus: Available,
		HistoricalPE: HistoricalPEStats{
			Status:      Available,
			WindowDays:  DefaultWindowDays,
			SampleCount: samples,
			P25:         fp(p25),
			Median:      fp(median),
			P75:         fp(p75),
		},
	}
}

func byName(tp TargetPrice, name string) TargetScenario {
	for _, s := range tp.Scenarios {
		if s.Name == name {
			return s
		}
	}
	return TargetScenario{}
}

// ── the happy path ────────────────────────────────────────────────────────────────────

// Price 1,000 with a published P/E of 20 implies EPS 50. The three multiples then give three
// targets, and the arithmetic is checked exactly rather than approximately.
func TestBearBaseBullFromTheHistoricalDistribution(t *testing.T) {
	tp := ComputeTargetPrice(TargetInput{
		Symbol:       "2330.TW",
		CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03",
		Valuation: histValuation(20, 40, 16, 22, 28),
	})
	if tp.Status != Available {
		t.Fatalf("status = %s (%s)", tp.Status, tp.Reason)
	}
	if tp.Rule != RuleHistoricalPercentile {
		t.Fatalf("rule = %s", tp.Rule)
	}
	if tp.EPSBasis != EPSTrailingImplied {
		t.Fatalf("eps basis = %s", tp.EPSBasis)
	}
	if got := *tp.EPS; got != 50 {
		t.Fatalf("implied EPS = %v, want 50", got)
	}
	for _, tc := range []struct {
		name           string
		wantPE, wantTP float64
		wantUp         float64
	}{
		{ScenarioBear, 16, 800, -20},
		{ScenarioBase, 22, 1100, 10},
		{ScenarioBull, 28, 1400, 40},
	} {
		s := byName(tp, tc.name)
		if s.Status != Available {
			t.Fatalf("%s status = %s (%s)", tc.name, s.Status, s.Reason)
		}
		if *s.PEMultiple != tc.wantPE || *s.TargetPrice != tc.wantTP {
			t.Fatalf("%s = %vx → %v, want %vx → %v", tc.name, *s.PEMultiple, *s.TargetPrice,
				tc.wantPE, tc.wantTP)
		}
		if math.Abs(*s.UpsidePct-tc.wantUp) > 1e-9 {
			t.Fatalf("%s upside = %v, want %v", tc.name, *s.UpsidePct, tc.wantUp)
		}
	}
	// Ordering is fixed so a UI can render three rows without sorting.
	if tp.Scenarios[0].Name != ScenarioBear || tp.Scenarios[1].Name != ScenarioBase ||
		tp.Scenarios[2].Name != ScenarioBull {
		t.Fatalf("order = %v", tp.Scenarios)
	}
}

// Negative upside is a real answer. A stock trading above its own historical P75 has downside
// to every case, and a target engine that hid that would be a marketing tool.
func TestNegativeUpsideIsReported(t *testing.T) {
	tp := ComputeTargetPrice(TargetInput{
		CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03",
		Valuation: histValuation(40, 40, 10, 14, 18),
	})
	if tp.Status != Available {
		t.Fatalf("status = %s", tp.Status)
	}
	for _, s := range tp.Scenarios {
		if *s.UpsidePct >= 0 {
			t.Fatalf("%s upside = %v, expected downside throughout", s.Name, *s.UpsidePct)
		}
	}
	if got := *byName(tp, ScenarioBull).UpsidePct; math.Abs(got-(-55)) > 1e-9 {
		t.Fatalf("bull upside = %v, want -55", got)
	}
}

// A stock priced exactly at its median multiple has zero upside in the base case — which must
// come out as 0, not as "unavailable".
func TestZeroUpside(t *testing.T) {
	tp := ComputeTargetPrice(TargetInput{
		CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03",
		Valuation: histValuation(20, 40, 16, 20, 28),
	})
	s := byName(tp, ScenarioBase)
	if s.UpsidePct == nil || *s.UpsidePct != 0 {
		t.Fatalf("base upside = %v, want exactly 0", s.UpsidePct)
	}
	if *s.TargetPrice != 1000 {
		t.Fatalf("base target = %v", *s.TargetPrice)
	}
}

// ── the refusals ──────────────────────────────────────────────────────────────────────

// The state the repo is actually in today: one archived session, twenty needed.
func TestInsufficientHistoricalSamplesRefuses(t *testing.T) {
	v := histValuation(20, 1, 16, 22, 28)
	v.HistoricalPE.Status = InsufficientData
	tp := ComputeTargetPrice(TargetInput{CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03", Valuation: v})

	if tp.Status != InsufficientData {
		t.Fatalf("status = %s", tp.Status)
	}
	if tp.Rule != RuleNone {
		t.Fatalf("rule = %s — no rule may be claimed when none applied", tp.Rule)
	}
	if tp.SampleCount != 1 || tp.RequiredSamples != MinHistoricalPESamples {
		t.Fatalf("samples = %d/%d", tp.SampleCount, tp.RequiredSamples)
	}
	if len(tp.Scenarios) != 3 {
		t.Fatalf("a refusal must still render three labelled rows, got %d", len(tp.Scenarios))
	}
	for _, s := range tp.Scenarios {
		if s.TargetPrice != nil || s.UpsidePct != nil || s.PEMultiple != nil {
			t.Fatalf("%s carries numbers despite INSUFFICIENT_DATA: %+v", s.Name, s)
		}
		if s.Status != InsufficientData || s.Reason == "" {
			t.Fatalf("%s = %+v", s.Name, s)
		}
	}
}

// A distribution whose status is AVAILABLE but whose quantiles are nil must not produce a
// target from whatever is left.
func TestAvailableDistributionWithMissingQuantilesRefuses(t *testing.T) {
	v := histValuation(20, 40, 16, 22, 28)
	v.HistoricalPE.Median = nil
	tp := ComputeTargetPrice(TargetInput{CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03", Valuation: v})
	if tp.Status != InsufficientData || tp.Rule != RuleNone {
		t.Fatalf("tp = %+v", tp)
	}
}

// No published trailing P/E means no earnings base. The cumulative reported EPS is NOT used
// as a substitute: it covers a different period than an annual multiple does.
func TestNoTrailingPERefusesRatherThanAnnualising(t *testing.T) {
	v := histValuation(20, 40, 16, 22, 28)
	v.TrailingPE = nil
	v.TrailingStatus = Unavailable
	tp := ComputeTargetPrice(TargetInput{CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03", Valuation: v})

	if tp.Status != InsufficientData {
		t.Fatalf("status = %s", tp.Status)
	}
	if tp.EPS != nil {
		t.Fatal("no EPS may be reported without an earnings base")
	}
	if !strings.Contains(tp.Reason, "累計") || !strings.Contains(tp.Reason, "年化") {
		t.Fatalf("reason = %q — it should say why the reported EPS is not a substitute", tp.Reason)
	}
	for _, s := range tp.Scenarios {
		if s.TargetPrice != nil {
			t.Fatalf("%s produced a target with no earnings base", s.Name)
		}
	}
}

func TestNoCurrentPriceRefuses(t *testing.T) {
	tp := ComputeTargetPrice(TargetInput{
		CurrentPrice: nil,
		Valuation:    histValuation(20, 40, 16, 22, 28),
	})
	if tp.Status != Unavailable {
		t.Fatalf("status = %s", tp.Status)
	}
	if tp.CurrentPrice != nil || tp.EPS != nil {
		t.Fatalf("tp = %+v", tp)
	}
	for _, s := range tp.Scenarios {
		if s.TargetPrice != nil {
			t.Fatalf("%s produced a target with no price", s.Name)
		}
	}
}

// A zero price must not divide. ±Inf renders as a plausible-looking "+Inf%".
func TestZeroPriceIsDefensive(t *testing.T) {
	for _, price := range []float64{0, -5, math.NaN(), math.Inf(1)} {
		tp := ComputeTargetPrice(TargetInput{
			CurrentPrice: fp(price),
			Valuation:    histValuation(20, 40, 16, 22, 28),
		})
		if tp.Status == Available {
			t.Fatalf("price %v produced an available target", price)
		}
		for _, s := range tp.Scenarios {
			if s.UpsidePct != nil && (math.IsInf(*s.UpsidePct, 0) || math.IsNaN(*s.UpsidePct)) {
				t.Fatalf("price %v produced upside %v", price, *s.UpsidePct)
			}
		}
	}
}

func TestNonFiniteRatiosNeverProduceATarget(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, -3} {
		v := histValuation(20, 40, bad, 22, 28)
		tp := ComputeTargetPrice(TargetInput{CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03", Valuation: v})
		if tp.Status == Available {
			t.Fatalf("P25 = %v produced an available target", bad)
		}
	}
}

// ── the configured escape hatch ───────────────────────────────────────────────────────

func TestConfiguredRangeIsUsedOnlyWhenHistoryCannotBe(t *testing.T) {
	cfg := &PERange{Bear: 10, Base: 15, Bull: 20, Source: "產業區間，人工設定"}

	// History wins when it is usable.
	tp := ComputeTargetPrice(TargetInput{
		CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03", Valuation: histValuation(20, 40, 16, 22, 28), ConfiguredPE: cfg,
	})
	if tp.Rule != RuleHistoricalPercentile {
		t.Fatalf("rule = %s, history must take priority", tp.Rule)
	}

	// It is used only when history is not.
	thin := histValuation(20, 1, 16, 22, 28)
	thin.HistoricalPE.Status = InsufficientData
	tp = ComputeTargetPrice(TargetInput{
		CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03", Valuation: thin, ConfiguredPE: cfg,
	})
	if tp.Rule != RuleConfiguredRange || tp.Status != Available {
		t.Fatalf("tp = %+v", tp)
	}
	if !strings.Contains(tp.RuleDetail, "人工設定") {
		t.Fatalf("rule detail = %q, it must name its source", tp.RuleDetail)
	}
	if *byName(tp, ScenarioBase).TargetPrice != 750 {
		t.Fatalf("base = %v, want 50 × 15", *byName(tp, ScenarioBase).TargetPrice)
	}
}

func TestMisorderedOrUnusableConfiguredRangeIsIgnored(t *testing.T) {
	thin := histValuation(20, 1, 16, 22, 28)
	thin.HistoricalPE.Status = InsufficientData
	// Every case here carries a Source, so each is rejected on the defect it is named for
	// rather than incidentally failing the attribution check.
	for _, cfg := range []*PERange{
		{Bear: 20, Base: 15, Bull: 10, Source: "測試"}, // reversed
		{Bear: 0, Base: 15, Bull: 20, Source: "測試"},  // a zero multiple is not a multiple
		{Bear: 10, Base: 15, Bull: math.Inf(1), Source: "測試"},
		nil,
	} {
		tp := ComputeTargetPrice(TargetInput{CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03", Valuation: thin, ConfiguredPE: cfg})
		if tp.Rule != RuleNone || tp.Status == Available {
			t.Fatalf("cfg %+v was accepted: %+v", cfg, tp)
		}
	}
}

// ── margin of safety ──────────────────────────────────────────────────────────────────

func TestUpsidePct(t *testing.T) {
	for _, tc := range []struct {
		current, target float64
		want            float64
		ok              bool
	}{
		{100, 120, 20, true},
		{100, 80, -20, true},
		{100, 100, 0, true},
		{0, 120, 0, false},
		{-10, 120, 0, false},
		{math.NaN(), 120, 0, false},
		{math.Inf(1), 120, 0, false},
		{100, math.NaN(), 0, false},
		{100, math.Inf(1), 0, false},
	} {
		got, ok := UpsidePct(tc.current, tc.target)
		if ok != tc.ok {
			t.Fatalf("UpsidePct(%v,%v) ok = %v, want %v", tc.current, tc.target, ok, tc.ok)
		}
		if ok && math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("UpsidePct(%v,%v) = %v, want %v", tc.current, tc.target, got, tc.want)
		}
	}
}

// ── purity ────────────────────────────────────────────────────────────────────────────

// Same inputs, same output — twice, including the pointers' values.
func TestComputeTargetPriceIsPure(t *testing.T) {
	in := TargetInput{Symbol: "2330.TW", CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03", Valuation: histValuation(20, 40, 16, 22, 28)}
	a := ComputeTargetPrice(in)
	b := ComputeTargetPrice(in)
	if *a.EPS != *b.EPS || len(a.Scenarios) != len(b.Scenarios) {
		t.Fatal("not pure")
	}
	for i := range a.Scenarios {
		if *a.Scenarios[i].TargetPrice != *b.Scenarios[i].TargetPrice {
			t.Fatalf("scenario %d differs", i)
		}
	}
	// The input's own pointers must not have been written through.
	if *in.CurrentPrice != 1000 || *in.Valuation.TrailingPE != 20 {
		t.Fatal("ComputeTargetPrice mutated its input")
	}
}

// A forward-earnings target is not merely absent; it is unsourceable, and the result says so.
func TestForwardTargetIsExplicitlyNotImplemented(t *testing.T) {
	tp := ComputeTargetPrice(TargetInput{CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03", Valuation: histValuation(20, 40, 16, 22, 28)})
	if tp.ForwardStatus != NotImplemented {
		t.Fatalf("ForwardStatus = %s", tp.ForwardStatus)
	}
}

// ── the EPS base session (the lag defect) ─────────────────────────────────────────────

// The EPS must be recovered against the session its P/E was computed FOR, not against the
// analysis date.
//
// The exchanges publish these ratios days late, so the two are routinely different sessions.
// Dividing the newer close by the older P/E returns the true EPS scaled by whatever the price
// did in between — a figure that never existed, wearing the exchange's name.
func TestEPSIsRecoveredAgainstThePERatioSession(t *testing.T) {
	v := histValuation(40, 25, 16, 22, 28) // trailing P/E 40, 25 samples
	// The P/E was computed for a session that closed at 800. The stock has since run to 1000.
	tp := ComputeTargetPrice(TargetInput{
		Symbol: "2330.TW", AsOf: "2026-09-04",
		CurrentPrice: fp(1000),
		EPSBasePrice: fp(800), EPSBaseDate: "2026-08-28",
		Valuation: v,
	})
	if tp.Status != Available {
		t.Fatalf("status = %s (%s)", tp.Status, tp.Reason)
	}
	// 800 / 40 = 20. Using the current price would have given 25 — a 25% overstatement of
	// the company's earnings, and of every target built on them.
	if tp.EPS == nil || *tp.EPS != 20 {
		t.Fatalf("EPS = %v, want 800/40 = 20 (25 means the analysis-date close was used)", tp.EPS)
	}
	if tp.EPSBasePrice == nil || *tp.EPSBasePrice != 800 {
		t.Fatalf("EPSBasePrice = %v, want the 800 the division actually used", tp.EPSBasePrice)
	}
	if tp.EPSBaseDate != "2026-08-28" {
		t.Fatalf("EPSBaseDate = %q, want the P/E's own session", tp.EPSBaseDate)
	}
	// Base target = 20 × 22 = 440.
	base := scenarioByName(t, tp, ScenarioBase)
	if base.TargetPrice == nil || *base.TargetPrice != 440 {
		t.Fatalf("base target = %v, want 20 × 22 = 440", base.TargetPrice)
	}
	// Upside is measured from what a buyer pays NOW: (440 − 1000)/1000 = −56%.
	if base.UpsidePct == nil || math.Abs(*base.UpsidePct-(-56)) > 1e-9 {
		t.Fatalf("base upside = %v, want −56%% off the current 1000", base.UpsidePct)
	}
}

// With no close for the P/E's session there is no way to recover the earnings base, and the
// engine must refuse rather than reach for the nearest price it has.
func TestNoCloseForThePESessionRefuses(t *testing.T) {
	v := histValuation(40, 25, 16, 22, 28)
	for _, bad := range []*float64{nil, fp(0), fp(-5)} {
		tp := ComputeTargetPrice(TargetInput{
			CurrentPrice: fp(1000), EPSBasePrice: bad, EPSBaseDate: "2026-08-28", Valuation: v,
		})
		if tp.Status == Available {
			t.Fatalf("base price %v produced a target", bad)
		}
		if tp.EPS != nil {
			t.Fatalf("an EPS was produced with no base price: %v", *tp.EPS)
		}
		for _, sc := range tp.Scenarios {
			if sc.TargetPrice != nil {
				t.Fatalf("%s produced a target price", sc.Name)
			}
		}
		if tp.Reason == "" {
			t.Fatalf("refused with no reason")
		}
	}
}

// ── per-scenario provenance ───────────────────────────────────────────────────────────

// Every scenario row must answer, alone: which EPS, from where, times what multiple, under
// which rule, over how many samples, as of when. A row lifted out of the page carries its
// own justification or it is just a number.
func TestEveryScenarioCarriesItsOwnProvenance(t *testing.T) {
	tp := ComputeTargetPrice(TargetInput{
		Symbol: "2330.TW", AsOf: "2026-09-04",
		CurrentPrice: fp(1000), EPSBasePrice: fp(800), EPSBaseDate: "2026-08-28",
		Valuation: histValuation(40, 25, 16, 22, 28),
	})
	if tp.Status != Available {
		t.Fatalf("status = %s (%s)", tp.Status, tp.Reason)
	}
	if len(tp.Scenarios) != 3 {
		t.Fatalf("scenarios = %d, want 3", len(tp.Scenarios))
	}
	for _, sc := range tp.Scenarios {
		if sc.EPS == nil {
			t.Errorf("%s: no EPS value", sc.Name)
		}
		if sc.EPSBasis != EPSTrailingImplied {
			t.Errorf("%s: EPSBasis = %q, want the kind of earnings named", sc.Name, sc.EPSBasis)
		}
		if sc.EPSSource == "" || !strings.Contains(sc.EPSSource, "2026-08-28") {
			t.Errorf("%s: EPSSource = %q, want it to name the session it divided", sc.Name, sc.EPSSource)
		}
		if sc.PEMultiple == nil {
			t.Errorf("%s: no PE multiple", sc.Name)
		}
		if sc.Rule != RuleHistoricalPercentile {
			t.Errorf("%s: Rule = %q", sc.Name, sc.Rule)
		}
		if sc.RuleDetail == "" {
			t.Errorf("%s: no rule detail — the multiple has no justification on this row", sc.Name)
		}
		if sc.SampleCount != 25 {
			t.Errorf("%s: SampleCount = %d, want 25", sc.Name, sc.SampleCount)
		}
		if sc.AsOf != "2026-09-04" {
			t.Errorf("%s: AsOf = %q", sc.Name, sc.AsOf)
		}
		if sc.TargetPrice == nil {
			t.Errorf("%s: no target price", sc.Name)
		}
	}
}

// ── no arbitrary multiple, anywhere ───────────────────────────────────────────────────

// Every multiple the engine ever emits must come from the stock's own distribution or from a
// range a human wrote down. There is no third source, and in particular no built-in number.
//
// The check is structural: with no history and no configured range, NOTHING may be produced,
// whatever the price or the trailing P/E.
func TestNoMultipleIsEverInvented(t *testing.T) {
	for _, samples := range []int{0, 1, 5, 19} {
		thin := histValuation(40, samples, 16, 22, 28)
		thin.HistoricalPE.Status = InsufficientData
		tp := ComputeTargetPrice(TargetInput{
			CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03",
			Valuation: thin,
		})
		if tp.Status == Available {
			t.Fatalf("%d samples produced a target", samples)
		}
		if tp.Rule != RuleNone {
			t.Fatalf("%d samples: rule = %q, want NONE", samples, tp.Rule)
		}
		for _, sc := range tp.Scenarios {
			if sc.PEMultiple != nil {
				t.Fatalf("%d samples: %s got a multiple of %v out of nowhere",
					samples, sc.Name, *sc.PEMultiple)
			}
		}
		if !strings.Contains(tp.Reason, itoa(samples)) {
			t.Errorf("%d samples: reason %q does not name the count", samples, tp.Reason)
		}
	}
}

// A configured range must name where it came from, and an anonymous one is still traceable
// to the fact that a human entered it.
func TestConfiguredRangeNamesItsSource(t *testing.T) {
	thin := histValuation(40, 1, 16, 22, 28)
	thin.HistoricalPE.Status = InsufficientData
	tp := ComputeTargetPrice(TargetInput{
		CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03",
		Valuation:    thin,
		ConfiguredPE: &PERange{Bear: 15, Base: 20, Bull: 25, Source: "2026 產業區間，研究員 A"},
	})
	if tp.Status != Available {
		t.Fatalf("status = %s (%s)", tp.Status, tp.Reason)
	}
	if tp.Rule != RuleConfiguredRange {
		t.Fatalf("rule = %q", tp.Rule)
	}
	if !strings.Contains(tp.RuleDetail, "研究員 A") {
		t.Fatalf("RuleDetail = %q, want the configured source named", tp.RuleDetail)
	}
	for _, sc := range tp.Scenarios {
		if !strings.Contains(sc.RuleDetail, "研究員 A") {
			t.Errorf("%s: the row does not carry the source of its multiple", sc.Name)
		}
	}
}

func scenarioByName(t *testing.T, tp TargetPrice, name string) TargetScenario {
	t.Helper()
	for _, sc := range tp.Scenarios {
		if sc.Name == name {
			return sc
		}
	}
	t.Fatalf("no %s scenario", name)
	return TargetScenario{}
}

// A configured range with no attribution is an invented multiple with a rule name attached,
// and must be refused rather than quietly given a default source.
//
// Nothing wires ConfiguredPE today, so this is a guard for the moment config reaches it: the
// value of the whole rule system is that every multiple traces to evidence, and "configured"
// by an unnamed party is not evidence.
func TestAnonymousConfiguredRangeIsRefused(t *testing.T) {
	thin := histValuation(20, 1, 16, 22, 28)
	thin.HistoricalPE.Status = InsufficientData
	for _, src := range []string{"", "   ", "\t"} {
		tp := ComputeTargetPrice(TargetInput{
			CurrentPrice: fp(1000), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03",
			Valuation:    thin,
			ConfiguredPE: &PERange{Bear: 15, Base: 20, Bull: 25, Source: src},
		})
		if tp.Status == Available {
			t.Fatalf("source %q produced a target", src)
		}
		if tp.Rule != RuleNone {
			t.Fatalf("source %q: rule = %q, want NONE", src, tp.Rule)
		}
		for _, sc := range tp.Scenarios {
			if sc.PEMultiple != nil {
				t.Fatalf("source %q: %s got a multiple of %v", src, sc.Name, *sc.PEMultiple)
			}
		}
	}
}

// A zero close is not a price, and must not survive onto the block as an available figure.
func TestZeroCurrentPriceIsNotCarried(t *testing.T) {
	tp := ComputeTargetPrice(TargetInput{
		CurrentPrice: fp(0), EPSBasePrice: fp(1000), EPSBaseDate: "2026-09-03",
		Valuation: histValuation(20, 40, 16, 22, 28),
	})
	if tp.CurrentPrice != nil {
		t.Fatalf("CurrentPrice = %v, want nil — 0 is not a price", *tp.CurrentPrice)
	}
	if tp.Status == Available {
		t.Fatalf("status = AVAILABLE with no price")
	}
}
