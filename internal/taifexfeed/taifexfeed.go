// Package taifexfeed holds the ONE decode of a TAIFEX OpenAPI response, and the column names
// of the institutional-futures feed, spelled once.
//
// It exists for the same reason internal/numtoken does, one level up: two readers need the
// SAME decode of the same bytes and keep OPPOSITE policies about what a token means. R15
// §10.2 (FU-10) requires exactly one code path that turns an institutional-futures response
// into a domain value, because two of them disagreed about the same day depending on which
// entry point ran.
//
// Neutral by construction. This package imports neither reader, and both import it:
//
//	internal/market/provider ───┐
//	                            ├──> internal/taifexfeed ──> internal/numtoken
//	internal/derivatives/provider ─┘
//
// internal/derivatives must stay a leaf of the decision path (internal/derivatives/architecture_test.go),
// so internal/market/provider must never import it and it must never import internal/market.
// A shared package that imports neither is the only shape that lets both share a decode.
//
// # What is shared, and what is deliberately not
//
// SHARED: the envelope decode (a flat JSON array of objects whose values are all strings),
// the required-column detection, and the column names.
//
// NOT SHARED: what a cleaned-but-unparsable token MEANS. The two readers diverge on exactly
// one class of token and it is a decision, not an accident:
//
//	token        internal/market/provider        internal/derivatives/provider
//	             parseAmount (twse.go)           decodeNum (decode.go)
//	---------------------------------------------------------------------------------
//	"1234"       1234                            1234, present
//	"-1"         -1                              -1, present
//	"0"          0                               0, present
//	""           0                               ABSENT (nil), a known sentinel
//	"-"          0                               ABSENT (nil), a known sentinel
//	"NULL"       error                           ABSENT (nil), a known sentinel
//	"暫停交易"     error                           ABSENT (nil), reported as an unknown token
//
// Why they must differ. On the TWSE cash rows internal/market reads, an empty cell genuinely
// IS a zero, and that behaviour is shipped and pinned by internal/market/provider/twse_test.go.
// On the TAIFEX derivatives rows R15 reads, "-" means "not carried" while "0" means "a strike
// nobody holds" (2,868 vs 1,295 rows of the 2026-09-04 by-strike capture); storing both as 0
// erases the distinction the whole layer rests on, and treating "-" as an error flips the OI
// aggregate to MISSING and fails the §10.1 reconciliation gate.
//
// Giving either reader the other's policy is therefore a real regression in a known direction,
// which is why the divergence survives the merge of the decode. It is pinned side by side, on
// the SAME input bytes, by internal/taifexfeed/policy_contrast_test.go — change either policy
// and that test fails, so it becomes a decision instead of a drift.
//
// See docs/SPEC_R15_TAIFEX_DERIVATIVES_RISK.md §3.1, §6.2, §10.2.
package taifexfeed

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Column names of the institutional-futures feed
// (MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate), which is the one
// endpoint both readers consume.
//
// The keys carry parentheses, so nothing that reads this feed can rely on Go field-name
// inference; spelling them once here is what makes "the same column" a compile-time fact
// rather than two string literals that happen to match today.
//
// The FLOW half (TradingVolume / TradingValue) and the POSITION half (OpenInterest /
// ContractValueofOpenInterest) sit side by side in every row. §10.2 asks that the decode be
// widened from the POSITION half to both, and it is: the decode returns every column the
// response carried. Which of them a reader consumes is that reader's business — see
// ColsInstitutionalFuturesPosition.
const (
	ColDate         = "Date"
	ColContractCode = "ContractCode"
	ColItem         = "Item"

	ColFlowLong  = "TradingVolume(Long)"
	ColFlowShort = "TradingVolume(Short)"
	ColFlowNet   = "TradingVolume(Net)"
	ColFlowValL  = "TradingValue(Long)(Thousands)"
	ColFlowValS  = "TradingValue(Short)(Thousands)"
	ColFlowValN  = "TradingValue(Net)(Thousands)"

	ColPosLong  = "OpenInterest(Long)"
	ColPosShort = "OpenInterest(Short)"
	ColPosNet   = "OpenInterest(Net)"
	ColPosValL  = "ContractValueofOpenInterest(Long)(Thousands)"
	ColPosValS  = "ContractValueofOpenInterest(Short)(Thousands)"
	ColPosValN  = "ContractValueofOpenInterest(Net)(Thousands)"
)

// ColsInstitutionalFuturesIdentity is what identifies a row of that feed: which day, which
// product, which institution. A reader that cannot resolve these has no row at all.
var ColsInstitutionalFuturesIdentity = []string{ColDate, ColContractCode, ColItem}

// ColsInstitutionalFuturesBothSemantics is identity plus the lot counts of BOTH semantics —
// what internal/derivatives/provider requires, because it stores FLOW and POSITION as separate
// observations and a response missing either half is a layout change, not a quiet day.
var ColsInstitutionalFuturesBothSemantics = concat(ColsInstitutionalFuturesIdentity,
	[]string{ColFlowLong, ColFlowShort, ColFlowNet, ColPosLong, ColPosShort, ColPosNet})

// ColsInstitutionalFuturesPosition is identity plus the POSITION lot counts — what
// internal/market/provider requires.
//
// Narrower than the list above ON PURPOSE, and this is the one place the difference is
// visible. internal/market reads foreign net OI for the market snapshot and nothing else, so
// requiring the FLOW columns there would take a shipped, decision-path reader offline over a
// half of the response it never looks at. Requiring only what you read is a per-reader policy;
// the NAMES are shared, so a rename still cannot be spelled two ways.
var ColsInstitutionalFuturesPosition = concat(ColsInstitutionalFuturesIdentity,
	[]string{ColPosLong, ColPosShort, ColPosNet})

func concat(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

// The ways a response can fail to decode at all. Typed so each caller can translate them into
// its own vocabulary — ErrStructure in R15, ErrNoData in internal/market — without matching on
// message text.
var (
	// ErrEmptyBody: no bytes at all.
	ErrEmptyBody = errors.New("empty body")
	// ErrNotJSON: the body is not JSON, most often an HTML error or maintenance page.
	ErrNotJSON = errors.New("response is not JSON")
	// ErrEnvelope: the body is JSON but not this feed's shape — including a value that is a
	// JSON number rather than a string, which is a layout change and says so instead of being
	// reformatted through a float.
	ErrEnvelope = errors.New("undecodable envelope")
	// ErrNoRows: a well-formed but empty array, where the exchange should have returned rows.
	ErrNoRows = errors.New("payload carries no rows")
	// ErrColumnsAbsent: a REQUIRED column is not in the response. See ColumnsAbsentError.
	ErrColumnsAbsent = errors.New("required column(s) absent")
)

// ColumnsAbsentError names the required columns the response did not carry, so a layout change
// is reported as the specific rename it is.
type ColumnsAbsentError struct{ Columns []string }

func (e *ColumnsAbsentError) Error() string {
	return fmt.Sprintf("required column(s) absent — layout changed: %v", e.Columns)
}

func (e *ColumnsAbsentError) Is(target error) bool { return target == ErrColumnsAbsent }

// Decode decodes the envelope every TAIFEX OpenAPI feed uses — a flat array of objects whose
// values are all JSON strings — and checks that the first row carries every required column.
//
// It returns map[string]string rather than a struct, and that is the whole point.
// json.Unmarshal into a struct fills a missing key with the zero value, so a RENAMED column
// arrives as an empty string, decodes to 0 or to ABSENT depending on the reader's policy, and
// produces a plausible successful response with a whole dimension silently gone. A map decode
// can tell "the column is not here" from "the column is here and empty"; a struct cannot, at
// any layer above it.
//
// It makes NO decision about what any value MEANS. Every value comes back as the raw token the
// exchange sent, whitespace and all, for the caller to interpret under its own policy.
func Decode(body []byte, required []string) ([]map[string]string, error) {
	if len(body) == 0 {
		return nil, ErrEmptyBody
	}
	// An HTML error page decodes as neither an array nor an object, but the message
	// "invalid character '<'" is not a useful thing to read at 3am.
	if t := strings.TrimSpace(string(body[:min(len(body), 512)])); strings.HasPrefix(t, "<") {
		return nil, fmt.Errorf("%w (looks like an HTML error page)", ErrNotJSON)
	}
	var rows []map[string]string
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvelope, err)
	}
	if len(rows) == 0 {
		return nil, ErrNoRows
	}
	var absent []string
	for _, col := range required {
		if _, ok := rows[0][col]; !ok {
			absent = append(absent, col)
		}
	}
	if len(absent) > 0 {
		return nil, &ColumnsAbsentError{Columns: absent}
	}
	return rows, nil
}
