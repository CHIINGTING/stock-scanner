package provider

import (
	"fmt"
	"strings"
)

const (
	// SourceFinalSettlement names the expiry that settled on a given day. It returns ONE row:
	// the most recent settlement event, with no date of its own beyond that event's.
	SourceFinalSettlement = "FinalSettlementPriceIndexOptions"
	// SourceSettledPositions is the settlement-event feed whose ContractDeliveryMonth is a
	// DELIVERY MONTH rather than a series code — the TXO/TXU trap of §7.2.
	SourceSettledPositions = "SettledPositionsIndexOptions"
)

// SettlementRow is one settlement event: which series settled, on which day, at what price.
//
// This feed is what makes the open-interest half of the aggregation rule possible. The
// settling expiry's lots are still printed in that day's by-strike report (126,594 for
// 202609F1 on 2026-09-04) and are no longer live exposure; excluding them takes TXO open
// interest from 223,014 to 96,420, which is exactly the official PutOI + CallOI.
//
// Its ContractDeliveryMonth IS a series code here (TXO / 202609F1), unlike in
// SettledPositionsIndexOptions, so classifying it is legitimate.
type SettlementRow struct {
	FinalSettlementDay string // YYYY-MM-DD
	Product            string // TXO
	ContractName       string
	ExpiryCode         string
	ExpiryKind         string
	SettlementPrice    *float64
}

// Settlement is a decoded FinalSettlementPriceIndexOptions response.
type Settlement struct {
	Source string
	Rows   []SettlementRow
	Report Report
}

var settlementRequired = []string{
	"TheFinalSettlementDay", "Contract", "ContractDeliveryMonth", "TheFinalSettlementPrice",
}

// ParseFinalSettlement decodes the settlement-price feed.
func ParseFinalSettlement(body []byte) (*Settlement, error) {
	raw, err := rawRows(SourceFinalSettlement, body, settlementRequired)
	if err != nil {
		return nil, err
	}
	out := &Settlement{
		Source: SourceFinalSettlement,
		Report: Report{Source: SourceFinalSettlement, RowsSeen: len(raw)},
	}
	rep := &out.Report
	for i, r := range raw {
		day, err := isoDate(r["TheFinalSettlementDay"])
		if err != nil {
			rep.reject(i, "TheFinalSettlementDay", r["TheFinalSettlementDay"], err.Error())
			continue
		}
		if r["Contract"] == "" || r["ContractDeliveryMonth"] == "" {
			rep.reject(i, "Contract/ContractDeliveryMonth",
				r["Contract"]+"/"+r["ContractDeliveryMonth"], "row has no identity")
			continue
		}
		price, ok := decodeNum(r["TheFinalSettlementPrice"])
		rep.note(ok, "TheFinalSettlementPrice", r["TheFinalSettlementPrice"])
		out.Rows = append(out.Rows, SettlementRow{
			FinalSettlementDay: day,
			Product:            r["Contract"],
			ContractName:       r["ContractName"],
			ExpiryCode:         r["ContractDeliveryMonth"],
			ExpiryKind:         ClassifyExpiry(r["ContractDeliveryMonth"]),
			SettlementPrice:    price,
		})
		rep.RowsAccepted++
	}
	rep.finish()
	return out, nil
}

// SettlingExpiry answers "which expiry of this product stops being live exposure at the close
// of session D", from a settlement snapshot ARCHIVED FOR D.
//
// The comparison is against the session being aggregated, never against "today" and never
// against the live feed, and both mistakes were live in earlier drafts of the spec. The feed
// carries no date of its own and always answers with the most recent settlement event, so
// recomputing 2026-09-04 on or after 2026-09-09 (TXO's next settlement, 202609W2) gets
// 20260909 != 20260904, excludes nothing, and lands on 223,014 against 96,420 — a 2.31x error
// reached by rerunning a day that was correct when it was first fetched.
//
// Returns "" when D is not a settlement day for this product. That is a legitimate answer and
// is NOT the same as a failed settlement read: a failed read means the OI aggregate is
// MISSING, which is the caller's decision to make with information a parser does not have.
func SettlingExpiry(rows []SettlementRow, product, tradingDate string) string {
	for _, r := range rows {
		if r.Product == product && r.FinalSettlementDay == tradingDate {
			return r.ExpiryCode
		}
	}
	return ""
}

// ── settled positions, and the TXO/TXU trap ───────────────────────────────────────────

// SettledPositionRow is one settled-positions row.
//
// DeliveryMonth is deliberately NOT classified and carries no ExpiryKind. It is a delivery
// month, not a series code: this feed reports the F1 weekly as
// {Contract: TXU, ContractDeliveryMonth: 202609, ContractName: 臺指選擇權F1}, so the
// classification rule applied here would call MONTHLY the same series DailyMarketReportOpt
// calls WEEKLY — one contract, two kinds. §3.1 names three feeds as the only classifier
// inputs and this is not one of them.
type SettledPositionRow struct {
	FinalSettlementDay string // YYYY-MM-DD
	Product            string // TXU — the settlement-side alias of TXO
	DeliveryMonth      string // 202609 — a delivery month, NOT an expiry code
	ContractName       string // 臺指選擇權F1 — where the series suffix actually lives
	CallPut            string
	OIAtMaturity       *float64
	OIInTheMoney       *float64
	AbandonedOI        *float64
	ActualExecutedOI   *float64
}

// SettledPositions is a decoded SettledPositionsIndexOptions response.
type SettledPositions struct {
	Source string
	Rows   []SettledPositionRow
	Report Report
}

var settledPositionsRequired = []string{
	"TheFinalSettlementDay", "Contract", "ContractDeliveryMonth", "ContractName", "Call/Put",
	"OIAtTheMaturityDate(Lot)",
}

// ParseSettledPositions decodes the settled-positions feed.
//
// Its key for the call/put column is "Call/Put", with a slash, where every other options feed
// says "CallPut". Read from the feed, not assumed.
func ParseSettledPositions(body []byte) (*SettledPositions, error) {
	raw, err := rawRows(SourceSettledPositions, body, settledPositionsRequired)
	if err != nil {
		return nil, err
	}
	out := &SettledPositions{
		Source: SourceSettledPositions,
		Report: Report{Source: SourceSettledPositions, RowsSeen: len(raw)},
	}
	rep := &out.Report
	for i, r := range raw {
		day, err := isoDate(r["TheFinalSettlementDay"])
		if err != nil {
			rep.reject(i, "TheFinalSettlementDay", r["TheFinalSettlementDay"], err.Error())
			continue
		}
		cp, err := NormalizeCallPut(r["Call/Put"])
		if err != nil {
			rep.reject(i, "Call/Put", r["Call/Put"], err.Error())
			continue
		}
		row := SettledPositionRow{
			FinalSettlementDay: day,
			Product:            r["Contract"],
			DeliveryMonth:      r["ContractDeliveryMonth"],
			ContractName:       r["ContractName"],
			CallPut:            cp,
		}
		for _, f := range []struct {
			col string
			dst **float64
		}{
			{"OIAtTheMaturityDate(Lot)", &row.OIAtMaturity},
			{"OIOfIn-the-MoneyAtTheMaturityDate(Lot)", &row.OIInTheMoney},
			{"AbandonedOIofIn-the-MoneyAtTheMaturityDate(Lot)", &row.AbandonedOI},
			{"ActualExecutedOI(Lot)", &row.ActualExecutedOI},
		} {
			v, ok := decodeNum(r[f.col])
			rep.note(ok, f.col, r[f.col])
			*f.dst = v
		}
		out.Rows = append(out.Rows, row)
		rep.RowsAccepted++
	}
	rep.finish()
	return out, nil
}

// ContractAlias is one series under both of the exchange's names — the row
// derivative_contracts exists to hold.
type ContractAlias struct {
	Product       string // TXO, the canonical series product
	ExpiryCode    string // 202609F1, the canonical series code
	ExpiryKind    string
	AltProduct    string // TXU
	AltExpiryCode string // 202609
	ContractName  string // 臺指選擇權F1
}

// seriesExpiryCode recovers the series code a settled-positions row belongs to.
//
// The suffix is not lost, it is MOVED: the settlement feed puts it in ContractName
// (臺指選擇權F1) and leaves ContractDeliveryMonth bare (202609). Recombining them gives
// 202609F1 — the code DailyMarketReportOpt and FinalSettlementPriceIndexOptions both use.
//
// Returns "" when the name carries no suffix, which is the honest answer for a genuine
// monthly and keeps this function from inventing a join that does not exist.
func seriesExpiryCode(deliveryMonth, contractName string) string {
	name := strings.TrimSpace(contractName)
	// Walk back over the trailing digits, then expect one suffix letter. F1/F2/F3 and W1..W5
	// are what live data carries; the shape, not the letter set, is what is checked, so an
	// unseen suffix reconciles rather than silently failing to.
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	if i == len(name) || i == 0 {
		return ""
	}
	c := name[i-1]
	if c < 'A' || c > 'Z' {
		return ""
	}
	return strings.TrimSpace(deliveryMonth) + name[i-1:]
}

// ReconcileSettledPositions maps the settlement feed's naming onto the canonical series.
//
// A join keyed on (contract, expiry) matches NOTHING across these two feeds — (TXU, 202609)
// against (TXO, 202609F1) — and it matches nothing SILENTLY, which is the failure this
// function exists to replace with a fact. The bridge is
// FinalSettlementPriceIndexOptions: both feeds agree on the settlement DAY, and the series
// code recovered from ContractName picks the right settlement row out of it.
//
// A settled row that reconciles to nothing is returned in unmatched rather than dropped;
// silently discarding it is how a naming change becomes invisible.
func ReconcileSettledPositions(settled []SettledPositionRow, settlement []SettlementRow) (
	aliases []ContractAlias, unmatched []SettledPositionRow, err error) {

	byKey := map[string]SettlementRow{}
	for _, s := range settlement {
		byKey[s.FinalSettlementDay+"|"+s.ExpiryCode] = s
	}
	seen := map[string]bool{}
	for _, p := range settled {
		code := seriesExpiryCode(p.DeliveryMonth, p.ContractName)
		if code == "" {
			code = strings.TrimSpace(p.DeliveryMonth)
		}
		s, ok := byKey[p.FinalSettlementDay+"|"+code]
		if !ok {
			unmatched = append(unmatched, p)
			continue
		}
		k := s.Product + "|" + s.ExpiryCode
		if seen[k] {
			continue
		}
		seen[k] = true
		aliases = append(aliases, ContractAlias{
			Product:       s.Product,
			ExpiryCode:    s.ExpiryCode,
			ExpiryKind:    s.ExpiryKind,
			AltProduct:    p.Product,
			AltExpiryCode: p.DeliveryMonth,
			ContractName:  p.ContractName,
		})
	}
	if len(settled) > 0 && len(aliases) == 0 {
		err = fmt.Errorf("derivatives: %d settled-position rows reconciled to no series — "+
			"the exchange's naming changed", len(settled))
	}
	return aliases, unmatched, err
}
