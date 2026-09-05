package valuation

import (
	"math"
	"testing"
)

// Evidence quality, which is a different question from availability.
//
// The failure these tests exist to prevent is subtle and would be invisible in production: a
// coverage ratio computed as valid ÷ valid, which is 100% for every stock that ever existed
// and would have let 2337 旺宏 — 26 usable P/E values inside a 225-session window, all of them
// from one recent month — present itself as perfectly covered.

// session builds one archived session: a date, and a P/E the exchange either published or
// did not.
func session(date string, pe *float64) Ratios {
	return Ratios{Symbol: "TEST", Date: date, PERatio: pe}
}

// archive builds a run of consecutive daily sessions. valid says how many of them, counting
// from the END, carry a P/E — the shape a company that recently returned to profit leaves.
func archive(start string, sessions, validFromEnd int) []Ratios {
	out := make([]Ratios, 0, sessions)
	d := start
	for i := 0; i < sessions; i++ {
		var pe *float64
		if i >= sessions-validFromEnd {
			v := 20 + float64(i%7)
			pe = &v
		}
		out = append(out, session(d, pe))
		d = shiftDate(d, 1)
	}
	return out
}

func statsOf(t *testing.T, history []Ratios) HistoricalPEStats {
	t.Helper()
	return Calculate(Input{Symbol: "TEST", History: history}).HistoricalPE
}

// ── coverage ──────────────────────────────────────────────────────────────────────────

func TestCoverageIsValidSamplesOverArchivedSessions(t *testing.T) {
	for _, tc := range []struct {
		name             string
		sessions, valid  int
		wantCov          float64
		wantValid, wantW int
	}{
		{"every session published a P/E", 225, 225, 1.0, 225, 225},
		{"the 2337 shape", 225, 26, 26.0 / 225.0, 26, 225},
		{"half", 200, 100, 0.5, 100, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := statsOf(t, archive("2025-10-01", tc.sessions, tc.valid)).Quality
			if q.ValidSamples != tc.wantValid || q.WindowSessions != tc.wantW {
				t.Fatalf("valid/window = %d/%d, want %d/%d",
					q.ValidSamples, q.WindowSessions, tc.wantValid, tc.wantW)
			}
			cov := q.CoverageRatio
			if cov == nil {
				t.Fatal("coverage is nil")
			}
			if math.Abs(*cov-tc.wantCov) > 1e-9 {
				t.Fatalf("coverage = %v, want %v — the denominator must be every ARCHIVED "+
					"session, not just the ones with a P/E", *cov, tc.wantCov)
			}
		})
	}
}

// The specific arithmetic the spec names, so a refactor cannot quietly re-scale it.
func TestCoverageIsARatioScaledOnlyOnce(t *testing.T) {
	q := statsOf(t, archive("2025-10-01", 225, 26)).Quality
	got := *q.CoverageRatio
	if got <= 0 || got >= 1 {
		t.Fatalf("coverage = %v — it must be a RATIO in [0,1]; the ×100 belongs at "+
			"presentation, exactly once, like CurrentPercentile", got)
	}
	if want := 11.5555; math.Abs(got*100-want) > 0.001 {
		t.Fatalf("coverage×100 = %.4f%%, want %.4f%%", got*100, want)
	}
}

// The contract FU-5 established, stated as a test: a session the exchange published no P/E for
// is an ACQUIRED session. 3055 蔚華科 has 225 of them and not one usable ratio.
func TestSessionsWithoutAPERatioStayInTheDenominator(t *testing.T) {
	q := statsOf(t, archive("2025-10-01", 225, 0)).Quality

	if q.WindowSessions != 225 {
		t.Fatalf("window sessions = %d, want 225 — a session with no P/E was still archived, "+
			"and dropping it would make coverage undefined for a loss-making company rather "+
			"than 0%%", q.WindowSessions)
	}
	if q.ValidSamples != 0 {
		t.Fatalf("valid samples = %d, want 0", q.ValidSamples)
	}
	if cov := q.CoverageRatio; cov == nil || *cov != 0 {
		t.Fatalf("coverage = %v, want 0", cov)
	}
}

// A non-positive or non-finite P/E is not a sample, but the session it came from is still one.
func TestUnusableRatiosCountAsSessionsNotAsSamples(t *testing.T) {
	zero, neg, nan := 0.0, -3.0, math.NaN()
	inf := math.Inf(1)
	fine := 25.0
	q := statsOf(t, []Ratios{
		session("2026-09-01", &zero),
		session("2026-09-02", &neg),
		session("2026-09-03", &nan),
		session("2026-09-04", &inf),
		session("2026-09-05", nil),
		session("2026-09-08", &fine),
	}).Quality

	if q.WindowSessions != 6 {
		t.Fatalf("window sessions = %d, want 6", q.WindowSessions)
	}
	if q.ValidSamples != 1 {
		t.Fatalf("valid samples = %d, want 1", q.ValidSamples)
	}
}

// ── span ──────────────────────────────────────────────────────────────────────────────

func TestSpanBoundsTheValidSamplesNotTheWindow(t *testing.T) {
	// 225 daily sessions from 2025-10-01; the last 26 carry a P/E.
	rows := archive("2025-10-01", 225, 26)
	q := statsOf(t, rows).Quality

	wantOldestValid := rows[len(rows)-26].Date
	wantNewestValid := rows[len(rows)-1].Date
	if q.OldestValidSession != wantOldestValid || q.NewestValidSession != wantNewestValid {
		t.Fatalf("valid span %s → %s, want %s → %s", q.OldestValidSession,
			q.NewestValidSession, wantOldestValid, wantNewestValid)
	}
	if q.OldestSession != rows[0].Date || q.NewestSession != rows[len(rows)-1].Date {
		t.Fatalf("archive span %s → %s, want %s → %s", q.OldestSession, q.NewestSession,
			rows[0].Date, rows[len(rows)-1].Date)
	}
	if q.SampleSpanSessions != 26 {
		t.Fatalf("span sessions = %d, want 26", q.SampleSpanSessions)
	}
	if q.SampleSpanDays != 26 {
		t.Fatalf("span days = %d, want 26 (inclusive, consecutive daily sessions)",
			q.SampleSpanDays)
	}
}

// The distinction the span exists for: the same NUMBER of samples, drawn from a month or from
// a year, is not the same evidence.
func TestTheSameSampleCountCanHaveVeryDifferentSpans(t *testing.T) {
	pe := 20.0
	var dense, sparse []Ratios
	d := "2026-08-01"
	for i := 0; i < 26; i++ {
		dense = append(dense, session(d, &pe))
		d = shiftDate(d, 1)
	}
	d = "2025-10-01"
	for i := 0; i < 26; i++ {
		sparse = append(sparse, session(d, &pe))
		d = shiftDate(d, 13)
	}

	a := statsOf(t, dense).Quality
	b := statsOf(t, sparse).Quality
	if a.ValidSamples != b.ValidSamples {
		t.Fatalf("the fixtures differ in sample count (%d vs %d); the point is that they "+
			"do not", a.ValidSamples, b.ValidSamples)
	}
	if !(a.SampleSpanDays < b.SampleSpanDays) {
		t.Fatalf("span days: dense %d, sparse %d — the span must separate them when the "+
			"count cannot", a.SampleSpanDays, b.SampleSpanDays)
	}
}

// A span must never be computed from observations the caller is not allowed to see.
func TestSpanRespectsTheAsOfBound(t *testing.T) {
	pe := 20.0
	rows := []Ratios{
		session("2026-08-03", &pe),
		session("2026-08-04", &pe),
		session("2026-09-04", &pe), // after asOf
	}
	q := Calculate(Input{Symbol: "TEST", History: rows, AsOfDate: "2026-08-31"}).HistoricalPE.Quality

	if q.NewestValidSession != "2026-08-04" {
		t.Fatalf("newest valid = %s, want 2026-08-04 — a future session leaked into a "+
			"historical run", q.NewestValidSession)
	}
	if q.WindowSessions != 2 {
		t.Fatalf("window sessions = %d, want 2", q.WindowSessions)
	}
}

// The archive stores the same session repeatedly, because the exchanges publish with a lag.
// A session is one session however many times it was archived — otherwise coverage's
// denominator inflates and every stock looks better covered than it is.
func TestRepeatedArchivesOfOneSessionCountOnce(t *testing.T) {
	pe := 20.0
	rows := []Ratios{
		{Symbol: "T", Date: "2026-09-01", PERatio: &pe, ArchiveDate: "2026-09-02"},
		{Symbol: "T", Date: "2026-09-01", PERatio: &pe, ArchiveDate: "2026-09-03"},
		{Symbol: "T", Date: "2026-09-01", PERatio: &pe, ArchiveDate: "2026-09-04"},
		{Symbol: "T", Date: "2026-09-02", PERatio: nil, ArchiveDate: "2026-09-04"},
	}
	q := statsOf(t, rows).Quality
	if q.WindowSessions != 2 || q.ValidSamples != 1 {
		t.Fatalf("valid/window = %d/%d, want 1/2", q.ValidSamples, q.WindowSessions)
	}
}

// ── classification ────────────────────────────────────────────────────────────────────

func TestClassificationTable(t *testing.T) {
	cases := []struct {
		name      string
		m         QualityMetrics
		available bool
		want      Quality
	}{
		{"a full year, fully covered", QualityMetrics{ValidSamples: 225,
			WindowSessions: 225, SampleSpanDays: 339}, true, QualityHigh},
		{"exactly on the HIGH thresholds", QualityMetrics{ValidSamples: 120,
			WindowSessions: 171, SampleSpanDays: 180}, true, QualityHigh},
		{"one sample short of HIGH", QualityMetrics{ValidSamples: 119,
			WindowSessions: 170, SampleSpanDays: 180}, true, QualityMedium},
		{"one day short of HIGH", QualityMetrics{ValidSamples: 200,
			WindowSessions: 225, SampleSpanDays: 179}, true, QualityMedium},
		{"coverage short of HIGH", QualityMetrics{ValidSamples: 130,
			WindowSessions: 225, SampleSpanDays: 300}, true, QualityMedium},
		{"exactly on the MEDIUM thresholds", QualityMetrics{ValidSamples: 60,
			WindowSessions: 200, SampleSpanDays: 90}, true, QualityMedium},
		{"the 2337 shape: sparse and recent", QualityMetrics{ValidSamples: 26,
			WindowSessions: 225, SampleSpanDays: 36}, true, QualityLow},
		{"barely available", QualityMetrics{ValidSamples: 20,
			WindowSessions: 20, SampleSpanDays: 20}, true, QualityLow},
		{"many samples, concentrated in one month", QualityMetrics{ValidSamples: 200,
			WindowSessions: 225, SampleSpanDays: 30}, true, QualityLow},
		{"good coverage but a short history", QualityMetrics{ValidSamples: 30,
			WindowSessions: 30, SampleSpanDays: 42}, true, QualityLow},
		{"below the availability floor", QualityMetrics{ValidSamples: 19,
			WindowSessions: 225, SampleSpanDays: 25}, false, QualityInsufficient},
		{"no usable P/E at all — the 3055 shape", QualityMetrics{ValidSamples: 0,
			WindowSessions: 225}, false, QualityInsufficient},
		{"nothing archived", QualityMetrics{}, false, QualityInsufficient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyQuality(tc.m, tc.available); got != tc.want {
				t.Fatalf("quality = %s, want %s", got, tc.want)
			}
		})
	}
}

// A stock with no usable P/E must never be graded LOW. LOW says "here is a number, trust it
// less"; there is no number, and telling a reader otherwise is worse than telling them nothing.
func TestZeroValidSamplesIsInsufficientNotLow(t *testing.T) {
	q := statsOf(t, archive("2025-10-01", 225, 0)).Quality
	if q.Quality != QualityInsufficient {
		t.Fatalf("quality = %s, want %s", q.Quality, QualityInsufficient)
	}
}

// An unavailable distribution cannot be graded, whatever its metrics look like.
func TestAnUnavailableDistributionIsNeverGradedAbleToBeTrusted(t *testing.T) {
	m := QualityMetrics{ValidSamples: 300, WindowSessions: 300, SampleSpanDays: 400}
	if got := ClassifyQuality(m, false); got != QualityInsufficient {
		t.Fatalf("quality = %s with available=false, want %s", got, QualityInsufficient)
	}
}

// ── the separation FU-9 rests on ──────────────────────────────────────────────────────

// Availability and quality are different axes. A LOW grade must not disturb the status the
// target price gates on.
func TestALowGradeDoesNotChangeAvailability(t *testing.T) {
	h := statsOf(t, archive("2025-10-01", 225, 26))

	if h.Status != Available {
		t.Fatalf("status = %s, want AVAILABLE — 26 samples clears MinHistoricalPESamples "+
			"and quality must not gate it", h.Status)
	}
	if h.Quality.Quality != QualityLow {
		t.Fatalf("quality = %s, want LOW", h.Quality.Quality)
	}
	if h.SampleCount != 26 || h.Median == nil || h.P25 == nil || h.P75 == nil {
		t.Fatalf("the statistics were disturbed by the grade: %+v", h)
	}
}

// The line FU-9 must not cross: a high P/E is a MODEL question, not a data-quality one.
// 3037 欣興 has 225 of 225 sessions and a median around 120x; its evidence is excellent.
func TestAHighPERatioDoesNotLowerTheGrade(t *testing.T) {
	for _, level := range []float64{8, 25, 120, 400} {
		rows := make([]Ratios, 0, 225)
		d := "2025-10-01"
		for i := 0; i < 225; i++ {
			pe := level + float64(i%9)
			rows = append(rows, session(d, &pe))
			d = shiftDate(d, 1)
		}
		q := statsOf(t, rows).Quality
		if q.Quality != QualityHigh {
			t.Fatalf("median around %vx graded %s — FU-9 grades the EVIDENCE, and whether "+
				"P/E is the right model for this company is a separate question it does not "+
				"answer", level, q.Quality)
		}
	}
}

// Nor does a wide distribution. Width is a re-rating, faithfully recorded.
func TestAWideDistributionDoesNotLowerTheGrade(t *testing.T) {
	rows := make([]Ratios, 0, 225)
	d := "2025-10-01"
	for i := 0; i < 225; i++ {
		pe := 20.0
		if i > 112 {
			pe = 200.0 // a violent re-rating, correctly observed
		}
		rows = append(rows, session(d, &pe))
		d = shiftDate(d, 1)
	}
	h := statsOf(t, rows)
	if h.Quality.Quality != QualityHigh {
		t.Fatalf("quality = %s — a wide distribution is a fact about the stock, not a defect "+
			"in the archive", h.Quality.Quality)
	}
	if h.Dispersion.Status != Available || h.Dispersion.RelativeIQR == nil {
		t.Fatalf("the width itself must still be reported: %+v", h.Dispersion)
	}
}

// ── rule version ──────────────────────────────────────────────────────────────────────

func TestEveryGradeCarriesItsRuleVersion(t *testing.T) {
	for _, rows := range [][]Ratios{
		archive("2025-10-01", 225, 225),
		archive("2025-10-01", 225, 26),
		archive("2025-10-01", 225, 0),
		nil,
	} {
		q := statsOf(t, rows).Quality
		if q.RuleVersion != QualityRuleVersion {
			t.Fatalf("rule version = %q, want %q — a stored grade with no version cannot be "+
				"interpreted once the thresholds move", q.RuleVersion, QualityRuleVersion)
		}
	}
}

// ── dispersion ────────────────────────────────────────────────────────────────────────

func TestDispersionRefusesRatherThanApproximating(t *testing.T) {
	if d := ComputeDispersion(HistoricalPEStats{Status: InsufficientData}); d.Status == Available {
		t.Fatal("an unavailable distribution produced a dispersion")
	}
	// A non-positive median has no meaningful fraction to divide by. The absolute width
	// still stands.
	zero, p25, p75 := 0.0, -5.0, 5.0
	d := ComputeDispersion(HistoricalPEStats{Status: Available,
		Median: &zero, P25: &p25, P75: &p75})
	if d.RelativeIQR != nil {
		t.Fatalf("relative IQR = %v with a median of 0; 0 would read as perfectly tight",
			*d.RelativeIQR)
	}
	if d.IQR == nil || *d.IQR != 10 {
		t.Fatalf("IQR = %v, want 10 — the absolute width is still defined", d.IQR)
	}
}

func TestDispersionIsIQROverMedian(t *testing.T) {
	med, p25, p75 := 20.0, 15.0, 25.0
	d := ComputeDispersion(HistoricalPEStats{Status: Available,
		Median: &med, P25: &p25, P75: &p75})
	if d.IQR == nil || *d.IQR != 10 {
		t.Fatalf("IQR = %v, want 10", d.IQR)
	}
	if d.RelativeIQR == nil || math.Abs(*d.RelativeIQR-0.5) > 1e-9 {
		t.Fatalf("relative IQR = %v, want 0.5", d.RelativeIQR)
	}
}

// ── which record wins when one session is archived twice ──────────────────────────────
//
// The archive stores the same session repeatedly because the exchanges publish with a lag, so
// this rule runs on real data constantly. It also reaches further than it looks: dropping a
// sample changes SampleCount, and SampleCount is the target price's availability gate. Both
// directions are pinned so a later refactor has to choose deliberately rather than inherit.

// A later archive that carries NO P/E supersedes an earlier one that did. Re-publication is a
// correction, and the exchange restating that a company had no positive earnings for a session
// is exactly as authoritative as its earlier statement that it did.
func TestALaterCorrectionWithNoPERatioWins(t *testing.T) {
	pe := 25.0
	rows := []Ratios{
		{Symbol: "T", Date: "2026-09-01", PERatio: &pe, ArchiveDate: "2026-09-02"},
		{Symbol: "T", Date: "2026-09-01", PERatio: nil, ArchiveDate: "2026-09-05"},
	}
	q := statsOf(t, rows).Quality

	if q.WindowSessions != 1 {
		t.Fatalf("window sessions = %d, want 1", q.WindowSessions)
	}
	if q.ValidSamples != 0 {
		t.Fatalf("valid samples = %d, want 0 — the withdrawn ratio was kept, so a figure the "+
			"exchange has retracted still counts toward the target price's sample gate",
			q.ValidSamples)
	}
}

// And the converse, which is the same rule read the other way: an EARLIER nil never survives a
// later valid publication.
func TestALaterArchiveNeverResurrectsAWithdrawnRatio(t *testing.T) {
	pe := 25.0
	rows := []Ratios{
		{Symbol: "T", Date: "2026-09-01", PERatio: nil, ArchiveDate: "2026-09-02"},
		{Symbol: "T", Date: "2026-09-01", PERatio: &pe, ArchiveDate: "2026-09-05"},
	}
	q := statsOf(t, rows).Quality

	if q.ValidSamples != 1 || q.WindowSessions != 1 {
		t.Fatalf("valid/window = %d/%d, want 1/1 — the later publication must win",
			q.ValidSamples, q.WindowSessions)
	}
}

// A correction the caller is not yet allowed to see must not apply. The asOf bound is on the
// ARCHIVE date, so a withdrawal archived tomorrow cannot reach a check dated today.
func TestAWithdrawalArchivedLaterIsInvisibleToAnEarlierCheck(t *testing.T) {
	pe := 25.0
	rows := []Ratios{
		{Symbol: "T", Date: "2026-09-01", PERatio: &pe, ArchiveDate: "2026-09-02"},
		{Symbol: "T", Date: "2026-09-01", PERatio: nil, ArchiveDate: "2026-09-05"},
	}
	q := Calculate(Input{Symbol: "T", History: rows, AsOfDate: "2026-09-03"}).HistoricalPE.Quality

	if q.ValidSamples != 1 {
		t.Fatalf("valid samples = %d, want 1 — a correction from 2026-09-05 reached a check "+
			"dated 2026-09-03", q.ValidSamples)
	}
}

// ── the LOW / INSUFFICIENT boundary, across the whole floor ───────────────────────────
//
// Zero valid samples is the obvious case and was already covered. The interesting range is
// 1–19: computable arithmetic, below the availability floor, and therefore still nothing to
// grade. A grade of LOW there would describe a distribution that was never published.
func TestEverySampleCountBelowTheFloorIsInsufficient(t *testing.T) {
	for _, valid := range []int{1, 2, 5, 19, MinHistoricalPESamples - 1} {
		h := statsOf(t, archive("2025-10-01", 225, valid))
		q := h.Quality

		if h.Status != InsufficientData {
			t.Fatalf("%d valid: status = %s, want INSUFFICIENT_DATA", valid, h.Status)
		}
		if q.Quality != QualityInsufficient {
			t.Fatalf("%d valid: quality = %s, want INSUFFICIENT — below the floor there is "+
				"no distribution to grade, and LOW would describe one", valid, q.Quality)
		}
		if q.ValidSamples != valid || q.WindowSessions != 225 {
			t.Fatalf("%d valid: metrics = %d/%d", valid, q.ValidSamples, q.WindowSessions)
		}
	}
}

// The first count AT the floor flips to a real grade. Stated beside the test above so the
// boundary is pinned from both sides rather than only from below.
func TestTheFloorIsWhereGradingBegins(t *testing.T) {
	h := statsOf(t, archive("2025-10-01", 225, MinHistoricalPESamples))

	if h.Status != Available {
		t.Fatalf("status = %s at exactly the floor, want AVAILABLE", h.Status)
	}
	if h.Quality.Quality != QualityLow {
		t.Fatalf("quality = %s, want LOW", h.Quality.Quality)
	}
}

// Below the floor the width of the distribution is not merely unmeasured, it does not exist.
// Reported as INSUFFICIENT_DATA rather than a bare UNAVAILABLE, which would claim we tried.
func TestDispersionBelowTheFloorSaysThereIsNoDistributionYet(t *testing.T) {
	d := statsOf(t, archive("2025-10-01", 225, 4)).Dispersion

	if d.Status != InsufficientData {
		t.Fatalf("dispersion status = %q, want INSUFFICIENT_DATA", d.Status)
	}
	if d.Reason == "" {
		t.Fatal("no reason given for an absent dispersion")
	}
}
