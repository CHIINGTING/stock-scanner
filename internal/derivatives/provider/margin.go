package provider

// SourceMargin is the exchange's published margin table: 31 contracts on the 2026-09-04
// capture, including the three 臺指選擇權風險保證金 (A)/(B)/(C) rows.
//
// It exists, and finding it required reading the 135-endpoint catalogue rather than guessing
// URLs — an earlier draft probed a dozen invented names, collected HTTP 302s, and concluded
// margin had no official source, which would have led M7 to hand-maintain a table that was
// staler than the feed and had no staleness rule at all.
const SourceMargin = "IndexFuturesAndOptionsMargining"

// MarginRow is one contract's margin requirement.
//
// EffectiveDate is the exchange's EFFECTIVE date, not a publication date: it changes only
// when margins change. That is why it never validates against the requested session —
// ErrDateMismatch would fire on every day without a margin revision and leave M7 permanently
// at margin_source: NONE — and why the snapshot records date_source: ASSIGNED.
//
// It is NOT NULL in margin_rates and part of the key, so a row whose date will not parse is
// rejected rather than stored dateless.
type MarginRow struct {
	Product           string
	EffectiveDate     string // YYYY-MM-DD
	ClearingMargin    *float64
	MaintenanceMargin *float64
	InitialMargin     *float64
}

// Margin is a decoded margin response.
type Margin struct {
	Source string
	Rows   []MarginRow
	Report Report
}

var marginRequired = []string{"Contract", "ClearingMargin", "MaintenanceMargin", "InitialMargin", "Date"}

// ParseMargin decodes the margin table.
//
// Rows are returned as published. The carry-forward M7 needs — the most recent effective date
// <= D — is bounded to the snapshot archived for D and is therefore the reader's job, not the
// parser's: a failed fetch for D means no snapshot for D, which means margin_source: NONE.
// The exchange's "applies until changed" licenses reusing a value observed FOR D, not reusing
// an observation never made.
func ParseMargin(body []byte) (*Margin, error) {
	raw, err := rawRows(SourceMargin, body, marginRequired)
	if err != nil {
		return nil, err
	}
	out := &Margin{Source: SourceMargin, Report: Report{Source: SourceMargin, RowsSeen: len(raw)}}
	rep := &out.Report

	for i, r := range raw {
		eff, err := isoDate(r["Date"])
		if err != nil {
			rep.reject(i, "Date", r["Date"], err.Error())
			continue
		}
		if r["Contract"] == "" {
			rep.reject(i, "Contract", "", "row has no identity")
			continue
		}
		row := MarginRow{Product: r["Contract"], EffectiveDate: eff}
		for _, f := range []struct {
			col string
			dst **float64
		}{
			{"ClearingMargin", &row.ClearingMargin},
			{"MaintenanceMargin", &row.MaintenanceMargin},
			{"InitialMargin", &row.InitialMargin},
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
