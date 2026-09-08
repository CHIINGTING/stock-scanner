package provider

import "fmt"

// SourceOptionsByStrike is the per-strike options report: 12,012 rows across TXO / TEO / TGO /
// IJO and others, both trading sessions, on the 2026-09-04 capture.
const SourceOptionsByStrike = "DailyMarketReportOpt"

// ProductTXO is the 臺股指數選擇權 series code, and the only product R15 v1 aggregates.
const ProductTXO = "TXO"

// OptionStrikeRow is one (product, expiry, strike, call/put) row of the by-strike report.
//
// Session lives on the row here because a parser has no snapshot to put it on. Storage moves
// it: options_oi_by_strike is keyed without a session column, so the response is archived as
// TWO snapshots, DAY and AFTER_HOURS, or the 盤後 rows collide with the 一般 rows on the
// UNIQUE index.
type OptionStrikeRow struct {
	TradingDate string
	Product     string
	ExpiryCode  string
	ExpiryKind  string
	Strike      float64 // NOT NULL, part of the key: an unparsable strike rejects the row
	CallPut     string
	Session     string // DAY | AFTER_HOURS

	// Nullable, and that is load-bearing. On the 2026-09-04 capture 1,295 TXO rows carry
	// OpenInterest "0" (a strike nobody holds) and 2,868 carry "-" (no value carried, every
	// one of them a 盤後 row). Those are different facts.
	OI         *float64
	Volume     *float64
	Settlement *float64
}

// OptionsByStrike is a decoded by-strike response.
type OptionsByStrike struct {
	Source      string
	TradingDate string
	Rows        []OptionStrikeRow
	Report      Report
}

var optionsByStrikeRequired = []string{
	"Date", "Contract", "ContractMonth(Week)", "StrikePrice", "CallPut",
	"Volume", "OpenInterest", "SettlementPrice", "TradingSession",
}

// ParseOptionsByStrike decodes DailyMarketReportOpt.
//
// Every product is decoded, not just TXO: the contract-selection rule (§3.1's "all
// aggregation is over TXO rows only") belongs to the aggregate, and a parser that dropped the
// other 6,024 rows would make it impossible to see, later, that it had.
func ParseOptionsByStrike(body []byte) (*OptionsByStrike, error) {
	raw, err := rawRows(SourceOptionsByStrike, body, optionsByStrikeRequired)
	if err != nil {
		return nil, err
	}
	out := &OptionsByStrike{
		Source: SourceOptionsByStrike,
		Report: Report{Source: SourceOptionsByStrike, RowsSeen: len(raw)},
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
				ErrStructure, SourceOptionsByStrike, out.TradingDate, date)
		}
		session, err := NormalizeSession(r["TradingSession"])
		if err != nil {
			rep.reject(i, "TradingSession", r["TradingSession"], err.Error())
			continue
		}
		callPut, err := NormalizeCallPut(r["CallPut"])
		if err != nil {
			rep.reject(i, "CallPut", r["CallPut"], err.Error())
			continue
		}
		strike, err := decodeRequiredNum(r["StrikePrice"])
		if err != nil {
			rep.reject(i, "StrikePrice", r["StrikePrice"], err.Error())
			continue
		}
		if r["Contract"] == "" || r["ContractMonth(Week)"] == "" {
			rep.reject(i, "Contract/ContractMonth(Week)",
				r["Contract"]+"/"+r["ContractMonth(Week)"], "row has no identity")
			continue
		}

		oi, ok := decodeNum(r["OpenInterest"])
		rep.note(ok, "OpenInterest", r["OpenInterest"])
		vol, okv := decodeNum(r["Volume"])
		rep.note(okv, "Volume", r["Volume"])
		settle, oks := decodeNum(r["SettlementPrice"])
		rep.note(oks, "SettlementPrice", r["SettlementPrice"])

		out.Rows = append(out.Rows, OptionStrikeRow{
			TradingDate: date,
			Product:     r["Contract"],
			ExpiryCode:  r["ContractMonth(Week)"],
			ExpiryKind:  ClassifyExpiry(r["ContractMonth(Week)"]),
			Strike:      strike,
			CallPut:     callPut,
			Session:     session,
			OI:          oi,
			Volume:      vol,
			Settlement:  settle,
		})
		rep.RowsAccepted++
	}
	rep.finish()
	return out, nil
}
