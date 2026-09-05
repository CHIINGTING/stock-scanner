package dailydata

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, Taipei)
	if err != nil {
		panic(err)
	}
	return t
}

// The clock is the market's, never the host's.
//
// A machine running in UTC would roll the trading date over at 08:00 Taipei — mid-session —
// and every EXACT_DATE archive would be written under the wrong name with nothing to show it.
func TestResolveUsesTaipeiNotHostLocal(t *testing.T) {
	// 2026-09-08 is a Tuesday. 06:00 UTC is 14:00 Taipei: closed in Taipei, still "morning"
	// anywhere west of it.
	utc := time.Date(2026, 9, 8, 6, 0, 0, 0, time.UTC)
	got := Resolve(utc)
	if got.Date != "2026-09-08" {
		t.Fatalf("date = %s, want 2026-09-08 (14:00 Taipei)", got.Date)
	}
	if got.Session != SessionClosed {
		t.Fatalf("session = %s, want CLOSED", got.Session)
	}
	// The same instant expressed in a different zone must give the same answer.
	ny, _ := time.LoadLocation("America/New_York")
	if other := Resolve(utc.In(ny)); other.Date != got.Date || other.Session != got.Session {
		t.Fatalf("the answer changed with the caller's zone: %+v vs %+v", other, got)
	}
}

func TestResolveSessionStates(t *testing.T) {
	cases := []struct {
		name    string
		now     string
		date    string
		session string
	}{
		// 2026-09-05 is a Saturday, 09-06 a Sunday, 09-07 Monday, 09-08 Tuesday.
		{"saturday", "2026-09-05 15:00", "2026-09-05", SessionNone},
		{"sunday", "2026-09-06 10:00", "2026-09-06", SessionNone},
		{"weekday before close", "2026-09-08 10:00", "2026-09-07", SessionClosed},
		{"weekday at 13:59", "2026-09-08 13:59", "2026-09-07", SessionClosed},
		{"weekday at 14:00", "2026-09-08 14:00", "2026-09-08", SessionClosed},
		{"weekday evening", "2026-09-08 21:00", "2026-09-08", SessionClosed},
		// Before the close on a Monday, the previous session is the FRIDAY, not Sunday.
		{"monday morning skips the weekend", "2026-09-07 09:00", "2026-09-04", SessionClosed},
	}
	for _, tc := range cases {
		got := Resolve(at(tc.now))
		if got.Date != tc.date || got.Session != tc.session {
			t.Errorf("%s: got %s/%s, want %s/%s (%s)",
				tc.name, got.Date, got.Session, tc.date, tc.session, got.Reason)
		}
	}
}

// 13:30 is the close; acquisition waits until 14:00 because the exchanges publish after the
// bell and the feeds lag behind them.
func TestSettleMarginIsApplied(t *testing.T) {
	for _, tc := range []struct {
		clock  string
		closed bool
	}{
		{"2026-09-08 13:29", false},
		{"2026-09-08 13:30", false}, // the bell itself is not "settled"
		{"2026-09-08 13:59", false},
		{"2026-09-08 14:00", true},
		{"2026-09-08 14:01", true},
	} {
		if got := Closed(at(tc.clock)); got != tc.closed {
			t.Errorf("Closed(%s) = %v, want %v", tc.clock, got, tc.closed)
		}
	}
}

// ── holidays are discovered, not guessed ──────────────────────────────────────────────

// stubPrices returns a series ending at `last`, or an explicit list of session dates.
//
// `dates` matters: a stub that only ever returns ONE bar cannot express the shape a real
// backfill sees — a series that runs PAST the target date while having no bar on it, which
// is exactly what a public holiday looks like when you ask about it later. The first version
// of these tests could not produce that shape, so the holiday check passed while being wrong
// for every date except the newest.
type stubPrices struct {
	last  string
	dates []string
	err   error
}

func (s stubPrices) Fetch(fetcher.StockInfo) (fetcher.StockData, error) {
	if s.err != nil {
		return fetcher.StockData{}, s.err
	}
	list := s.dates
	if len(list) == 0 && s.last != "" {
		list = []string{s.last}
	}
	if len(list) == 0 {
		return fetcher.StockData{}, nil
	}
	var cs []fetcher.Candle
	for _, x := range list {
		d, _ := time.ParseInLocation("2006-01-02", x, Taipei)
		cs = append(cs, fetcher.Candle{Date: d, Close: 100})
	}
	return fetcher.StockData{Candles: cs}, nil
}

// A holiday looks exactly like an ordinary weekday on the calendar. Only the data can tell,
// so the benchmark's last bar is what decides.
func TestHolidayIsDiscoveredFromTheData(t *testing.T) {
	st := SessionState{Date: "2026-10-10", Session: SessionClosed} // a weekday, and a holiday
	got := ConfirmSession(st, stubPrices{last: "2026-10-09"}, fetcher.StockInfo{Symbol: "2330"})
	if got.Session != SessionNone {
		t.Fatalf("session = %s, want NON_TRADING — the benchmark has no bar for that date",
			got.Session)
	}
	if got.Reason == "" {
		t.Fatal("no reason given for the non-trading verdict")
	}
	if got.Date != "2026-10-10" {
		t.Fatalf("the target date changed to %s", got.Date)
	}
}

// A real trading day is confirmed and left alone.
func TestTradingDayIsConfirmed(t *testing.T) {
	st := SessionState{Date: "2026-09-08", Session: SessionClosed}
	got := ConfirmSession(st, stubPrices{last: "2026-09-08"}, fetcher.StockInfo{Symbol: "2330"})
	if got.Session != SessionClosed {
		t.Fatalf("session = %s, want CLOSED", got.Session)
	}
}

// An unreachable price source must NOT be read as a holiday. "We could not ask" and "the
// market was shut" are different facts, and only one of them means there is nothing to fetch.
func TestUnreachablePriceSourceDoesNotDeclareAHoliday(t *testing.T) {
	st := SessionState{Date: "2026-09-08", Session: SessionClosed}
	for _, p := range []PriceSource{
		stubPrices{err: errFake},
		stubPrices{}, // no candles at all
		nil,
	} {
		got := ConfirmSession(st, p, fetcher.StockInfo{Symbol: "2330"})
		if got.Session != SessionClosed {
			t.Fatalf("an unreachable price source produced %s; it must leave the state alone",
				got.Session)
		}
	}
}

// A weekend stays a weekend whatever the probe says.
func TestConfirmNeverUpgradesANonTradingDay(t *testing.T) {
	st := SessionState{Date: "2026-09-05", Session: SessionNone, Reason: "週末"}
	got := ConfirmSession(st, stubPrices{last: "2026-09-05"}, fetcher.StockInfo{Symbol: "2330"})
	if got.Session != SessionNone {
		t.Fatalf("a weekend was upgraded to %s", got.Session)
	}
}

var errFake = fakeErr("boom")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

// The shape a repair run actually sees: the series runs PAST the target date and has no bar
// ON it. That is a holiday, and the old "is the last bar older than the target" test could
// never detect it — for any past date the answer is trivially no.
func TestHolidayIsDetectedEvenWhenTheSeriesRunsPastIt(t *testing.T) {
	st := SessionState{Date: "2026-10-09", Session: SessionClosed}
	series := []string{"2026-10-07", "2026-10-08", "2026-10-12", "2026-10-13"} // 10-09 absent
	got := ConfirmSession(st, stubPrices{dates: series}, fetcher.StockInfo{Symbol: "2330"})
	if got.Session != SessionNone {
		t.Fatalf("session = %s, want NON_TRADING — the series has no bar for 2026-10-09",
			got.Session)
	}

	// And a date the series DOES cover stays a trading day, even though it is not the newest.
	st2 := SessionState{Date: "2026-10-08", Session: SessionClosed}
	got2 := ConfirmSession(st2, stubPrices{dates: series}, fetcher.StockInfo{Symbol: "2330"})
	if got2.Session != SessionClosed {
		t.Fatalf("a session with a bar was called %s", got2.Session)
	}
}

// A series that has fallen far behind says more about our copy than about the market. It must
// not be read as a run of holidays — that looks identical to a quiet week and exits 0.
func TestStaleSeriesDoesNotDeclareAHoliday(t *testing.T) {
	st := SessionState{Date: "2026-09-30", Session: SessionClosed}
	got := ConfirmSession(st, stubPrices{last: "2026-09-01"}, fetcher.StockInfo{Symbol: "2330"})
	if got.Session != SessionClosed {
		t.Fatalf("a stale series declared %s; it must leave the session alone", got.Session)
	}
	if got.Reason == "" {
		t.Fatal("no reason given for declining to judge")
	}
	// Just behind a long weekend is still trustworthy evidence.
	near := ConfirmSession(SessionState{Date: "2026-09-08", Session: SessionClosed},
		stubPrices{last: "2026-09-07"}, fetcher.StockInfo{Symbol: "2330"})
	if near.Session != SessionNone {
		t.Fatalf("a one-day gap should still be readable as a holiday, got %s", near.Session)
	}
}
