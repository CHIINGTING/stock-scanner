package healthcheck

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// FU-9 end to end: from archived sessions on disk, through the service, to the block a page
// renders. The domain tests prove the arithmetic; these prove the wiring, which is where a
// correct calculation gets dropped on the way to the reader.

// writeMixedSessions writes n consecutive daily sessions ending the day before endExclusive.
// The last validFromEnd of them carry a P/E; the rest are archived with none — a company that
// only recently returned to profit, which is the 2337 shape.
func writeMixedSessions(t *testing.T, dir string, n, validFromEnd int, endExclusive string) {
	t.Helper()
	end := day(endExclusive)
	for i := n; i >= 1; i-- {
		d := end.AddDate(0, 0, -i).Format(dateLayout)
		idx := n - i
		var pe *float64
		if idx >= n-validFromEnd {
			// Valued by distance from the END, so the same run of valid sessions produces
			// the same numbers whatever window it is embedded in. Two fixtures that differ
			// only in how much unpriced history sits in front of them must be comparable.
			v := 20 + float64((n-1-idx)%7)
			pe = &v
		}
		writeRatios(t, dir, d, pe, nil, nil)
	}
}

// ── the two shapes that motivated FU-9 ────────────────────────────────────────────────

// 2330-like: every archived session published a P/E.
func TestFullCoverageIsGradedHigh(t *testing.T) {
	s, vdir := valService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 225, "2026-09-05")

	q := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"}).Valuation.Historical.Quality

	if q.Quality != string(valuation.QualityHigh) {
		t.Fatalf("quality = %s, want HIGH (%d valid / %d sessions)",
			q.Quality, q.ValidSamples, q.WindowSessions)
	}
	if q.ValidSamples != 225 || q.WindowSessions != 225 {
		t.Fatalf("valid/window = %d/%d, want 225/225", q.ValidSamples, q.WindowSessions)
	}
	if q.Coverage.Status != StatusAvailable || q.Coverage.Display != "100.0%" {
		t.Fatalf("coverage = %+v, want 100.0%%", q.Coverage)
	}
	if len(q.Caveats) != 0 {
		t.Fatalf("a fully covered year carries caveats: %v", q.Caveats)
	}
}

// 2337-like: 26 usable P/E values inside a 225-session window, all recent. The target price
// still computes — that is the point — and the page says why it deserves less weight.
func TestSparseRecentSamplesAreGradedLowAndExplained(t *testing.T) {
	s, vdir := valService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 26, "2026-09-05")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})
	hist := h.Valuation.Historical
	q := hist.Quality

	if hist.Status != StatusAvailable {
		t.Fatalf("historical status = %s, want AVAILABLE — 26 clears the floor of %d",
			hist.Status, valuation.MinHistoricalPESamples)
	}
	if q.Quality != string(valuation.QualityLow) {
		t.Fatalf("quality = %s, want LOW", q.Quality)
	}
	if q.ValidSamples != 26 || q.WindowSessions != 225 {
		t.Fatalf("valid/window = %d/%d, want 26/225 — the 199 sessions with no published "+
			"P/E were still acquired", q.ValidSamples, q.WindowSessions)
	}
	if q.Coverage.Display != "11.6%" {
		t.Fatalf("coverage = %q, want 11.6%%", q.Coverage.Display)
	}
	// A grade with no numbers behind it is an assertion. The page must carry both.
	if len(q.Caveats) == 0 {
		t.Fatal("a LOW grade with no caveats tells a reader to distrust a number without " +
			"telling them why")
	}
	joined := strings.Join(q.Caveats, " / ")
	for _, want := range []string{"26", "225", "11.6"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the caveats do not name %s: %s", want, joined)
		}
	}
	if q.OldestValidSession == "" || q.NewestValidSession == "" || q.SampleSpanDays <= 0 {
		t.Fatalf("the sample span is missing: %+v", q)
	}
}

// 3055-like: 225 archived sessions, not one usable P/E. The distribution is unavailable, so
// there is nothing to grade — and calling that LOW would describe a number that does not
// exist.
func TestNoUsablePERatioIsInsufficientNotLow(t *testing.T) {
	s, vdir := valService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 0, "2026-09-05")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})
	q := h.Valuation.Historical.Quality

	if q.Quality != string(valuation.QualityInsufficient) {
		t.Fatalf("quality = %s, want INSUFFICIENT", q.Quality)
	}
	if q.Quality == string(valuation.QualityLow) {
		t.Fatal("a company with no published P/E was graded LOW, which reads as 'the number " +
			"is weak' when there is no number")
	}
	if h.Valuation.Historical.Status != StatusInsufficientData {
		t.Fatalf("historical status = %s, want INSUFFICIENT_DATA", h.Valuation.Historical.Status)
	}
	if h.Target.Status == StatusAvailable {
		t.Fatalf("target status = %s, want unavailable", h.Target.Status)
	}
	// 225 sessions WERE acquired, and the block must still say so — that is the difference
	// between "keep waiting" and "this company has no earnings to wait for".
	if q.WindowSessions != 225 {
		t.Fatalf("window sessions = %d, want 225", q.WindowSessions)
	}
}

// ── independence from the target price ────────────────────────────────────────────────

// The gate the whole design rests on: a LOW grade must leave the target price exactly as it
// was. Quality is a caveat printed beside a number, never the removal of the number.
func TestALowGradeLeavesTheTargetPriceAvailable(t *testing.T) {
	s, vdir := valService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 26, "2026-09-05")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})

	if h.Valuation.Historical.Quality.Quality != string(valuation.QualityLow) {
		t.Fatalf("fixture is not LOW: %s", h.Valuation.Historical.Quality.Quality)
	}
	if h.Target.Status != StatusAvailable {
		t.Fatalf("target status = %s, want AVAILABLE — quality must not gate it (%s)",
			h.Target.Status, h.Target.Reason)
	}
	if h.Target.Rule != valuation.RuleHistoricalPercentile {
		t.Fatalf("target rule = %s, want %s", h.Target.Rule, valuation.RuleHistoricalPercentile)
	}
	if h.Target.SampleCount != 26 {
		t.Fatalf("target sample count = %d, want 26", h.Target.SampleCount)
	}
	for _, sc := range h.Target.Scenarios {
		if sc.Status != StatusAvailable {
			t.Fatalf("scenario %s = %s, want AVAILABLE", sc.Name, sc.Status)
		}
	}
}

// The SAME 225 valid sessions, graded HIGH in one archive and MEDIUM in another, must produce
// identical statistics. The only difference between the fixtures is how much unpriced history
// sits in front of the samples — which changes coverage, and must change nothing else. If a
// grade ever reached back into the numbers it describes, this is where it would show.
func TestTheGradeDoesNotAlterTheStatistics(t *testing.T) {
	full, fullDir := valService(t, "2026-09-05")
	writeMixedSessions(t, fullDir, 225, 225, "2026-09-05")
	a := check(t, full, Request{Symbol: "2330", AsOf: "2026-09-05"}).Valuation.Historical

	sparse, sparseDir := valService(t, "2026-09-05")
	writeMixedSessions(t, sparseDir, 400, 225, "2026-09-05")
	b := check(t, sparse, Request{Symbol: "2330", AsOf: "2026-09-05"}).Valuation.Historical

	if a.Quality.Quality == b.Quality.Quality {
		t.Fatalf("both fixtures graded %s; the test needs them to differ to mean anything",
			a.Quality.Quality)
	}
	if a.SampleCount != b.SampleCount {
		t.Fatalf("sample counts differ (%d vs %d); the fixtures are not the same samples",
			a.SampleCount, b.SampleCount)
	}
	if a.Median.Display != b.Median.Display || a.P25.Display != b.P25.Display ||
		a.P75.Display != b.P75.Display ||
		a.CurrentPercentile.Display != b.CurrentPercentile.Display {
		t.Fatalf("the same samples produced different statistics under different grades:\n"+
			"  %s: P25 %s median %s P75 %s pct %s\n  %s: P25 %s median %s P75 %s pct %s",
			a.Quality.Quality, a.P25.Display, a.Median.Display, a.P75.Display,
			a.CurrentPercentile.Display,
			b.Quality.Quality, b.P25.Display, b.Median.Display, b.P75.Display,
			b.CurrentPercentile.Display)
	}
	// And the coverage that separates them is real, not incidental.
	if a.Quality.Coverage.Display == b.Quality.Coverage.Display {
		t.Fatalf("coverage is identical (%s) in both fixtures", a.Quality.Coverage.Display)
	}
}

// ── versioning and provenance ─────────────────────────────────────────────────────────

func TestTheGradeCarriesItsRuleVersion(t *testing.T) {
	s, vdir := valService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 225, "2026-09-05")

	q := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"}).Valuation.Historical.Quality
	if q.RuleVersion != valuation.QualityRuleVersion {
		t.Fatalf("rule version = %q, want %q", q.RuleVersion, valuation.QualityRuleVersion)
	}
	if !strings.Contains(q.Summary, q.RuleVersion) {
		t.Fatalf("the summary does not name the rule version: %q", q.Summary)
	}
}

// A stock with no valuation archive at all still answers with a grade rather than an empty
// struct, so a consumer never has to distinguish "not graded" from "graded INSUFFICIENT".
func TestAnEmptyArchiveIsGradedInsufficient(t *testing.T) {
	s, _ := valService(t, "2026-09-05")

	q := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"}).Valuation.Historical.Quality
	if q.Quality != string(valuation.QualityInsufficient) {
		t.Fatalf("quality = %q, want INSUFFICIENT", q.Quality)
	}
	if q.RuleVersion != valuation.QualityRuleVersion {
		t.Fatalf("rule version = %q", q.RuleVersion)
	}
}

// ── persistence ───────────────────────────────────────────────────────────────────────

// A grade with no version stored beside it becomes uninterpretable the moment the thresholds
// move: nobody can tell whether the stock changed or the rule did.
func TestQualityIsPersistedWithItsVersionAndMetrics(t *testing.T) {
	s, vdir := valService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 26, "2026-09-05")
	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})

	rows := evidenceRows(h)
	want := map[string]string{
		"valuation/quality":              string(valuation.QualityLow),
		"valuation/quality_rule_version": valuation.QualityRuleVersion,
	}
	got := map[string]string{}
	nums := map[string]float64{}
	for _, r := range rows {
		key := r.Category + "/" + r.Key
		if r.TextValue != nil {
			got[key] = *r.TextValue
		}
		if r.NumValue != nil {
			nums[key] = *r.NumValue
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for k, v := range map[string]float64{
		"valuation/quality_valid_samples":   26,
		"valuation/quality_window_sessions": 225,
	} {
		if nums[k] != v {
			t.Errorf("%s = %v, want %v", k, nums[k], v)
		}
	}
	if _, ok := nums["valuation/quality_coverage"]; !ok {
		t.Error("coverage was not persisted")
	}
}

// writeHighPESessions writes n consecutive daily sessions whose P/E sits around `level`.
// Used to prove a multiple's LEVEL changes nothing about either grade.
func writeHighPESessions(t *testing.T, dir string, n int, level float64, endExclusive string) {
	t.Helper()
	end := day(endExclusive)
	for i := n; i >= 1; i-- {
		d := end.AddDate(0, 0, -i).Format(dateLayout)
		pe := level + float64((n-i)%9)
		writeRatios(t, dir, d, &pe, nil, nil)
	}
}

// writeWideSessions writes a distribution that re-rates violently partway through — the
// 6274 台燿 shape, correctly observed.
func writeWideSessions(t *testing.T, dir string, n int, endExclusive string) {
	t.Helper()
	end := day(endExclusive)
	for i := n; i >= 1; i-- {
		d := end.AddDate(0, 0, -i).Format(dateLayout)
		pe := 20.0
		if n-i > n/2 {
			pe = 200.0
		}
		writeRatios(t, dir, d, &pe, nil, nil)
	}
}
