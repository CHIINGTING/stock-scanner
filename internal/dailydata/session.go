package dailydata

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// Deciding WHICH day to acquire, and whether its data can exist yet.
//
// This is the step everything else depends on being right. Acquire too early and the archives
// fill with a session that has not finished; acquire the wrong date and every EXACT_DATE
// source is written under a name no query will look for.

// Taipei is the market's timezone. Every date derived here uses it.
//
// NEVER time.Local: a machine in UTC would roll the trading date over at 08:00 Taipei, in the
// middle of the session, and the archives would be named for the wrong day with nothing to
// show it. The market's clock is a property of the market, not of the host.
var Taipei = mustTaipei()

func mustTaipei() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		// A machine with no tzdata still has to produce the right trading day, so a fixed
		// offset is used rather than silently falling back to UTC. Taiwan has had no daylight
		// saving since 1979, so the offset is constant.
		return time.FixedZone("CST", 8*60*60)
	}
	return loc
}

const dateLayout = "2006-01-02"

// Session close, and the margin after it.
//
// The market closes at 13:30. Acquisition waits until 14:00 — the same cut-off R14's outcome
// grading already uses, deliberately, so the repo has ONE answer to "has the session
// finished" rather than two that can disagree.
//
// The margin is not politeness: the exchanges publish after the close, the feeds lag behind
// them, and a fetch at 13:31 collects a session in the middle of being written down.
const (
	closeHour     = 13
	closeMinute   = 30
	settleMinutes = 30
)

// SessionState describes the target day.
type SessionState struct {
	// Date is the session to acquire, YYYY-MM-DD.
	Date string
	// Session is CLOSED / OPEN / NON_TRADING.
	Session string
	// Reason explains a non-CLOSED state in one line.
	Reason string
}

// Resolve decides which session to acquire from the wall clock.
//
//	weekend                        → NON_TRADING, targeting that day (a safe no-op)
//	weekday before 14:00 Taipei    → the session is still OPEN; target the PREVIOUS weekday,
//	                                 whose data is final, rather than today's half-day
//	weekday at or after 14:00      → today, CLOSED
//
// A public holiday is indistinguishable from a trading day by the calendar alone, so it is
// NOT guessed here: it comes back CLOSED and the sources themselves report having nothing,
// which is the honest sequence. Guessing a holiday list would eventually skip a real session.
func Resolve(now time.Time) SessionState {
	t := now.In(Taipei)
	if isWeekend(t) {
		return SessionState{
			Date:    t.Format(dateLayout),
			Session: SessionNone,
			Reason:  "週末，交易所無交易",
		}
	}
	if !Closed(t) {
		prev := previousWeekday(t)
		return SessionState{
			Date:    prev.Format(dateLayout),
			Session: SessionClosed,
			Reason: "今日尚未收盤（" + t.Format("15:04") + " < 14:00），改取前一個交易日 " +
				prev.Format(dateLayout),
		}
	}
	return SessionState{Date: t.Format(dateLayout), Session: SessionClosed}
}

// Closed reports whether the trading day of t has finished, with the settle margin applied.
func Closed(t time.Time) bool {
	t = t.In(Taipei)
	cut := time.Date(t.Year(), t.Month(), t.Day(), closeHour, closeMinute+settleMinutes, 0, 0, Taipei)
	return !t.Before(cut)
}

func isWeekend(t time.Time) bool {
	switch t.In(Taipei).Weekday() {
	case time.Saturday, time.Sunday:
		return true
	}
	return false
}

func previousWeekday(t time.Time) time.Time {
	d := t.In(Taipei).AddDate(0, 0, -1)
	for isWeekend(d) {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// PriceSource supplies one stock's bars. Satisfied by the repo's existing fetcher.
type PriceSource interface {
	Fetch(info fetcher.StockInfo) (fetcher.StockData, error)
}

// ConfirmSession checks the target date against a real price series.
//
// The calendar says a Tuesday is a trading day; only the data says whether it traded. A
// public holiday, an unscheduled closure or a typhoon day all look like ordinary weekdays
// until a bar fails to appear, so the benchmark's last session is what actually decides.
//
// It DOWNGRADES only: a date the calendar already called non-trading stays that way. A probe
// that cannot answer leaves the state untouched rather than inventing one — an unreachable
// price source is a reason to report the price step as failed, not to declare a holiday.
func ConfirmSession(st SessionState, prices PriceSource, probe fetcher.StockInfo) SessionState {
	if st.Session == SessionNone || prices == nil {
		return st
	}
	sd, err := prices.Fetch(probe)
	if err != nil || len(sd.Candles) == 0 {
		return st
	}
	// A bar FOR the target session, not merely a series that reaches past it. Comparing the
	// last bar's date against the target is only meaningful when the target IS the newest
	// date: for any older session — a backfill, a repair run — "the series has kept going"
	// is trivially true, so a public holiday would sail through as a trading day and the
	// whole run would report failures instead of a no-op.
	for i := len(sd.Candles) - 1; i >= 0; i-- {
		d := sd.Candles[i].Date.In(Taipei).Format(dateLayout)
		if d == st.Date {
			return st // it traded
		}
		if d < st.Date {
			break
		}
	}

	last := sd.Candles[len(sd.Candles)-1].Date.In(Taipei).Format(dateLayout)

	// A series that stops well short of the target says more about our copy of it than about
	// the market. A stale cache would otherwise declare every recent day a holiday and exit
	// 0, which looks exactly like a quiet week.
	if staleBy(last, st.Date) > maxProbeStaleDays {
		return SessionState{
			Date: st.Date, Session: st.Session,
			Reason: "指標股序列只到 " + last + "，落後目標日過多，無法用來判斷是否開盤",
		}
	}
	return SessionState{
		Date: st.Date, Session: SessionNone,
		Reason: "指標股沒有 " + st.Date + " 的 K 棒（最後一根 " + last + "），該日未開盤",
	}
}

// maxProbeStaleDays is how far behind the probe series may fall before it stops being
// evidence about the market at all. Four calendar days spans a long weekend.
const maxProbeStaleDays = 4

// staleBy returns how many calendar days `last` is behind `target`, or 0 if it is not behind.
func staleBy(last, target string) int {
	l, err1 := time.ParseInLocation(dateLayout, last, Taipei)
	t, err2 := time.ParseInLocation(dateLayout, target, Taipei)
	if err1 != nil || err2 != nil || !l.Before(t) {
		return 0
	}
	return int(t.Sub(l).Hours() / 24)
}
