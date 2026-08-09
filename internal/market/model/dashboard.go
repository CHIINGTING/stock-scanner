package model

import "time"

// SectorRank is one row of the sector ranking (★1–5). Score is the blended cycle+news
// standing; Reason is a short generated explanation.
type SectorRank struct {
	Name   string  `json:"name"`
	Stars  int     `json:"stars"` // 1..5
	Score  float64 `json:"score"` // 0..100 blended
	Reason string  `json:"reason"`
}

// Opportunity / Risk are the highlighted sectors at the top and bottom of the ranking,
// with the reasons that put them there (price / news / cycle).
type Opportunity struct {
	Sector  string   `json:"sector"`
	Reasons []string `json:"reasons"`
}

type Risk struct {
	Sector  string   `json:"sector"`
	Reasons []string `json:"reasons"`
}

// Dashboard is the complete Market Dashboard for one day — the top-level artifact that is
// rendered to text/JSON and (in later phases) to the report's first page. It is persisted
// verbatim to data/market/YYYY-MM-DD.json for charting, backtesting, and validation.
type Dashboard struct {
	Date             string        `json:"date"`
	GeneratedAt      time.Time     `json:"generated_at"`
	MarketScore      MarketScore   `json:"market_score"`
	Regime           RegimeInfo    `json:"regime"`
	Sectors          []SectorRank  `json:"sectors"`
	TopOpportunities []Opportunity `json:"top_opportunities"`
	TopRisks         []Risk        `json:"top_risks"`
}
