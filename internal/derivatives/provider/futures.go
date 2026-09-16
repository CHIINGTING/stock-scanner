package provider

import "fmt"

// SourceFuturesDaily is the per-contract futures daily report: 2,250 rows on the 2026-09-04
// capture, including 271 combination (spread) codes.
const SourceFuturesDaily = "DailyMarketReportFut"

// FuturesDailyRow is one contract-month row of the futures daily report.
//
// Field names differ per feed and are read from each feed rather than assumed:
// DailyMarketReportOpt has Close; this one has LAST and no Close at all, plus Change and a
// percentage column literally named "%".
type FuturesDailyRow struct {
	TradingDate string
	Product     string
	ExpiryCode  string
	ExpiryKind  string // COMBINATION for the 271 "/" codes — one order across two expiries,
	// excluded from every per-series aggregate
	Session string

	Open, High, Low, Last *float64
	Change                *float64
	ChangePct             *float64 // the "%" column, after the trailing-percent strip
	Volume                *float64
	Settlement            *float64
	OI                    *float64
}

// FuturesDaily is a decoded futures daily response.
type FuturesDaily struct {
	Source      string
	TradingDate string
	Rows        []FuturesDailyRow
	Report      Report
}

var futuresDailyRequired = []string{
	"Date", "Contract", "ContractMonth(Week)", "Last", "Volume", "OpenInterest",
	"SettlementPrice", "TradingSession",
}

// ParseFuturesDaily decodes DailyMarketReportFut.
//
// Two live characteristics this feed alone carries, both of which broke an earlier rule:
//
//   - "%" holds 727 real values next to 1,467 "-" and 56 "0.00%", so it needs the percentage
//     normalisation rather than the plain one.
//   - 252 一般 rows carry OpenInterest "-". An earlier draft explained "-" with the story
//     "the night rows carry no OI"; the story is false and these rows are why. The rule
//     survives without it.
func ParseFuturesDaily(body []byte) (*FuturesDaily, error) {
	raw, err := rawRows(SourceFuturesDaily, body, futuresDailyRequired)
	if err != nil {
		return nil, err
	}
	out := &FuturesDaily{
		Source: SourceFuturesDaily,
		Report: Report{Source: SourceFuturesDaily, RowsSeen: len(raw)},
	}
	rep := &out.Report

	for i, r := range raw {
		date, err := isoDate(r["Date"])
		if err != nil {
			rep.reject(i, "Date", r["Date"], err.Error())
			continue
		}
		if out.TradingDate == "" {
			out.TradingDate = date
		} else if out.TradingDate != date {
			return nil, fmt.Errorf("%w: %s: response carries more than one trading date (%s and %s)",
				ErrStructure, SourceFuturesDaily, out.TradingDate, date)
		}
		session, err := NormalizeSession(r["TradingSession"])
		if err != nil {
			rep.reject(i, "TradingSession", r["TradingSession"], err.Error())
			continue
		}
		if r["Contract"] == "" || r["ContractMonth(Week)"] == "" {
			rep.reject(i, "Contract/ContractMonth(Week)",
				r["Contract"]+"/"+r["ContractMonth(Week)"], "row has no identity")
			continue
		}

		row := FuturesDailyRow{
			TradingDate: date,
			Product:     r["Contract"],
			ExpiryCode:  r["ContractMonth(Week)"],
			ExpiryKind:  ClassifyExpiry(r["ContractMonth(Week)"]),
			Session:     session,
		}
		for _, f := range []struct {
			col string
			dst **float64
		}{
			{"Open", &row.Open}, {"High", &row.High}, {"Low", &row.Low}, {"Last", &row.Last},
			{"Change", &row.Change}, {"Volume", &row.Volume},
			{"SettlementPrice", &row.Settlement}, {"OpenInterest", &row.OI},
		} {
			v, ok := decodeNum(r[f.col])
			rep.note(ok, f.col, r[f.col])
			*f.dst = v
		}
		pct, okp := decodePercent(r["%"])
		rep.note(okp, "%", r["%"])
		row.ChangePct = pct

		out.Rows = append(out.Rows, row)
		rep.RowsAccepted++
	}
	rep.finish()
	return out, nil
}
