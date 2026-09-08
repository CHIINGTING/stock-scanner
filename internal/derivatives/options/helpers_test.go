package options_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/options"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// The committed 2026-09-04 fixture is the whole of M5's evidence, and the numbers below were
// measured off it rather than copied from an implementation. §10.8's own table is the same
// measurement:
//
//	session | family  | put vol | call vol | put OI | call OI (raw, before the settling expiry)
//	一般     | MONTHLY |     829 |      609 |  3,141 |   3,380
//	一般     | WEEKLY  |  40,700 |   45,261 | 14,007 |   8,000
//	盤後     | MONTHLY |     591 |      737 |      — |       —
//	盤後     | WEEKLY  |  20,894 |   20,309 |      — |       —
//
// The open-interest figures the aggregate PUBLISHES are those minus 202609F1, the expiry whose
// final settlement day is this trading date: WEEKLY becomes 2,510 / 1,338 and the product-wide
// total 5,651 / 4,718, which is exactly what the derived reconciliation fixture carries.
const (
	tradingDate    = "2026-09-04"
	settlingExpiry = "202609F1"

	dayMonthlyPutVol  = 829.0
	dayMonthlyCallVol = 609.0
	dayMonthlyPutOI   = 3141.0
	dayMonthlyCallOI  = 3380.0

	dayWeeklyPutVol  = 40700.0
	dayWeeklyCallVol = 45261.0
	dayWeeklyPutOI   = 2510.0 // 14,007 raw, minus the settling expiry's 11,497
	dayWeeklyCallOI  = 1338.0 // 8,000 raw, minus 6,662

	nightMonthlyPutVol  = 591.0
	nightMonthlyCallVol = 737.0
	nightWeeklyPutVol   = 20894.0
	nightWeeklyCallVol  = 20309.0

	allBothSessionsPutVol  = 63014.0
	allBothSessionsCallVol = 66916.0
	allDayPutOI            = 5651.0
	allDayCallOI           = 4718.0
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func fixtureRows(t *testing.T) []provider.OptionStrikeRow {
	t.Helper()
	decoded, err := provider.ParseOptionsByStrike(readFixture(t, "options_by_strike_20260904.json"))
	if err != nil {
		t.Fatalf("parse by-strike fixture: %v", err)
	}
	if decoded.Report.Status() != "AVAILABLE" {
		t.Fatalf("fixture parses %s: %v", decoded.Report.Status(), decoded.Report.Rejected)
	}
	if decoded.TradingDate != tradingDate {
		t.Fatalf("fixture trading date %q", decoded.TradingDate)
	}
	return decoded.Rows
}

// officialRatio is the exchange's own figures for the session. The DERIVED companion is the
// same two rules applied to the trimmed rows; the real capture reconciles only against the
// full 12,012-row response, which is 1.9 MB and is not committed (testdata/README.md).
func officialRatio(t *testing.T, name string) *provider.PutCallRatioRow {
	t.Helper()
	pcr, err := provider.ParsePutCallRatio(readFixture(t, name))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	row := pcr.ByDate(tradingDate)
	if row == nil {
		t.Fatalf("%s has no row for %s", name, tradingDate)
	}
	return row
}

// req is a request over the committed fixture, with the settlement read that actually happened
// on that session.
func req(family options.ContractFamily, session string) options.ObservationRequest {
	return options.ObservationRequest{
		TradingDate:    tradingDate,
		Product:        provider.ProductTXO,
		Family:         family,
		Session:        session,
		Source:         provider.SourceOptionsByStrike,
		SourceStatus:   institutional.StatusAvailable,
		SettlementRead: true,
		SettlingExpiry: settlingExpiry,
		Snapshots: []options.SnapshotRef{
			{Session: provider.SessionDay, SnapshotID: 11, Revision: 0},
		},
	}
}

func mustObserve(t *testing.T, r options.ObservationRequest, rows []provider.OptionStrikeRow) options.Observation {
	t.Helper()
	obs, err := options.NewObservation(r, rows)
	if err != nil {
		t.Fatalf("NewObservation(%s/%s): %v", r.Family, r.Session, err)
	}
	return obs
}

func fptr(v float64) *float64 { return &v }

// row builds one synthetic by-strike row. The synthetic cases exist for the states the live
// session does not contain — an observed zero denominator, an absent numerator against a
// present denominator, one side entirely missing — and every one of them is a state §10.8
// names.
func row(session, expiry string, strike float64, side string, vol, oi *float64) provider.OptionStrikeRow {
	return provider.OptionStrikeRow{
		TradingDate: tradingDate,
		Product:     provider.ProductTXO,
		ExpiryCode:  expiry,
		ExpiryKind:  provider.ClassifyExpiry(expiry),
		Strike:      strike,
		CallPut:     side,
		Session:     session,
		Volume:      vol,
		OI:          oi,
	}
}

// pair is a call and a put at one strike.
func pair(session, expiry string, strike float64, callVol, putVol, callOI, putOI *float64) []provider.OptionStrikeRow {
	return []provider.OptionStrikeRow{
		row(session, expiry, strike, provider.CallSide, callVol, callOI),
		row(session, expiry, strike, provider.PutSide, putVol, putOI),
	}
}

// series builds a synthetic multi-day series over one measurement, one row pair per day.
type day struct {
	date    string
	expiry  string
	callOI  *float64
	putOI   *float64
	status  institutional.MetricStatus
	noPairs bool
}

func buildSeries(t *testing.T, family options.ContractFamily, days []day) []options.Observation {
	t.Helper()
	var out []options.Observation
	for _, d := range days {
		status := d.status
		if status == "" {
			status = institutional.StatusAvailable
		}
		r := options.ObservationRequest{
			TradingDate:    d.date,
			Product:        provider.ProductTXO,
			Family:         family,
			Session:        provider.SessionDay,
			Source:         provider.SourceOptionsByStrike,
			SourceStatus:   status,
			SettlementRead: true,
		}
		var rows []provider.OptionStrikeRow
		if status.Usable() && !d.noPairs {
			rows = pair(provider.SessionDay, d.expiry, 46000, fptr(1), fptr(1), d.callOI, d.putOI)
			for i := range rows {
				rows[i].TradingDate = d.date
			}
		}
		obs, err := options.NewObservation(r, rows)
		if err != nil {
			t.Fatalf("series day %s: %v", d.date, err)
		}
		out = append(out, obs)
	}
	return out
}

// split returns the newest observation and the rest, which is the shape every M5 function
// takes: a reading, and the history it is computed against.
func split(obs []options.Observation) (options.Observation, []options.Observation) {
	return obs[len(obs)-1], obs[:len(obs)-1]
}
