// Package news is the Phase-1 SHADOW-MODE news/sentiment layer for the scanner.
//
// It ingests external Chinese-language market-note sources, deduplicates the same
// podcast episode across sources into a single event, resolves stock/sector/theme
// references from an explicit dictionary (never fuzzy AI guessing), and produces
// display-only views + a market summary + a historical JSON snapshot.
//
// SHADOW MODE guarantees (Phase 1): this package NEVER changes RocketScore /
// WatchAction / ExplosionProb / Action / ordering. It is display / record / store /
// relate / preserve-for-backtest only. It deliberately does NOT import the scanner
// package — the attach post-pass lives in internal/scanner (mirroring etfflow), so
// the dependency only ever points scanner → news.
package news

import "time"

// SignalType is the coarse direction/intent of a news signal. Phase 1 keeps only the
// types actually produced by the classifier; more can be added when needed.
type SignalType string

const (
	SignalBullish     SignalType = "BULLISH"
	SignalBearish     SignalType = "BEARISH"
	SignalRotationIn  SignalType = "ROTATION_IN"
	SignalRotationOut SignalType = "ROTATION_OUT"
	SignalRiskWarning SignalType = "RISK_WARNING"
	SignalNeutral     SignalType = "NEUTRAL"
)

// AgeBucket is the lifecycle stage of a signal derived from PublishedAt vs now.
type AgeBucket string

const (
	AgeFresh    AgeBucket = "FRESH"    // 0–2 days
	AgeActive   AgeBucket = "ACTIVE"   // 3–5 days
	AgeDecaying AgeBucket = "DECAYING" // 6–10 days
	AgeExpired  AgeBucket = "EXPIRED"  // > max_age_days
)

// RawNewsItem is one article/episode as fetched from a single provider, before
// dedup/resolution. Content is plain text (HTML already stripped by the provider).
type RawNewsItem struct {
	Source      string    `json:"source"`       // provider name, e.g. "socialworkerdaily"
	Title       string    `json:"title"`        // e.g. "股癌筆記EP677"
	URL         string    `json:"url"`          // canonical article URL
	PublishedAt time.Time `json:"published_at"` // reliable publish time from the source
	Content     string    `json:"content"`      // plain-text body (may be empty for best-effort providers)
	EpisodeID   string    `json:"episode_id"`   // normalized episode key, e.g. "gooaye-ep-677" ("" if none)
}

// NewsSource is one piece of evidence backing a unified signal (post-dedup a single
// event can carry several sources, e.g. TWETQ + SocialWorkerDaily for the same EP).
type NewsSource struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
}

// StockReference is a mention of a specific stock. Resolved=false means the name could
// not be mapped to a code from the dictionary; it is kept as-is and never guess-bound.
type StockReference struct {
	Code     string `json:"code"`     // "" when unresolved
	Name     string `json:"name"`     // raw name as mentioned
	Resolved bool   `json:"resolved"` // true only when Code came from the explicit dictionary
}

// NewsSignal is one unified signal for one event, after dedup + resolution. Strength
// and Confidence are computed ONCE on the merged event (never summed per source), so
// multiple sources for the same episode cannot double-count.
type NewsSignal struct {
	EventID     string           `json:"event_id"`
	Sources     []NewsSource     `json:"sources"`
	PublishedAt time.Time        `json:"published_at"` // earliest source publish time
	ObservedAt  time.Time        `json:"observed_at"`  // when the scanner first ingested (look-ahead guard)
	Stocks      []StockReference `json:"stocks,omitempty"`
	Sectors     []string         `json:"sectors,omitempty"`
	Themes      []string         `json:"themes,omitempty"`
	Signal      SignalType       `json:"signal"`
	Strength    int              `json:"strength"`           // 0–10
	Confidence  int              `json:"confidence"`         // 0–10
	Conflict    bool             `json:"conflict,omitempty"` // same entity has opposing-polarity signals in this event
	Summary     string           `json:"summary,omitempty"`
	Evidence    []string         `json:"evidence,omitempty"`

	// ── minimum-viable raw preservation (MED-6) — for future historical audit ──
	// Enough to answer "why was this judged BULLISH?" without re-fetching the site.
	RawTitle       string `json:"raw_title,omitempty"`       // source article/episode title
	ContentExcerpt string `json:"content_excerpt,omitempty"` // ~240-rune excerpt of the classified source text
}

// StockNewsView is the per-stock display object attached to a watchlist entry
// (WatchlistEntry.News). It is purely presentational; nothing here feeds scoring.
type StockNewsView struct {
	Computed    bool         `json:"computed"`               // false → insufficient data (card renders as today)
	StockItems  []NewsSignal `json:"stock_items,omitempty"`  // signals naming this exact code
	SectorItems []NewsSignal `json:"sector_items,omitempty"` // sector/theme signals touching this code
	Divergence  bool         `json:"divergence"`             // stock-level vs sector-level direction conflict
}

// SectorWindowCell is one sector's news lean within one day-window. Counts fold the 6
// SignalTypes into three polarity buckets (Pos = BULLISH+ROTATION_IN 🟢, Risk =
// RISK_WARNING 🟡, Neg = BEARISH+ROTATION_OUT 🔴). Display is the pre-rendered compact
// cell (net for short windows, tally for the widest).
type SectorWindowCell struct {
	Days    int    `json:"days"`
	Pos     int    `json:"pos"`
	Risk    int    `json:"risk"`
	Neg     int    `json:"neg"`
	Net     string `json:"net"`     // 偏多 / 風險 / 偏空 / 偏多轉風險 / 分歧 / "" (empty window)
	Display string `json:"display"` // compact rendered cell (e.g. "🟢偏多" or "🟢5 🟡1 🔴3")
}

// SectorNewsRow is one sector across all configured windows (方案 A + multi-window). A
// sector with mixed views is shown ONCE, letting you read its evolution 1日 → 3日 → 7日
// and spot fast rotation (e.g. 7日 偏多 but 近1日 轉空).
type SectorNewsRow struct {
	Name       string             `json:"name"`
	Cells      []SectorWindowCell `json:"cells"`               // one per MarketNewsSummary.Windows, same order
	LatestType SignalType         `json:"latest_type"`         // most-recent signal's type (widest window)
	LatestEP   string             `json:"latest_ep,omitempty"` // e.g. "EP677"
}

// MarketNewsSummary is the market-wide banner object: one row per sector (sorted by the
// widest window's net lean) shown across Windows day-windows, plus age counts. Nothing
// here feeds scoring.
type MarketNewsSummary struct {
	Windows []int           `json:"windows,omitempty"` // day-windows, ascending (e.g. [1,3,7])
	Sectors []SectorNewsRow `json:"sectors,omitempty"`
	Fresh   int             `json:"fresh"`
	Active  int             `json:"active"`
	Expired int             `json:"expired"`
	AsOf    time.Time       `json:"as_of"`
}
