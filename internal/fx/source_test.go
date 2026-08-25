package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// yahooBody builds a chart response. A nil close marks a session that has not settled.
func yahooBody(t *testing.T, days []struct {
	date  string
	close *float64
}) string {
	t.Helper()
	var ts []int64
	var cl []*float64
	for _, d := range days {
		day, err := time.ParseInLocation("2006-01-02", d.date, londonTZ)
		if err != nil {
			t.Fatalf("bad fixture date %q: %v", d.date, err)
		}
		ts = append(ts, day.Unix())
		cl = append(cl, d.close)
	}
	b, err := json.Marshal(map[string]any{
		"chart": map[string]any{
			"result": []any{map[string]any{
				"timestamp": ts,
				"indicators": map[string]any{
					"quote": []any{map[string]any{"close": cl}},
				},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func f64p(v float64) *float64 { return &v }

func serve(t *testing.T, body string, status int) (*http.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	// Point the package's URL at the test server for the duration.
	old := yahooChartOverride
	yahooChartOverride = srv.URL + "/"
	return srv.Client(), func() { yahooChartOverride = old; srv.Close() }
}

// The in-progress session must never reach the archive. Yahoo appends it with a null close
// (and again as a live intraday row); persisting it would freeze a partial price into the
// record as though it had settled.
func TestFetchDropsUnfinishedSessions(t *testing.T) {
	today := time.Now().In(londonTZ)
	y := func(n int) string { return today.AddDate(0, 0, -n).Format("2006-01-02") }

	body := yahooBody(t, []struct {
		date  string
		close *float64
	}{
		{y(3), f64p(31.50)},
		{y(2), f64p(31.60)},
		{y(1), f64p(31.70)},
		{y(0), nil},         // today, not settled
		{y(0), f64p(31.99)}, // today again, the live intraday row
	})
	c, done := serve(t, body, http.StatusOK)
	defer done()

	s, err := Fetch(context.Background(), c, 1)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(s.Bars) != 3 {
		t.Fatalf("got %d bars, want 3 finished ones: %+v", len(s.Bars), s.Bars)
	}
	for _, b := range s.Bars {
		if b.Date >= today.Format("2006-01-02") {
			t.Errorf("today's unfinished session %s reached the archive", b.Date)
		}
	}
	if s.Bars[len(s.Bars)-1].Close != 31.70 {
		t.Errorf("last close = %v, want the last SETTLED one", s.Bars[len(s.Bars)-1].Close)
	}
	if s.Symbol != Symbol {
		t.Errorf("symbol = %q, want %q", s.Symbol, Symbol)
	}
}

// An inverted or wrong pair would silently reverse every conclusion the research draws, so
// the level is checked rather than the ticker name trusted. TWD/USD is ~0.031.
func TestFetchRejectsAnImplausiblePair(t *testing.T) {
	today := time.Now().In(londonTZ)
	body := yahooBody(t, []struct {
		date  string
		close *float64
	}{
		{today.AddDate(0, 0, -2).Format("2006-01-02"), f64p(0.0314)}, // TWD/USD, inverted
	})
	c, done := serve(t, body, http.StatusOK)
	defer done()

	if _, err := Fetch(context.Background(), c, 1); err == nil {
		t.Fatal("an inverted quote should be refused, not archived")
	} else if !strings.Contains(err.Error(), "plausible") {
		t.Errorf("error should name the implausible range, got: %v", err)
	}
}

func TestFetchTransportFailures(t *testing.T) {
	today := time.Now().In(londonTZ).AddDate(0, 0, -2).Format("2006-01-02")
	ok := yahooBody(t, []struct {
		date  string
		close *float64
	}{{today, f64p(31.5)}})

	c, done := serve(t, ok, http.StatusInternalServerError)
	if _, err := Fetch(context.Background(), c, 1); err == nil {
		t.Error("a 500 should be an error")
	}
	done()

	c, done = serve(t, `{"chart":{"error":{"description":"Not Found"}}}`, http.StatusOK)
	if _, err := Fetch(context.Background(), c, 1); err == nil {
		t.Error("a yahoo error payload should be an error")
	}
	done()

	c, done = serve(t, `{"chart":{"result":[]}}`, http.StatusOK)
	if _, err := Fetch(context.Background(), c, 1); err == nil {
		t.Error("an empty result should be an error")
	}
	done()
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Series{Symbol: Symbol, Bars: []Bar{
		{Date: "2026-08-20", Close: 31.5},
		{Date: "2026-08-21", Close: 31.6},
	}}
	if _, err := Save(dir, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Bars) != 2 || got.Bars[1].Close != 31.6 {
		t.Errorf("round trip lost data: %+v", got.Bars)
	}

	// The archive must not shrink. A truncated upstream response would otherwise delete
	// history the research already depends on.
	short := &Series{Symbol: Symbol, Bars: s.Bars[:1]}
	if _, err := Save(dir, short); err == nil {
		t.Error("saving a shorter series should be refused")
	}
	if again, _ := Load(dir); len(again.Bars) != 2 {
		t.Errorf("the archive was damaged by the refused write: %d bars", len(again.Bars))
	}

	// An empty series is never written.
	if _, err := Save(dir, &Series{Symbol: Symbol}); err == nil {
		t.Error("saving an empty series should be refused")
	}
}

// A missing archive must be an ERROR, so "no FX configured" stays distinguishable from
// "the currency did not move".
func TestLoadMissingArchiveIsAnError(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("a missing archive should be an error, not an empty series")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SpecUSDTWD.File), []byte(`{"symbol":"USDTWD","bars":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("an empty archive should be an error")
	}
}

// Bars must come back in date order whatever order they were stored in, because every metric
// assumes it.
func TestLoadSortsByDate(t *testing.T) {
	dir := t.TempDir()
	body := `{"symbol":"USDTWD","bars":[{"date":"2026-08-21","close":31.6},{"date":"2026-08-19","close":31.4},{"date":"2026-08-20","close":31.5}]}`
	if err := os.WriteFile(filepath.Join(dir, SpecUSDTWD.File), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := 1; i < len(got.Bars); i++ {
		if got.Bars[i-1].Date > got.Bars[i].Date {
			t.Fatalf("bars are out of order: %+v", got.Bars)
		}
	}
}
