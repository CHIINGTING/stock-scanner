// Package provider parses TAIFEX's OpenAPI responses into R15's domain rows.
//
// It is PURE: nothing here opens a socket, reads a clock, or touches a database. A ParseX
// takes the bytes a fetcher already has and returns rows plus a Report; deciding which
// session those bytes belong to, validating the response date against it, hashing and
// archiving them are the fetcher's job (M3 part B), and every test in this package runs off
// committed fixtures.
//
// Nothing in internal/scanner may import this package; `go list -deps ./internal/scanner`
// enforces it.
//
// See docs/SPEC_R15_TAIFEX_DERIVATIVES_RISK.md §3, §3.1, §6.1, §6.2, §7.2.
package provider

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/deep-huang/stock-scanner/internal/numtoken"
	"github.com/deep-huang/stock-scanner/internal/taifexfeed"
)

// ── the value envelope ────────────────────────────────────────────────────────────────

// value_semantics (§3). Stored, not a naming convention: 外資 can sell 3,000 lots on a day
// their net long POSITION rises, because the sale closed shorts, so a single "net" field
// carrying whichever the caller assumed makes the two indistinguishable.
const (
	SemanticsFlow     = "FLOW"     // what was traded during the session
	SemanticsPosition = "POSITION" // open exposure at the close
)

// side. NET is always read from the feed's own net column and never derived from long-short,
// so a column-layout change surfaces as a mismatch instead of being papered over.
const (
	SideLong  = "LONG"
	SideShort = "SHORT"
	SideNet   = "NET"
)

// trading_session, as internal/derivatives spells it. The feed's own 一般 / 盤後 decides,
// never the clock at fetch time.
const (
	SessionDay        = "DAY"
	SessionAfterHours = "AFTER_HOURS"
	SessionCombined   = "COMBINED"
)

// call_put, normalised at this boundary. The feeds disagree about spelling (§7.2): the
// by-strike report says 買權/賣權, the institutional calls-and-puts feed says CALL/PUT.
const (
	CallSide = "CALL"
	PutSide  = "PUT"
)

// ── the normalisation policy ──────────────────────────────────────────────────────────

// knownAbsent lists the tokens TAIFEX uses for "no value carried", as observed across every
// numeric column of every endpoint R15 reads (100,274 "-", 1,979 "", 157 "NULL" in the
// 2026-09-04 capture).
//
// The list is NOT the rule. The rule is "anything that does not parse is absent"; this list
// only decides whether the absence is worth telling data health about. Three earlier drafts
// of the spec enumerated sentinels and were wrong each time, which is why the enumeration was
// demoted to a reporting aid.
var knownAbsent = map[string]bool{"": true, "-": true, "NULL": true, "null": true}

// decodeNum applies §3.1 to one token, for a NULLABLE column.
//
//	normalise -> parse -> present
//	normalise -> does not parse -> ABSENT (nil), and NOT an error
//
// "0" parses, and is an observed zero: on the by-strike report 1,295 TXO rows carry a real 0
// (a strike nobody holds) next to 2,868 carrying "-" (a value not carried at all). Storing
// both as 0 erases the distinction the whole layer rests on; treating "-" as a parse error
// flips the OI aggregate to MISSING and fails the §10.1 reconciliation gate. Both plausible
// shortcuts fail, in opposite directions.
//
// ok reports whether the token was RECOGNISED — parsed, or a known absence sentinel. A false
// ok still yields an absent value; it additionally means "a token nobody has seen before",
// which the caller records so a new sentinel becomes visible instead of silent.
func decodeNum(raw string) (v *float64, ok bool) {
	return decodeToken(numtoken.Normalize(raw))
}

// decodePercent is decodeNum for a column declared to be a percentage.
//
// The suffix strip is not decoration. DailyMarketReportFut's "%" column carries 727 genuine
// values ("9.98%") beside 1,467 "-" and 56 "0.00%". Without it every real value decodes to
// ABSENT and is logged unknown: 783 lines of daily noise while 727 observations disappear.
func decodePercent(raw string) (v *float64, ok bool) {
	return decodeToken(numtoken.NormalizePercent(raw))
}

func decodeToken(s string) (*float64, bool) {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return &f, true
	}
	return nil, knownAbsent[s]
}

// decodeRequiredNum applies §6.2 to a token in a NOT NULL column.
//
// The two rules only look contradictory. §3.1 makes an unparsable token ABSENT, which the
// schema can express for oi / volume / settlement / delta / margin — every one of them
// nullable, precisely so an absence has somewhere to live. It cannot express an absent
// options_oi_by_strike.strike: the column is `REAL NOT NULL` and part of the UNIQUE key, so a
// row whose strike will not parse has no identity and cannot be stored at all. That is
// §6.2's rejected row — counted, reported, status PARTIAL — and it is a different thing from
// a row carrying "-", which is an observation of absence and is stored normally.
func decodeRequiredNum(raw string) (float64, error) {
	s := numtoken.Normalize(raw)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", raw)
	}
	return f, nil
}

// ── what a parse reports back ─────────────────────────────────────────────────────────

// RejectedRow is one row §6.2 refused to store, with enough detail to fix the cause.
//
// Rejected rows are COUNTED rather than dropped: an aggregate whose correctness depends on
// completeness may not be published from a PARTIAL snapshot, and a count is what lets the
// caller know it is one.
type RejectedRow struct {
	Index  int    // position in the response, 0-based
	Field  string // the column that failed
	Token  string // the raw token, verbatim
	Reason string
}

func (r RejectedRow) String() string {
	return fmt.Sprintf("row %d: %s=%q: %s", r.Index, r.Field, r.Token, r.Reason)
}

// UnknownToken is a token that neither parsed nor matched a known absence sentinel.
//
// It does not fail anything — §3.1 makes it ABSENT like any other unparsable token. It is
// carried so that the day TAIFEX invents a fourth sentinel, data health says so on the first
// session instead of the layer quietly losing a column.
type UnknownToken struct {
	Field string
	Token string
	Count int
}

// Report is the per-response accounting §6.2 needs to choose between AVAILABLE and PARTIAL.
type Report struct {
	Source        string
	RowsSeen      int
	RowsAccepted  int
	Rejected      []RejectedRow
	UnknownTokens []UnknownToken

	unknown map[[2]string]int
}

// Status is §6's AVAILABLE or PARTIAL for a response that decoded structurally.
//
// Only these two: a structural failure never produces a Report at all, it produces an error
// and no rows (§6.2's whole-response rejection). MISSING, STALE, NOT_PUBLISHED and NO_SESSION
// are the fetcher's answers about a response that does not exist, which a parser cannot see.
func (r *Report) Status() string {
	if len(r.Rejected) > 0 {
		return "PARTIAL"
	}
	return "AVAILABLE"
}

func (r *Report) reject(index int, field, token, reason string) {
	r.Rejected = append(r.Rejected, RejectedRow{Index: index, Field: field, Token: token, Reason: reason})
}

// note records an unrecognised token. Recognised absences ("-", "", "NULL") are silent: they
// are normal data, and logging 100,274 of them a day would bury the one that is not.
func (r *Report) note(ok bool, field, token string) {
	if ok {
		return
	}
	if r.unknown == nil {
		r.unknown = map[[2]string]int{}
	}
	r.unknown[[2]string{field, token}]++
}

// finish materialises the unknown-token census in a deterministic order, so a test and a
// health panel see the same list.
func (r *Report) finish() {
	for k, n := range r.unknown {
		r.UnknownTokens = append(r.UnknownTokens, UnknownToken{Field: k[0], Token: k[1], Count: n})
	}
	sort.Slice(r.UnknownTokens, func(i, j int) bool {
		a, b := r.UnknownTokens[i], r.UnknownTokens[j]
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		return a.Token < b.Token
	})
	r.unknown = nil
}

// ── structural decoding (§6.2 whole-response rejection) ───────────────────────────────

// ErrStructure marks a response that cannot be trusted as a whole: nothing is stored, the
// status is ERROR, and no partial answer is offered.
var ErrStructure = fmt.Errorf("derivatives: structural failure")

// rawRows is the R15 side of internal/taifexfeed.Decode: the SAME decode every reader of a
// TAIFEX response uses, wrapped in this layer's vocabulary.
//
// The decode lives in a neutral package because internal/market/provider reads the same
// institutional-futures response and R15 must stay a leaf of the decision path
// (internal/derivatives/architecture_test.go), so neither reader can import the other. §10.2
// requires exactly ONE code path from those bytes to a domain value; before this, there were
// two, and they disagreed on the same row because their policies about "-" differ.
//
// What is added here is only the vocabulary: every failure the shared decode reports is a §6.2
// whole-response rejection, so it becomes an ErrStructure naming the endpoint. The POLICY
// about token meaning stays in decodeNum above and is deliberately NOT shared — see the table
// in internal/taifexfeed's package comment.
func rawRows(source string, body []byte, required []string) ([]map[string]string, error) {
	rows, err := taifexfeed.Decode(body, required)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrStructure, source, err)
	}
	return rows, nil
}
