package provider

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/taifexfeed"
)

// Source identifiers. These are the endpoint names, stored verbatim on every observation so a
// row that reaches a reader through any path still says where it came from.
const (
	SourceInstitutionalFutures   = "MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate"
	SourceInstitutionalCallsPuts = "MarketDataOfMajorInstitutionalTradersDetailsOfCallsAndPutsBytheDate"
)

// The three institutions the feed reports. 外資及陸資 is the exchange's own label; R15 never
// renames it, and never calls the non-institutional remainder 散戶.
const (
	InstitutionDealer  = "自營商"
	InstitutionTrust   = "投信"
	InstitutionForeign = "外資及陸資"
)

// InstitutionalRow is ONE (institution, product, call/put, semantics, side) observation —
// exactly the key institutional_derivatives uses.
//
// One feed row expands into six of these: FLOW x {LONG, SHORT, NET} and POSITION x the same.
// That expansion is the point of the type. The response carries TradingVolume(*) and
// OpenInterest(*) side by side, and the existing market provider reads only the second half;
// flattening them into one "net" would make the FLOW/POSITION distinction unrecoverable after
// the fact, which §3 exists to prevent.
type InstitutionalRow struct {
	TradingDate string // YYYY-MM-DD
	Product     string // ContractCode, the exchange's Chinese product name: 臺股期貨, 臺指選擇權
	Institution string // Item
	CallPut     string // "" for futures — a sentinel, never NULL, because a nullable key column
	// silently disables SQLite's UNIQUE index for exactly the rows it covers
	Semantics      string // FLOW | POSITION
	Side           string // LONG | SHORT | NET
	Lots           *float64
	ValueThousands *float64
}

// Institutional is a decoded institutional response.
type Institutional struct {
	Source string
	// TradingDate is the OBSERVATION date the feed carries, already converted to YYYY-MM-DD.
	// The fetcher validates it against the session it asked for (ErrDateMismatch); a parser
	// has nothing to validate it against and only reports it.
	TradingDate string
	Rows        []InstitutionalRow
	Report      Report
}

// Column names, spelled once for the whole repository — in internal/taifexfeed, because
// internal/market/provider reads this same response and neither package may import the other
// (§10.2, internal/derivatives/architecture_test.go). These are local names for them, so the
// expansion loop below stays readable; they are not a second spelling.
const (
	colFlowLong  = taifexfeed.ColFlowLong
	colFlowShort = taifexfeed.ColFlowShort
	colFlowNet   = taifexfeed.ColFlowNet
	colFlowValL  = taifexfeed.ColFlowValL
	colFlowValS  = taifexfeed.ColFlowValS
	colFlowValN  = taifexfeed.ColFlowValN
	colPosLong   = taifexfeed.ColPosLong
	colPosShort  = taifexfeed.ColPosShort
	colPosNet    = taifexfeed.ColPosNet
	colPosValL   = taifexfeed.ColPosValL
	colPosValS   = taifexfeed.ColPosValS
	colPosValN   = taifexfeed.ColPosValN
)

// institutionalRequired is identity plus the lot counts of BOTH semantics: this reader stores
// FLOW and POSITION as separate observations, so a response carrying only one half is a layout
// change rather than a quiet day. internal/market/provider requires the narrower POSITION list
// because that is all it reads — same names, different policy about what is required, which is
// the split internal/taifexfeed exists to keep honest.
var institutionalRequired = taifexfeed.ColsInstitutionalFuturesBothSemantics

// ParseInstitutionalFutures decodes the daily institutional futures response.
//
// It extracts BOTH semantics. internal/market/provider's reader consumes only the
// OpenInterest(*) columns and ignores TradingVolume(*), so the FLOW half §3 demands is already
// in bytes this repository fetches today.
//
// There is exactly ONE decode of these bytes, and it is not here: both readers go through
// internal/taifexfeed.Decode, which owns the envelope, the required-column detection and the
// column names (§10.2/FU-10 — two implementations of this loop previously disagreed about the
// same day depending on which entry point ran). What is NOT shared is the policy about what a
// token means: this reader maps "-" to ABSENT, internal/market's parseAmount maps it to 0, and
// those two answers are both correct for their own rows. The divergence is tabulated in
// internal/taifexfeed's package comment and pinned, on the same input bytes, by
// internal/taifexfeed/policy_contrast_test.go.
//
// (An earlier version of this comment claimed "there is no second decoder of OpenInterest(Net)
// here". That was false when written: internal/market/provider had its own taifexRow struct
// decoding the same three columns under the opposite policy. It is true now because the decode
// was moved out, not because it was asserted.)
//
// NET comes from the feed's own net column in every case, never from long - short, so a
// column-layout change surfaces as a mismatch instead of being papered over by arithmetic.
func ParseInstitutionalFutures(body []byte) (*Institutional, error) {
	return parseInstitutional(body, SourceInstitutionalFutures, false)
}

// ParseInstitutionalCallsPuts decodes the 30-row call/put-split institutional options
// response.
//
// The split version is used rather than the 15-row combined one because
// institutional_derivatives keys on call_put: without the dimension the combined feed could
// only be stored by collapsing it back into the shape it was chosen over.
//
// This feed spells the side CALL / PUT in ASCII while every other options feed says 買權 /
// 賣權. Normalisation happens here; an unrecognised value rejects the row rather than
// inventing a third category.
func ParseInstitutionalCallsPuts(body []byte) (*Institutional, error) {
	return parseInstitutional(body, SourceInstitutionalCallsPuts, true)
}

func parseInstitutional(body []byte, source string, withCallPut bool) (*Institutional, error) {
	required := institutionalRequired
	if withCallPut {
		required = append(append([]string{}, required...), "CallPut")
	}
	raw, err := rawRows(source, body, required)
	if err != nil {
		return nil, err
	}

	out := &Institutional{Source: source, Report: Report{Source: source, RowsSeen: len(raw)}}
	rep := &out.Report

	for i, r := range raw {
		date, err := isoDate(r["Date"])
		if err != nil {
			rep.reject(i, "Date", r["Date"], err.Error())
			continue
		}
		// A single-session feed carrying two trading dates is not a bad row, it is a
		// response that cannot be trusted: the fetcher validates ONE date against the
		// session it asked for, and there is no correct answer to "which one".
		if out.TradingDate == "" {
			out.TradingDate = date
		} else if out.TradingDate != date {
			return nil, fmt.Errorf("%w: %s: response carries more than one trading date (%s and %s)",
				ErrStructure, source, out.TradingDate, date)
		}

		callPut := ""
		if withCallPut {
			cp, err := NormalizeCallPut(r["CallPut"])
			if err != nil {
				rep.reject(i, "CallPut", r["CallPut"], err.Error())
				continue
			}
			callPut = cp
		}

		base := InstitutionalRow{
			TradingDate: date,
			Product:     r["ContractCode"],
			Institution: r["Item"],
			CallPut:     callPut,
		}
		if base.Product == "" || base.Institution == "" {
			rep.reject(i, "ContractCode/Item", base.Product+"/"+base.Institution,
				"row has no identity")
			continue
		}

		for _, e := range []struct {
			semantics        string
			side             string
			lotCol, valueCol string
		}{
			{SemanticsFlow, SideLong, colFlowLong, colFlowValL},
			{SemanticsFlow, SideShort, colFlowShort, colFlowValS},
			{SemanticsFlow, SideNet, colFlowNet, colFlowValN},
			{SemanticsPosition, SideLong, colPosLong, colPosValL},
			{SemanticsPosition, SideShort, colPosShort, colPosValS},
			{SemanticsPosition, SideNet, colPosNet, colPosValN},
		} {
			lots, ok := decodeNum(r[e.lotCol])
			rep.note(ok, e.lotCol, r[e.lotCol])
			val, okv := decodeNum(r[e.valueCol])
			rep.note(okv, e.valueCol, r[e.valueCol])

			row := base
			row.Semantics, row.Side = e.semantics, e.side
			row.Lots, row.ValueThousands = lots, val
			out.Rows = append(out.Rows, row)
		}
		rep.RowsAccepted++
	}
	rep.finish()
	return out, nil
}
