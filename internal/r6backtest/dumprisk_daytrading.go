package r6backtest

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/daytrading"
)

// dayTradingArchive adapts the 當沖 archive to the study's DayTradingLookup.
//
// The adapter deliberately carries no fallback: when the archive has no record for a
// (symbol, date) it reports ok=false, and dumprisk.Compute then leaves DayTradingOK false.
// A missing record means "we do not know how much of that session was day-traded", which is
// a different statement from "none of it was", and the difference matters most on exactly
// the days this study is about.
type dayTradingArchive struct {
	loaded *daytrading.Loaded
	// codes is the number of DISTINCT SYMBOLS the archive indexes — not stock-days.
	// Reported so a run states its own coverage instead of leaving the reader to assume
	// the archive was complete.
	codes int
}

// LoadDayTradingArchive reads every 當沖 snapshot in dir. A missing directory is not an
// error the study should die on: it returns an error the caller logs before continuing with
// the 當沖 rows empty.
func LoadDayTradingArchive(dir string) (*dayTradingArchive, error) {
	l, err := daytrading.LoadRange(dir, "", "")
	if err != nil {
		return nil, err
	}
	if l == nil || len(l.Dates()) == 0 {
		return nil, errNoDayTradingArchive
	}

	return &dayTradingArchive{loaded: l, codes: l.Codes()}, nil
}

// DayTradeShares implements DayTradingLookup.
func (a *dayTradingArchive) DayTradeShares(symbol string, date time.Time) (float64, bool) {
	if a == nil || a.loaded == nil {
		return 0, false
	}
	rec, ok := a.loaded.Record(symbol, date.Format("2006-01-02"))
	if !ok || rec == nil {
		return 0, false
	}
	return float64(rec.DayTradeShares), true
}

// Codes reports how many distinct symbols the archive indexes.
func (a *dayTradingArchive) Codes() int {
	if a == nil {
		return 0
	}
	return a.codes
}

// Dates reports how many snapshot dates were loaded.
func (a *dayTradingArchive) Dates() int {
	if a == nil || a.loaded == nil {
		return 0
	}
	return len(a.loaded.Dates())
}

type dayTradingArchiveError string

func (e dayTradingArchiveError) Error() string { return string(e) }

const errNoDayTradingArchive = dayTradingArchiveError("no 當沖 snapshots found (run: go run ./cmd/daytrading-fetch --from … --to …)")
