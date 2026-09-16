package provider

// SourcePutCallRatio is the exchange's own put/call ratio.
const SourcePutCallRatio = "PutCallRatio"

// PutCallRatioRow is one session of the official ratio.
//
// This is the reconciliation gate's other side (§10.1): a computed volume PCR and OI PCR must
// equal these figures for the same session, or the aggregate is ERROR with the discrepancy
// recorded rather than a number. An unchecked number that happens to be right is
// indistinguishable from one that happens to be wrong.
type PutCallRatioRow struct {
	TradingDate    string
	PutVolume      *float64
	CallVolume     *float64
	VolumeRatioPct *float64
	PutOI          *float64
	CallOI         *float64
	OIRatioPct     *float64
}

// PutCallRatio is a decoded PutCallRatio response.
//
// Rows is ordered exactly as the feed returned it and is NOT reduced to one session, because
// this endpoint is the one that behaves differently: it carries about 23 sessions of history
// while every other R15 endpoint answers with one. Reducing it here would throw away the only
// history R15 gets without a backfill.
type PutCallRatio struct {
	Source string
	Rows   []PutCallRatioRow
	Report Report
}

// ByDate returns the row for one session, or nil.
func (p *PutCallRatio) ByDate(date string) *PutCallRatioRow {
	for i := range p.Rows {
		if p.Rows[i].TradingDate == date {
			return &p.Rows[i]
		}
	}
	return nil
}

var putCallRatioRequired = []string{"Date", "PutVolume", "CallVolume", "PutOI", "CallOI"}

// ParsePutCallRatio decodes the official ratio feed.
//
// The two ratio columns are declared percentages: their key names end in "%" and, although
// the live values are bare ("98.45"), the value is normalised through the percent path so a
// day the exchange starts printing "98.45%" is not a day 23 sessions turn ABSENT.
func ParsePutCallRatio(body []byte) (*PutCallRatio, error) {
	raw, err := rawRows(SourcePutCallRatio, body, putCallRatioRequired)
	if err != nil {
		return nil, err
	}
	out := &PutCallRatio{
		Source: SourcePutCallRatio,
		Report: Report{Source: SourcePutCallRatio, RowsSeen: len(raw)},
	}
	rep := &out.Report

	for i, r := range raw {
		// Unlike every other feed here, a row's date is its identity rather than a
		// property shared with the response: 23 rows, 23 sessions.
		date, err := isoDate(r["Date"])
		if err != nil {
			rep.reject(i, "Date", r["Date"], err.Error())
			continue
		}
		row := PutCallRatioRow{TradingDate: date}
		for _, f := range []struct {
			col string
			dst **float64
		}{
			{"PutVolume", &row.PutVolume}, {"CallVolume", &row.CallVolume},
			{"PutOI", &row.PutOI}, {"CallOI", &row.CallOI},
		} {
			v, ok := decodeNum(r[f.col])
			rep.note(ok, f.col, r[f.col])
			*f.dst = v
		}
		for _, f := range []struct {
			col string
			dst **float64
		}{
			{"PutCallVolumeRatio%", &row.VolumeRatioPct},
			{"PutCallOIRatio%", &row.OIRatioPct},
		} {
			v, ok := decodePercent(r[f.col])
			rep.note(ok, f.col, r[f.col])
			*f.dst = v
		}
		out.Rows = append(out.Rows, row)
		rep.RowsAccepted++
	}
	rep.finish()
	return out, nil
}
