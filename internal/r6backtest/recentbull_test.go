package r6backtest

import (
	"math"
	"testing"
	"time"
)

func TestRecentBullStatusThresholds(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "INSUFFICIENT"}, {11, "INSUFFICIENT"},
		{12, "LOW_SAMPLE"}, {29, "LOW_SAMPLE"},
		{30, "OK"}, {1000, "OK"},
	}
	for _, c := range cases {
		if got := recentBullStatus(c.n); got != c.want {
			t.Errorf("recentBullStatus(%d)=%s want %s", c.n, got, c.want)
		}
	}
}

func TestDefaultRecentBullWindowsNested(t *testing.T) {
	// Build a synthetic axis ending 2026-06-08.
	var axis []string
	d := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	for !d.After(end) {
		axis = append(axis, d.Format("2006-01-02"))
		d = d.AddDate(0, 0, 1)
	}
	ws := DefaultRecentBullWindows(axis)
	if len(ws) != 3 {
		t.Fatalf("want 3 windows, got %d", len(ws))
	}
	if ws[0].Name != "recent_2m" || ws[1].Name != "recent_4m" || ws[2].Name != "recent_6m" {
		t.Fatalf("unexpected window names: %+v", ws)
	}
	// End must equal axis end; starts must be 2m ⊃ within 4m ⊃ within 6m (nested).
	if ws[0].End != "2026-06-08" {
		t.Errorf("end=%s want 2026-06-08", ws[0].End)
	}
	if !(ws[0].Start > ws[1].Start && ws[1].Start > ws[2].Start) {
		t.Errorf("starts not nested: 2m=%s 4m=%s 6m=%s", ws[0].Start, ws[1].Start, ws[2].Start)
	}
	if ws[0].Start != "2026-04-08" || ws[1].Start != "2026-02-08" || ws[2].Start != "2025-12-08" {
		t.Errorf("unexpected starts: %s %s %s", ws[0].Start, ws[1].Start, ws[2].Start)
	}
}

func TestMaturityCutoffKey(t *testing.T) {
	axis := []string{"d0", "d1", "d2", "d3", "d4"} // len 5
	// 20d cutoff impossible on a 5-bar axis → empty.
	if got := maturityCutoffKey(axis, 20); got != "" {
		t.Errorf("want empty cutoff for short axis, got %q", got)
	}
	// h=2 → axis[len-1-2] = axis[2] = "d2".
	if got := maturityCutoffKey(axis, 2); got != "d2" {
		t.Errorf("cutoff h=2 = %q want d2", got)
	}
}

// makeTrade builds a minimal Trade for a given signal date with a 20d return.
func makeTrade(setup, date string, ret20 float64) Trade {
	d, _ := time.Parse("2006-01-02", date)
	return Trade{
		SetupName:     setup,
		SignalDate:    d,
		EntryDate:     d.AddDate(0, 0, 1),
		Return5d:      1, Return10d: 1, Return20d: ret20, Return60d: math.NaN(),
		HoldReturn20d: ret20,
	}
}

func TestBuildRecentBullCellMaturity(t *testing.T) {
	// Axis ending 2026-06-08; cutoff at exactly 20 trading days back.
	axis := buildBusinessAxis("2026-05-01", "2026-06-08")
	cutoff := maturityCutoffKey(axis, PrimaryHorizon)
	if cutoff == "" {
		t.Fatalf("expected a cutoff for axis len=%d", len(axis))
	}
	w := RecentBullWindow{Name: "recent_test", Start: axis[0], End: axis[len(axis)-1]}

	// One signal exactly at cutoff (matured), one one-bar-after cutoff (unmatured).
	afterCutoff := axis[len(axis)-PrimaryHorizon] // one trading day after cutoff
	trades := []Trade{
		makeTrade("X", cutoff, 5.0),       // matured
		makeTrade("X", afterCutoff, -3.0), // UNMATURED_20D
	}
	c := buildRecentBullCell("X", 0, "BASELINE", w, cutoff, trades, DefaultParams())

	if c.SignalCount != 2 {
		t.Errorf("signal_count=%d want 2", c.SignalCount)
	}
	if c.Matured20dCount != 1 {
		t.Errorf("matured=%d want 1 (signal at cutoff should be matured)", c.Matured20dCount)
	}
	if c.Unmatured20dCount != 1 {
		t.Errorf("unmatured=%d want 1 (signal after cutoff is UNMATURED_20D)", c.Unmatured20dCount)
	}
	if c.Matured20dCount+c.Unmatured20dCount != c.SignalCount {
		t.Errorf("matured+unmatured != signal_count")
	}
	if c.Available20d != 1 {
		t.Errorf("available_20d=%d want 1", c.Available20d)
	}
	if c.Status != "INSUFFICIENT" {
		t.Errorf("status=%s want INSUFFICIENT (avail<12)", c.Status)
	}
	// 20d avg must reflect ONLY the matured trade (5.0), not the unmatured (-3.0).
	if math.Abs(c.AvgReturn20d-5.0) > 1e-9 {
		t.Errorf("avg_20d=%.4f want 5.0 (unmatured must be excluded)", c.AvgReturn20d)
	}
}

// buildBusinessAxis returns daily date keys (incl. weekends — fine for tests).
func buildBusinessAxis(start, end string) []string {
	s, _ := time.Parse("2006-01-02", start)
	e, _ := time.Parse("2006-01-02", end)
	var out []string
	for !s.After(e) {
		out = append(out, s.Format("2006-01-02"))
		s = s.AddDate(0, 0, 1)
	}
	return out
}

func TestRecentBullCSVHeaderStable(t *testing.T) {
	// Guard the schema length so the CSV row builder stays in sync.
	if len(recentBullCSVHeader) != len(recentBullRow(RecentBullCell{})) {
		t.Errorf("CSV header (%d) and row (%d) length mismatch",
			len(recentBullCSVHeader), len(recentBullRow(RecentBullCell{})))
	}
}
