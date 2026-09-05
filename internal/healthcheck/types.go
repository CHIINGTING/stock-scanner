// Package healthcheck answers one question about one stock, on demand: how healthy does the
// evidence say this company and this chart are, as of a given date?
//
// # It is a SIDECAR, and that is structural rather than aspirational
//
// Nothing in this package is imported by internal/scanner, and nothing it produces is ever
// handed back to a scan. The dependency arrow points one way:
//
//	scanner ──► WatchlistEntry ──► healthcheck ──► Health ──► httpapi ──► dashboard
//
// So BUY / WATCH / SELL, the rocket score, the ranking, the candidate filter and the market
// regime cannot be affected by anything here, whatever a future caller does.
//
// # The AI is not allowed to produce a financial fact
//
// Every number in a Health — price, EPS, revenue, YoY, margins, P/E, target price, upside,
// institutional net buy — is produced by a deterministic path from an existing evidence
// module, and is COMPUTED BEFORE the model is called. The judge receives that finished
// evidence and returns prose. Its reply is merged into a field that holds no numbers, so
// there is no code path by which a model's output could become a value on this page.
//
// # MISSING is never ZERO
//
// The repo-wide contract, enforced here by the Value type: a metric carries a STATUS and a
// pre-rendered Display string. A renderer that wants to print a number has to go through
// Display, and Display says INSUFFICIENT_DATA when there is no number. There is no path
// where an absent P/E renders as "0x".
package healthcheck

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Status is the availability vocabulary, shared with internal/fundamental and
// internal/valuation so a consumer reads ONE contract across the whole page.
type Status string

const (
	// StatusAvailable — the value is present and usable.
	StatusAvailable Status = "AVAILABLE"
	// StatusPartial — the block produced some of what it can produce.
	StatusPartial Status = "PARTIAL"
	// StatusInsufficientData — computable in principle, but not enough observations exist
	// yet. "Wait", as opposed to UNAVAILABLE's "there is nothing to wait for".
	StatusInsufficientData Status = "INSUFFICIENT_DATA"
	// StatusUnavailable — the source was consulted and had nothing.
	StatusUnavailable Status = "UNAVAILABLE"
	// StatusDisabled — the feature is switched off. A decision, not a data fact.
	StatusDisabled Status = "DISABLED"
	// StatusNotApplicable — the metric cannot apply to this security (an ETF has no EPS).
	StatusNotApplicable Status = "NOT_APPLICABLE"
	// StatusNotImplemented — this repo has no authorized source for the metric at all.
	StatusNotImplemented Status = "NOT_IMPLEMENTED"
)

// OK reports whether a status carries a usable value.
func (s Status) OK() bool { return s == StatusAvailable }

// Value is a number that might not exist.
//
// The pre-rendered Display is the point of the type. A template holding {Status, Value} will
// eventually print Value and forget Status — every UI does, once — and "0.00x" beside a
// company with no published P/E is a lie that looks like data. Display is always safe to
// print, and is never "0" or "-" for a missing value.
type Value struct {
	Status Status `json:"status"`
	// Value is meaningful only when Status is AVAILABLE. It is a pointer so that a genuine
	// 0 (a company really did break even) stays distinct from "no reading".
	Value *float64 `json:"value,omitempty"`
	// Display is what a UI prints, always. Never empty.
	Display string `json:"display"`
	// Unit is "TWD", "%", "x", "" — carried so a caller never has to guess the scale.
	Unit string `json:"unit,omitempty"`
	// Reason explains a non-AVAILABLE status in one line.
	Reason string `json:"reason,omitempty"`
	// Source names where the number came from, so any figure on the page is traceable.
	Source string `json:"source,omitempty"`
	// AsOf is the date this particular figure describes, when it differs from the request's.
	AsOf string `json:"as_of,omitempty"`
}

// Num builds an available value with the given formatting.
//
// A non-finite input is rejected rather than formatted: NaN would render as "NaN" and Inf as
// "+Inf", and both look enough like output to be believed.
func Num(v float64, unit string, decimals int, source string) Value {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Missing(StatusUnavailable, "not a finite number")
	}
	val := v
	return Value{
		Status:  StatusAvailable,
		Value:   &val,
		Display: formatNumber(v, unit, decimals),
		Unit:    unit,
		Source:  source,
	}
}

// Missing builds a value with no number. status must not be AVAILABLE.
func Missing(status Status, reason string) Value {
	if status == StatusAvailable {
		// A caller that reached here has a bug; recording it as UNAVAILABLE with the reason
		// beats emitting an AVAILABLE value with no number behind it.
		status = StatusUnavailable
		reason = "internal: Missing called with AVAILABLE (" + reason + ")"
	}
	return Value{Status: status, Display: string(status), Reason: reason}
}

// WithSource returns a copy carrying provenance.
func (v Value) WithSource(src string) Value { v.Source = src; return v }

// WithAsOf returns a copy carrying the date the figure describes.
func (v Value) WithAsOf(date string) Value { v.AsOf = date; return v }

// Qualified returns a copy downgraded to a non-AVAILABLE status while KEEPING the rendered
// number.
//
// It exists for a figure that is real but tainted: a 5-day institutional cumulative that
// spans a session with no record is a genuine sum of what was observed, and printing it
// unmarked would claim a completeness the record does not have. Unlike Missing, Display
// still shows the figure — the caveat rides in Status and Reason, and Float() stops
// returning it, so no downstream rule can treat it as clean.
func (v Value) Qualified(status Status, reason string) Value {
	if status == StatusAvailable {
		return v
	}
	v.Status = status
	v.Reason = reason
	return v
}

// Float returns the number and whether there is one.
func (v Value) Float() (float64, bool) {
	if v.Status != StatusAvailable || v.Value == nil {
		return 0, false
	}
	return *v.Value, true
}

// formatNumber renders a figure with thousands separators and a unit suffix.
func formatNumber(v float64, unit string, decimals int) string {
	s := humanise(v, decimals)
	switch unit {
	case "%":
		return s + "%"
	case "x":
		return s + "x"
	case "":
		return s
	default:
		return s + " " + unit
	}
}

// humanise formats with thousands separators.
func humanise(v float64, decimals int) string {
	s := formatFixed(v, decimals)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String() + frac
	if neg {
		out = "-" + out
	}
	return out
}

func formatFixed(v float64, decimals int) string {
	return fmt.Sprintf("%.*f", decimals, v)
}

// ── identity ──────────────────────────────────────────────────────────────────────────

// MarketSource says how the market was determined. RESOLVED means the price fetcher found
// the stock on one exchange after the catalog said it did not know — a fact discovered, not
// assumed.
const (
	MarketSourceCatalog  = "CATALOG"
	MarketSourceResolved = "RESOLVED"
	MarketSourceUnknown  = "UNKNOWN"
)

// Identity is the ONE answer to "which stock is this". It travels whole; no consumer ever
// carries a symbol without the name it belongs to.
type Identity struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
	// Market is the display label: TWSE / TPEX / UNKNOWN.
	Market string `json:"market"`
	// MarketCode is the internal form (TW / TWO), empty when unknown.
	MarketCode string `json:"market_code,omitempty"`
	// MarketSource is CATALOG / RESOLVED / UNKNOWN.
	MarketSource string `json:"market_source"`
	// CanonicalSymbol is "2330.TW" — the key every archive is filed under. Empty while the
	// market is unknown, because there is no canonical symbol until it is known.
	CanonicalSymbol string   `json:"canonical_symbol,omitempty"`
	Industry        string   `json:"industry,omitempty"`
	Aliases         []string `json:"aliases,omitempty"`
	Tags            []string `json:"tags,omitempty"`
}

// ── point-in-time reporting ───────────────────────────────────────────────────────────

// PITMode describes how faithfully one source honours a historical asOf.
type PITMode string

const (
	// PITAsOf — the source selects the latest observation at or before asOf. Fully safe.
	PITAsOf PITMode = "AS_OF"
	// PITTruncated — the full series was read and then cut at asOf by this package.
	PITTruncated PITMode = "TRUNCATED"
	// PITExactDate — the source is keyed on an exact date and has no as-of semantics: a date
	// with no file yields nothing rather than the previous day's.
	PITExactDate PITMode = "EXACT_DATE"
	// PITLatestOnly — the source can only supply its newest state. Anything read from it is
	// NOT historical, and this package refuses to present it as though it were.
	PITLatestOnly PITMode = "LATEST_ONLY"
)

// SourcePIT records one source's point-in-time behaviour, so the answer to "is this page
// historically honest" is on the page rather than in a comment.
type SourcePIT struct {
	Source string  `json:"source"`
	Mode   PITMode `json:"mode"`
	Detail string  `json:"detail,omitempty"`
}

// Limitation is something this build cannot do, stated plainly.
type Limitation struct {
	Area   string `json:"area"`
	Detail string `json:"detail"`
}

// ── data quality ──────────────────────────────────────────────────────────────────────

// QualityLevel summarises EVIDENCE COMPLETENESS. It is not a rating of the stock: a company
// with perfect data and terrible prospects is COMPLETE.
type QualityLevel string

const (
	QualityComplete     QualityLevel = "COMPLETE"
	QualityPartial      QualityLevel = "PARTIAL"
	QualityInsufficient QualityLevel = "INSUFFICIENT"
)

// BlockStatus is one evidence area's availability, for the data-quality panel.
type BlockStatus struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// DataQuality is the honesty report for one health check.
type DataQuality struct {
	Level  QualityLevel  `json:"level"`
	Blocks []BlockStatus `json:"blocks"`
	// PointInTime lists every source consulted and how it treats asOf.
	PointInTime []SourcePIT `json:"point_in_time"`
	// Limitations are the things this build cannot answer, named rather than left blank.
	Limitations []Limitation `json:"limitations,omitempty"`
}

// ── request ───────────────────────────────────────────────────────────────────────────

// Request is one on-demand health check.
type Request struct {
	// Symbol is a catalog code ("2330"), a canonical symbol ("2330.TW") or an alias.
	Symbol string
	// AsOf is the analysis date, YYYY-MM-DD. Empty → today in Taipei.
	AsOf string
	// SkipAI forces the AI section to DISABLED for this request without changing config.
	// Everything else in the answer is produced identically.
	SkipAI bool
}

// Taipei is the market's timezone. Every date this package derives from a wall clock uses
// it: a check run at 07:00 UTC is the same trading day as one run at 15:00 UTC+8.
var Taipei = mustLoadTaipei()

func mustLoadTaipei() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		// A machine with no tzdata still has to produce the right trading day, so the
		// fixed offset is used rather than silently falling back to UTC. Taiwan has had no
		// daylight saving since 1979, so the offset is constant.
		return time.FixedZone("CST", 8*60*60)
	}
	return loc
}

// Today is the current Taipei trading date.
func Today() string { return time.Now().In(Taipei).Format(dateLayout) }

const dateLayout = "2006-01-02"

// ParseDate validates a YYYY-MM-DD date.
func ParseDate(s string) (time.Time, error) {
	t, err := time.ParseInLocation(dateLayout, strings.TrimSpace(s), Taipei)
	if err != nil {
		return time.Time{}, fmt.Errorf("healthcheck: %q is not a YYYY-MM-DD date", s)
	}
	return t, nil
}
