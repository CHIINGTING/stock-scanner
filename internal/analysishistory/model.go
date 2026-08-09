// Package analysishistory defines the single authoritative structured history schema
// (the "sidecar" JSON) plus its writer. Both the scanner (writer) and the validator
// (reader, internal/validator/sidecar.go) use THESE types — the JSON format has exactly
// one owner, so a report-template redesign can never break structured historical
// analysis. See docs/SPEC_R10_1_INSTITUTION_BIAS_SHADOW.md §17.
//
// Files live at data/analysis_history/<YYYY-MM-DD>.json (under data/, not reports/, so
// reports/ stays presentation-only and outside the validator's report_*.html regex).
package analysishistory

// SchemaVersion is the sidecar schema version (additive evolution; §20).
const SchemaVersion = 1

// Snapshot is one scan date's structured history (all stocks shown that day).
type Snapshot struct {
	SchemaVersion int         `json:"schema_version"`
	Date          string      `json:"date"` // YYYY-MM-DD
	Stocks        []StockData `json:"stocks"`
}

// StockData is one stock's structured record for one scan date. Institution / Bias are
// nil when their feature was off or produced no data (absent ≠ zero).
type StockData struct {
	Code        string              `json:"code"`
	Action      string              `json:"action"`
	Score       float64             `json:"score"`
	Institution *InstitutionPayload `json:"institution,omitempty"`
	Bias        *BiasPayload        `json:"bias,omitempty"`
}

// InstitutionPayload holds the four display categories for one scan date.
type InstitutionPayload struct {
	Foreign         LegPayload `json:"foreign"`
	InvestmentTrust LegPayload `json:"investmentTrust"`
	Dealer          LegPayload `json:"dealer"`
	Total           LegPayload `json:"total"`
}

// LegPayload is one institution category's structured figures (§17). NetBuyRatio is a
// pointer so "unavailable" (nil) is distinct from a real 0.
type LegPayload struct {
	Today               int64    `json:"today"`
	Sum3d               int64    `json:"sum3d"`
	Sum5d               int64    `json:"sum5d"`
	Sum10d              int64    `json:"sum10d"`
	Sum20d              int64    `json:"sum20d"`
	ConsecutiveBuyDays  int      `json:"consecutiveBuyDays"`
	ConsecutiveSellDays int      `json:"consecutiveSellDays"`
	Transition          string   `json:"transition"`
	NetBuyRatio         *float64 `json:"netBuyRatio"`
	StreakComplete      bool     `json:"streakComplete"`
	DataComplete        bool     `json:"dataComplete"`
}

// BiasPayload holds the four BIAS windows + the risk label. Each window is a pointer so
// "insufficient history" (nil) is distinct from a real 0.0 (§13).
type BiasPayload struct {
	Bias5  *float64 `json:"bias5"`
	Bias10 *float64 `json:"bias10"`
	Bias20 *float64 `json:"bias20"`
	Bias60 *float64 `json:"bias60"`
	Risk   string   `json:"risk"`
}
