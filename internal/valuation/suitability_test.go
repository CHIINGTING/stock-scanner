package valuation

import "testing"

// Model suitability, and above all its INDEPENDENCE from the three layers below it.
//
// The failure this file exists to prevent is not a wrong verdict — it is a quiet collapse:
// suitability becoming a second name for data quality, or a threshold on the P/E level, or a
// reaction to a target that looked too good. Each of those would be invisible in production
// (the verdicts would all look plausible) and would destroy the only thing the layer adds.

func shape(valid []bool) []sessionPE {
	out := make([]sessionPE, 0, len(valid))
	d := "2025-10-01"
	for _, ok := range valid {
		var pe *float64
		if ok {
			v := 20.0
			pe = &v
		}
		out = append(out, sessionPE{date: d, pe: pe})
		d = shiftDate(d, 1)
	}
	return out
}

func repeat(v bool, n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// ── the availability shape, which carries most of the weight ──────────────────────────

func TestPEAvailabilityShapeIsStructural(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid []bool
		want  PEPersistence
	}{
		{"every session had a P/E", repeat(true, 225), PEPersistent},
		{"no session ever had one", repeat(false, 225), PENever},
		{"the 2337 shape: 199 without, then 26 with",
			append(repeat(false, 199), repeat(true, 26)...), PERecentOnly},
		{"one gap at the very start",
			append(repeat(false, 1), repeat(true, 224)...), PERecentOnly},
		{"profit, then loss — the transition runs the other way",
			append(repeat(true, 100), repeat(false, 125)...), PEIntermittent},
		{"crosses zero twice", append(append(repeat(true, 50), repeat(false, 50)...),
			repeat(true, 50)...), PEIntermittent},
		// UNKNOWN, not NEVER. NEVER is an observation — we watched and the exchange
		// published nothing; UNKNOWN is the absence of one. Conflating them lets a stock
		// nobody has fetched be described as one the exchange declined to rate.
		{"nothing archived", nil, PEUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PEAvailabilityShape(shape(tc.valid)); got != tc.want {
				t.Fatalf("shape = %s, want %s", got, tc.want)
			}
		})
	}
}

// RECENT_ONLY means EVERY gap precedes EVERY observation. A single later gap breaks that, and
// the difference matters: one transition is a company that turned profitable, repeated
// transitions are a denominator that will not stay still.
func TestOneLateGapMakesItIntermittentNotRecentOnly(t *testing.T) {
	v := append(repeat(false, 199), repeat(true, 26)...)
	v[220] = false
	if got := PEAvailabilityShape(shape(v)); got != PEIntermittent {
		t.Fatalf("shape = %s, want INTERMITTENT", got)
	}
}

// ── classification ────────────────────────────────────────────────────────────────────

func in(sign EarningsSign, p PEPersistence, window int) SuitabilityInput {
	return SuitabilityInput{Earnings: sign, Persistence: p, WindowSessions: window}
}

func TestSuitabilityTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   SuitabilityInput
		want Suitability
		//

		// reason is one code the verdict must carry, to pin WHY as well as WHAT.
		reason SuitabilityReason
	}{
		{"filed profit, unbroken P/E, a full year",
			in(EarningsPositive, PEPersistent, 225), SuitabilitySuitable,
			ReasonPositiveEligibilityPersistent},
		{"filed loss AND no P/E across a full year — two sources agreeing",
			in(EarningsNonPositive, PENever, 225), SuitabilityUnsuitable,
			ReasonNoPositiveTrailing},
		{"filed loss AND no P/E, but only a handful of sessions watched",
			in(EarningsNonPositive, PENever, 20), SuitabilityInsufficientData,
			ReasonInsufficientObservation},
		{"no P/E ever, no filing to say why",
			in(EarningsUnknown, PENever, 225), SuitabilityInsufficientData,
			ReasonPENeverPublished},
		{"no P/E ever, but the filing shows a PROFIT — the two disagree",
			in(EarningsPositive, PENever, 225), SuitabilityInsufficientData,
			ReasonPENeverPublished},
		{"the 2337 shape: one loss-to-profit transition",
			in(EarningsPositive, PERecentOnly, 225), SuitabilityWeak,
			ReasonPEAvailableOnlyRecently},
		{"denominator crosses zero repeatedly",
			in(EarningsPositive, PEIntermittent, 225), SuitabilityConditional,
			ReasonPEAvailabilityIntermit},
		{"unbroken P/E, but observed under one reporting cycle",
			in(EarningsPositive, PEPersistent, 40), SuitabilityConditional,
			ReasonInsufficientEarningsHist},
		{"unbroken P/E, filing turned negative",
			in(EarningsNonPositive, PEPersistent, 225), SuitabilityConditional,
			ReasonEarningsSignChange},
		{"unbroken P/E, no filing archived",
			in(EarningsUnknown, PEPersistent, 225), SuitabilityConditional,
			ReasonEarningsBasisUnknown},
		{"exactly one reporting cycle of observation",
			in(EarningsPositive, PEPersistent, MinSuitabilityWindowSessions),
			SuitabilitySuitable, ReasonPositiveEligibilityPersistent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := ClassifySuitability(tc.in)
			if v.Suitability != tc.want {
				t.Fatalf("suitability = %s, want %s (reasons %v)",
					v.Suitability, tc.want, v.Reasons)
			}
			if !hasReason(v, tc.reason) {
				t.Fatalf("reasons = %v, want one of them to be %s", v.Reasons, tc.reason)
			}
		})
	}
}

func hasReason(v SuitabilityVerdict, r SuitabilityReason) bool {
	for _, x := range v.Reasons {
		if x == r {
			return true
		}
	}
	return false
}

// The rule §11 exists for. "The exchange published no P/E" is consistent with a suspension, a
// listing change or a dataset gap; concluding a loss from silence would be inventing a
// financial fact this repo has not observed.
func TestNoPublishedPERatioAloneNeverProvesALoss(t *testing.T) {
	v := ClassifySuitability(in(EarningsUnknown, PENever, 225))

	if v.Suitability == SuitabilityUnsuitable {
		t.Fatal("an absent P/E alone produced UNSUITABLE — that asserts the company is " +
			"loss-making on no financial evidence at all")
	}
	if hasReason(v, ReasonNoPositiveTrailing) {
		t.Fatalf("reasons = %v — NO_POSITIVE_TRAILING_EARNINGS was claimed without a filing",
			v.Reasons)
	}
}

// ── what must NEVER decide a verdict ──────────────────────────────────────────────────

// §7. 3037 欣興: 225 of 225 sessions, median around 120x. The multiple is not an input here at
// ALL — the type system says so, since SuitabilityInput carries no P/E level — and this test
// exists so that stays true.
func TestAHighPERatioCannotProduceUnsuitable(t *testing.T) {
	stable := in(EarningsPositive, PEPersistent, 225)
	for _, median := range []float64{8, 25, 120, 400, 5000} {
		h := HistoricalPEStats{Status: Available, Median: &median,
			P25: ptr(median * 0.8), P75: ptr(median * 1.2)}
		d := ComputeDispersion(h)
		w := stable
		w.RelativeIQR = d.RelativeIQR

		v := ClassifySuitability(w)
		if v.Suitability != SuitabilitySuitable {
			t.Fatalf("median %vx produced %s — a level is a property of the NUMERATOR and "+
				"cannot say anything about the earnings denominator", median, v.Suitability)
		}
	}
}

// §8. 6274 台燿: relative IQR ≈ 0.91 on a flawless archive. A wide distribution is a re-rating
// the archive captured correctly. It may attach a caution; it may never decide.
func TestAWideDistributionCannotProduceUnsuitable(t *testing.T) {
	for _, rel := range []float64{0.0, 0.5, 0.91, 3.0, 50.0} {
		w := in(EarningsPositive, PEPersistent, 225)
		w.RelativeIQR = &rel

		v := ClassifySuitability(w)
		if v.Suitability != SuitabilitySuitable {
			t.Fatalf("relative IQR %v produced %s", rel, v.Suitability)
		}
		if rel >= WideRegimeRelativeIQR && !hasReason(v, ReasonValuationRegimeWide) {
			t.Fatalf("relative IQR %v attached no caution: %v", rel, v.Reasons)
		}
	}
}

// §9. 1815 富喬 has printed a base case implying the price should double. A large upside is
// what a low current multiple against the stock's own distribution arithmetically produces —
// reasoning from it back to the model's validity is reasoning backwards.
func TestAnExtremeTargetUpsideCannotProduceUnsuitable(t *testing.T) {
	for _, upside := range []float64{-99, 0, 99, 103.3, 900} {
		w := in(EarningsPositive, PEPersistent, 225)
		w.BaseUpsidePct = &upside

		v := ClassifySuitability(w)
		if v.Suitability != SuitabilitySuitable {
			t.Fatalf("upside %v%% produced %s", upside, v.Suitability)
		}
		if abs(upside) >= SensitiveTargetUpsidePct && !hasReason(v, ReasonTargetSensitivityCheck) {
			t.Fatalf("upside %v%% attached no caution: %v", upside, v.Reasons)
		}
	}
}

// The general form of the three tests above: a caution NEVER changes a class. Every verdict,
// with and without both cautions at their most extreme, must be identical.
func TestCautionsNeverChangeTheVerdict(t *testing.T) {
	wide, huge := 99.0, 10000.0
	for _, base := range []SuitabilityInput{
		in(EarningsPositive, PEPersistent, 225),
		in(EarningsPositive, PERecentOnly, 225),
		in(EarningsPositive, PEIntermittent, 225),
		in(EarningsNonPositive, PENever, 225),
		in(EarningsUnknown, PENever, 225),
		in(EarningsPositive, PEPersistent, 30),
	} {
		plain := ClassifySuitability(base)
		loud := base
		loud.RelativeIQR, loud.BaseUpsidePct = &wide, &huge

		if got := ClassifySuitability(loud); got.Suitability != plain.Suitability {
			t.Fatalf("%s/%s: verdict moved from %s to %s once cautions applied",
				base.Earnings, base.Persistence, plain.Suitability, got.Suitability)
		}
	}
}

// ── determinism and versioning ────────────────────────────────────────────────────────

func TestEveryVerdictCarriesItsRuleVersion(t *testing.T) {
	for _, i := range []SuitabilityInput{
		in(EarningsPositive, PEPersistent, 225),
		in(EarningsNonPositive, PENever, 225),
		{},
	} {
		if v := ClassifySuitability(i); v.RuleVersion != SuitabilityRuleVersion {
			t.Fatalf("rule version = %q, want %q", v.RuleVersion, SuitabilityRuleVersion)
		}
	}
}

// A zero input must not silently become a positive claim.
//
// The fully-zero case is absorbed by rule 0's persistence guard, which is why it was masking a
// real defect: the sub-tests below hold a VALID persistence and leave only the earnings sign
// zero, so the classification actually has to decide something.
func TestAnEmptyInputIsNeverSuitable(t *testing.T) {
	v := ClassifySuitability(SuitabilityInput{})
	if v.Suitability == SuitabilitySuitable {
		t.Fatal("no evidence at all produced SUITABLE")
	}
	if v.Earnings != EarningsUnknown {
		t.Fatalf("earnings sign = %q, want UNKNOWN", v.Earnings)
	}
}

// A zero-valued earnings sign must behave exactly like an explicit UNKNOWN.
//
// It did not. Normalisation was applied to the reported copy while every branch tested the
// input, so a zero sign matched nothing, fell through to the default and produced SUITABLE
// asserting POSITIVE_EARNINGS_BASIS_AVAILABLE — a filed positive basis that does not exist —
// on a verdict that simultaneously reported the sign as UNKNOWN. A verdict contradicting its
// own evidence is worse than a wrong verdict, because nothing downstream can detect it.
func TestAZeroEarningsSignIsTreatedAsUnknown(t *testing.T) {
	for _, p := range []PEPersistence{PEPersistent, PERecentOnly, PEIntermittent, PENever} {
		zero := ClassifySuitability(SuitabilityInput{Persistence: p, WindowSessions: 225})
		explicit := ClassifySuitability(in(EarningsUnknown, p, 225))

		if zero.Suitability != explicit.Suitability {
			t.Fatalf("%s: zero sign → %s, explicit UNKNOWN → %s; they must be identical",
				p, zero.Suitability, explicit.Suitability)
		}
		if len(zero.Reasons) != len(explicit.Reasons) {
			t.Fatalf("%s: reasons differ — zero %v vs explicit %v",
				p, zero.Reasons, explicit.Reasons)
		}
		if zero.Earnings != EarningsUnknown {
			t.Fatalf("%s: reported sign = %q, want UNKNOWN", p, zero.Earnings)
		}
		// The specific contradiction: claiming a filed positive basis while reporting the
		// sign as unknown.
		if hasReason(zero, ReasonPositiveEarningsBasis) {
			t.Fatalf("%s: reasons = %v — POSITIVE_EARNINGS_BASIS_AVAILABLE asserted on a "+
				"verdict whose own earnings sign is UNKNOWN", p, zero.Reasons)
		}
	}
}

// Every verdict must report the sign it actually decided on. A verdict whose stated evidence
// differs from the evidence it used cannot be audited afterwards.
func TestTheReportedSignIsTheOneUsed(t *testing.T) {
	for _, sign := range []EarningsSign{EarningsPositive, EarningsNonPositive,
		EarningsUnknown, ""} {
		for _, p := range []PEPersistence{PEPersistent, PERecentOnly, PEIntermittent,
			PENever, PEUnknown} {
			v := ClassifySuitability(SuitabilityInput{Earnings: sign, Persistence: p,
				WindowSessions: 225})
			want := sign
			if want == "" {
				want = EarningsUnknown
			}
			if v.Earnings != want {
				t.Fatalf("input %q/%s reported sign %q, want %q", sign, p, v.Earnings, want)
			}
		}
	}
}

func TestClassificationIsDeterministic(t *testing.T) {
	i := in(EarningsPositive, PERecentOnly, 225)
	first := ClassifySuitability(i)
	for n := 0; n < 50; n++ {
		got := ClassifySuitability(i)
		if got.Suitability != first.Suitability || len(got.Reasons) != len(first.Reasons) {
			t.Fatalf("run %d differed: %+v vs %+v", n, got, first)
		}
	}
}

// ── an empty archive is not a silent exchange ─────────────────────────────────────────
//
// The mirror image of TestNoPublishedPERatioAloneNeverProvesALoss. There, silence was not
// allowed to become a loss. Here, a NON-OBSERVATION must not become silence — a stock nobody
// has fetched must not be described as one the exchange declined to publish a ratio for.
//
// Reachable in practice: 3037 欣興 held a valuation archive and no fundamental one throughout
// this work item, and any newly added stock reaches the reverse.
func TestNothingArchivedIsNeverUnsuitable(t *testing.T) {
	for _, sign := range []EarningsSign{EarningsNonPositive, EarningsPositive, EarningsUnknown} {
		v := ClassifySuitability(SuitabilityInput{Earnings: sign, Persistence: PEUnknown})

		if v.Suitability != SuitabilityInsufficientData {
			t.Fatalf("earnings %s with an empty archive → %s, want INSUFFICIENT_DATA",
				sign, v.Suitability)
		}
		if hasReason(v, ReasonPENeverPublished) {
			t.Fatalf("earnings %s: reasons = %v — PE_NEVER_PUBLISHED_IN_WINDOW claims the "+
				"exchange published nothing, about an exchange this repo never asked",
				sign, v.Reasons)
		}
		if !hasReason(v, ReasonNoArchivedSessions) {
			t.Fatalf("earnings %s: reasons = %v, want NO_ARCHIVED_SESSIONS", sign, v.Reasons)
		}
	}
}

// A zero WindowSessions must reach the same place even if a caller hands over a shape.
func TestAZeroSessionWindowIsNeverJudged(t *testing.T) {
	v := ClassifySuitability(SuitabilityInput{
		Earnings: EarningsNonPositive, Persistence: PENever, WindowSessions: 0})

	if v.Suitability != SuitabilityInsufficientData {
		t.Fatalf("suitability = %s over zero sessions, want INSUFFICIENT_DATA (%v)",
			v.Suitability, v.Reasons)
	}
	if v.Persistence != PEUnknown {
		t.Fatalf("persistence = %s, want UNKNOWN — NEVER over zero sessions is a claim about "+
			"an exchange that was never queried", v.Persistence)
	}
}

// ── the window threshold, pinned from BOTH sides ──────────────────────────────────────
//
// Expressed against the constant AND against literals. Against the constant alone, moving the
// constant moves the test with it and the value is pinned only from below: raising 60 to 150
// would break nothing.
func TestTheReportingCycleThresholdIsPinnedFromBothSides(t *testing.T) {
	base := func(window int) SuitabilityInput {
		return in(EarningsPositive, PEPersistent, window)
	}
	// One session below the constant: not yet demonstrated.
	if got := ClassifySuitability(base(MinSuitabilityWindowSessions - 1)).Suitability; got != SuitabilityConditional {
		t.Fatalf("%d sessions → %s, want CONDITIONAL", MinSuitabilityWindowSessions-1, got)
	}
	// Exactly at it: demonstrated.
	if got := ClassifySuitability(base(MinSuitabilityWindowSessions)).Suitability; got != SuitabilitySuitable {
		t.Fatalf("%d sessions → %s, want SUITABLE", MinSuitabilityWindowSessions, got)
	}
	// And against literals, so the VALUE is pinned rather than only its role. One reporting
	// cycle is ~60 sessions; a rule that demanded a half-year or a year of observation before
	// calling an unbroken P/E established would be a different, stricter claim than the one
	// documented — and would silently reclassify most of the universe.
	if got := ClassifySuitability(base(59)).Suitability; got != SuitabilityConditional {
		t.Fatalf("59 sessions → %s, want CONDITIONAL", got)
	}
	if got := ClassifySuitability(base(60)).Suitability; got != SuitabilitySuitable {
		t.Fatalf("60 sessions → %s, want SUITABLE — the threshold is one reporting cycle "+
			"(~21 sessions a month), not a half-year", got)
	}
	if got := ClassifySuitability(base(120)).Suitability; got != SuitabilitySuitable {
		t.Fatalf("120 sessions → %s, want SUITABLE", got)
	}
}

// ── point in time ─────────────────────────────────────────────────────────────────────
//
// Persistence is now a DECISION INPUT to a verdict that gets persisted, so a future session
// leaking into it would silently rewrite history: a stock that later turns profitable would
// read RECENT_ONLY — or PERSISTENT — in a check dated before it did.
//
// FU-9 has this guard for its own metrics. The shape needs its own, because it is computed
// from the same slice by a different call and a refactor could hand it the raw history.
func TestPersistenceRespectsTheAsOfBound(t *testing.T) {
	pe := 20.0
	// Loss first, profit later. A check dated inside the loss stretch must see NEVER; one
	// dated after the transition must see RECENT_ONLY.
	var rows []Ratios
	d := "2026-01-01"
	for i := 0; i < 40; i++ {
		rows = append(rows, Ratios{Symbol: "T", Date: d, ArchiveDate: d})
		d = shiftDate(d, 1)
	}
	for i := 0; i < 40; i++ {
		rows = append(rows, Ratios{Symbol: "T", Date: d, PERatio: &pe, ArchiveDate: d})
		d = shiftDate(d, 1)
	}
	transition := rows[40].Date

	before := Calculate(Input{Symbol: "T", History: rows,
		AsOfDate: shiftDate(transition, -1)}).HistoricalPE
	after := Calculate(Input{Symbol: "T", History: rows, AsOfDate: rows[len(rows)-1].Date}).HistoricalPE

	if before.Persistence != PENever {
		t.Fatalf("a check dated before the company turned profitable saw %s — sessions after "+
			"asOf leaked into the shape, which would rewrite every historical verdict",
			before.Persistence)
	}
	if after.Persistence != PERecentOnly {
		t.Fatalf("a check dated after the transition saw %s, want RECENT_ONLY",
			after.Persistence)
	}
	// And the same bound on the ARCHIVE date: a session observed later, however old the
	// session itself is, must not reach an earlier check.
	late := append([]Ratios{}, rows[:40]...)
	late = append(late, Ratios{Symbol: "T", Date: rows[10].Date, PERatio: &pe,
		ArchiveDate: "2026-12-31"})
	got := Calculate(Input{Symbol: "T", History: late,
		AsOfDate: rows[39].Date}).HistoricalPE.Persistence
	if got != PENever {
		t.Fatalf("shape = %s — a correction archived on 2026-12-31 reached a check dated %s",
			got, rows[39].Date)
	}
}

// ── an acquisition gap is not a financial fact ────────────────────────────────────────
//
// The single most dangerous confusion available to this layer. A session this repo never
// fetched must never read as a session on which the exchange declined to publish a ratio —
// the first is a hole in our data, the second is a statement about the company's earnings.
//
// Safe by construction rather than by check: a stock absent from a whole-market response is
// archived with Ratios nil, so there is no row, so the session is simply not in the series.
// "By construction" is exactly the kind of safety that quietly stops being true, hence this.
func TestAnArchiveGapDoesNotBecomeAnEarningsTransition(t *testing.T) {
	pe := 20.0
	// P P [three sessions never fetched] P P — the gap leaves NO rows at all.
	var rows []Ratios
	d := "2026-08-03"
	for i := 0; i < 5; i++ {
		if i == 2 {
			d = shiftDate(d, 3) // the unfetched stretch
			continue
		}
		rows = append(rows, Ratios{Symbol: "T", Date: d, PERatio: &pe, ArchiveDate: d})
		d = shiftDate(d, 1)
	}

	if got := PEAvailabilityShape(eligibleSessions(rows, "", 0)); got != PEPersistent {
		t.Fatalf("shape = %s, want PERSISTENT — an acquisition gap was read as a stretch in "+
			"which the exchange published no P/E, which is a claim about the company's "+
			"earnings that nobody observed", got)
	}
}

// The same thing one layer down: the gap must not even produce a session.
func TestAnUnfetchedSessionProducesNoRow(t *testing.T) {
	pe := 20.0
	rows := []Ratios{
		{Symbol: "T", Date: "2026-08-03", PERatio: &pe},
		{Symbol: "T", Date: "2026-08-07", PERatio: &pe},
	}
	got := eligibleSessions(rows, "", 0)
	if len(got) != 2 {
		t.Fatalf("%d sessions from 2 archived records — the days between them were "+
			"materialised out of nothing", len(got))
	}
	for _, s := range got {
		if s.pe == nil {
			t.Fatalf("session %s has no P/E; only archived records may appear", s.date)
		}
	}
}

// ── RECENT_ONLY is an ORDERING, not a recency window ──────────────────────────────────
//
// The exact patterns the contract names. A rule that looked only at where the valid samples
// END, or at how recent they are, would call the second one RECENT_ONLY too — and would then
// describe a denominator that crossed zero three times as a single clean turnaround.
func TestRecentOnlyIsAnOrderingProperty(t *testing.T) {
	n, p := false, true
	for _, tc := range []struct {
		name    string
		pattern []bool
		want    PEPersistence
	}{
		{"N N N P P P", []bool{n, n, n, p, p, p}, PERecentOnly},
		{"N N P P N P", []bool{n, n, p, p, n, p}, PEIntermittent},
		{"P P P N N N", []bool{p, p, p, n, n, n}, PEIntermittent},
		{"N P N P N P", []bool{n, p, n, p, n, p}, PEIntermittent},
		{"N P P P P P", []bool{n, p, p, p, p, p}, PERecentOnly},
		{"P P P P P N", []bool{p, p, p, p, p, n}, PEIntermittent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PEAvailabilityShape(shape(tc.pattern)); got != tc.want {
				t.Fatalf("%s → %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// ── the observation floor on a NEVER conclusion ───────────────────────────────────────
//
// UNSUITABLE is the only verdict asserting a company-level fact, so it is the only one needing
// a floor under how much observation stands behind it. A handful of sessions with no published
// P/E is not a year of silence, and the stored record must be able to tell them apart.
func TestAShortNeverWindowCannotReachUnsuitable(t *testing.T) {
	for _, window := range []int{1, 5, 20, MinSuitabilityWindowSessions - 1} {
		v := ClassifySuitability(in(EarningsNonPositive, PENever, window))

		if v.Suitability != SuitabilityInsufficientData {
			t.Fatalf("%d sessions with no P/E plus a filed loss → %s; %d sessions of silence "+
				"is not the same evidence as a year of it", window, v.Suitability, window)
		}
		if !hasReason(v, ReasonInsufficientObservation) {
			t.Fatalf("%d sessions: reasons = %v, want INSUFFICIENT_OBSERVATION_WINDOW",
				window, v.Reasons)
		}
	}
	// At the floor, with both sources agreeing, the conclusion is available.
	v := ClassifySuitability(in(EarningsNonPositive, PENever, MinSuitabilityWindowSessions))
	if v.Suitability != SuitabilityUnsuitable {
		t.Fatalf("%d sessions → %s, want UNSUITABLE", MinSuitabilityWindowSessions, v.Suitability)
	}
}

// The floor is NOT bypassed by the filing. The filing is independent evidence and bypassing
// would be defensible — but it would be a different rule, concluding from a statement rather
// than from a pattern, and folding it into NEVER would leave the stored verdict unable to say
// which evidence produced it.
func TestFiledEarningsDoNotBypassTheObservationFloor(t *testing.T) {
	short := ClassifySuitability(in(EarningsNonPositive, PENever, 10))
	long := ClassifySuitability(in(EarningsNonPositive, PENever, 225))

	if short.Suitability == long.Suitability {
		t.Fatalf("10 and 225 sessions both → %s; the same filing must not license the same "+
			"conclusion over vastly different observation", short.Suitability)
	}
	if hasReason(short, ReasonPENeverPublished) {
		t.Fatalf("reasons = %v — a 10-session window claimed the exchange never published "+
			"a ratio, which is the year-scale conclusion it has not earned", short.Reasons)
	}
}

// ── SUITABLE says less than it looks like it says ─────────────────────────────────────
//
// A P/E is published whenever the reference EPS is positive, so eligibility persistence sees
// nothing of MAGNITUDE. This is the concrete case: earnings collapsing by 95% while the
// pattern stays unbroken.
func TestSuitableDoesNotClaimStableEarnings(t *testing.T) {
	v := ClassifySuitability(in(EarningsPositive, PEPersistent, 225))

	if v.Suitability != SuitabilitySuitable {
		t.Fatalf("suitability = %s, want SUITABLE", v.Suitability)
	}
	if !hasReason(v, ReasonEarningsMagnitudeUnknown) {
		t.Fatalf("reasons = %v — SUITABLE must carry EARNINGS_MAGNITUDE_NOT_ESTABLISHED, "+
			"because a company whose TTM EPS fell 20 → 10 → 5 → 1 stays PERSISTENT the whole "+
			"way and would otherwise be reported as though its denominator were stable",
			v.Reasons)
	}
	// And the reason code itself must not be the one that was renamed away.
	for _, r := range v.Reasons {
		if r == "STABLE_POSITIVE_EARNINGS" {
			t.Fatal("the reason code claims stable earnings; the evidence is a sign-state " +
				"history, which cannot establish stability")
		}
	}
}

// A magnitude collapse is invisible to this layer, and that must be stated rather than
// discovered. The two verdicts below are indistinguishable, by construction.
func TestACollapsingButPositiveDenominatorIsIndistinguishable(t *testing.T) {
	healthy := ClassifySuitability(in(EarningsPositive, PEPersistent, 225))
	collapsing := ClassifySuitability(in(EarningsPositive, PEPersistent, 225))

	if healthy.Suitability != collapsing.Suitability {
		t.Fatal("the fixtures differ; they must not — the point is that FU11-v1 cannot " +
			"tell them apart")
	}
	if !hasReason(healthy, ReasonEarningsMagnitudeUnknown) {
		t.Fatal("nothing on the verdict says the magnitude is unknown, so a reader has no " +
			"way to learn that these two cases are the same to this layer")
	}
}
