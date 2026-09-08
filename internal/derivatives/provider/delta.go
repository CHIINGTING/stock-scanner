package provider

// SourceOptionsDelta is the per-strike delta feed.
const SourceOptionsDelta = "DailyOptionsDelta"

// DeltaRow is one strike's delta.
//
// ContractSettlementDay is a FUTURE date, per row — the day that contract will settle — and
// is therefore never validated against the session being fetched. The feed carries no
// observation date at all, so its snapshot records date_source: ASSIGNED.
type DeltaRow struct {
	Product               string
	ExpiryCode            string
	ExpiryKind            string
	Strike                float64
	CallPut               string
	Delta                 *float64
	ContractSettlementDay string // YYYY-MM-DD, in the future
}

// OptionsDelta is a decoded delta response.
type OptionsDelta struct {
	Source string
	Rows   []DeltaRow
	Report Report
}

// HasExpiry reports whether the feed carried any row for one series.
//
// It exists to be asked, and to be answered "no" without consequence. DailyOptionsDelta OMITS
// the expiry that settles today: 202609F1 is absent from it while the by-strike report shows
// 126,594 lots against it. Absence here means "no delta is available for that contract" and
// NOTHING else — in particular it never makes a contract UNRESOLVED. An earlier draft let it,
// and the resulting "UNRESOLVED is counted in every total" put the settling expiry straight
// back into open interest: 223,014 against an official 96,420, a 2.31x error.
func (d *OptionsDelta) HasExpiry(product, expiryCode string) bool {
	for _, r := range d.Rows {
		if r.Product == product && r.ExpiryCode == expiryCode {
			return true
		}
	}
	return false
}

var optionsDeltaRequired = []string{
	"Contract", "CallPut", "ContractMonth(Week)", "StrikePrice", "Delta", "ContractSettlementDay",
}

// ParseOptionsDelta decodes DailyOptionsDelta.
func ParseOptionsDelta(body []byte) (*OptionsDelta, error) {
	raw, err := rawRows(SourceOptionsDelta, body, optionsDeltaRequired)
	if err != nil {
		return nil, err
	}
	out := &OptionsDelta{
		Source: SourceOptionsDelta,
		Report: Report{Source: SourceOptionsDelta, RowsSeen: len(raw)},
	}
	rep := &out.Report
	for i, r := range raw {
		cp, err := NormalizeCallPut(r["CallPut"])
		if err != nil {
			rep.reject(i, "CallPut", r["CallPut"], err.Error())
			continue
		}
		strike, err := decodeRequiredNum(r["StrikePrice"])
		if err != nil {
			rep.reject(i, "StrikePrice", r["StrikePrice"], err.Error())
			continue
		}
		day, err := isoDate(r["ContractSettlementDay"])
		if err != nil {
			rep.reject(i, "ContractSettlementDay", r["ContractSettlementDay"], err.Error())
			continue
		}
		if r["Contract"] == "" || r["ContractMonth(Week)"] == "" {
			rep.reject(i, "Contract/ContractMonth(Week)",
				r["Contract"]+"/"+r["ContractMonth(Week)"], "row has no identity")
			continue
		}
		delta, ok := decodeNum(r["Delta"])
		rep.note(ok, "Delta", r["Delta"])
		out.Rows = append(out.Rows, DeltaRow{
			Product:               r["Contract"],
			ExpiryCode:            r["ContractMonth(Week)"],
			ExpiryKind:            ClassifyExpiry(r["ContractMonth(Week)"]),
			Strike:                strike,
			CallPut:               cp,
			Delta:                 delta,
			ContractSettlementDay: day,
		})
		rep.RowsAccepted++
	}
	rep.finish()
	return out, nil
}
