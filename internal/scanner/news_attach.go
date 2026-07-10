package scanner

import "github.com/deep-huang/stock-scanner/internal/news"

// ──────────────────────────────────────────────────────────────────────────────
// News attach (Phase 1) — SHADOW MODE, display/context only.
//
// AttachNews runs AFTER EnrichWatchlist as an independent post-pass, so the scoring
// enrichment path is never touched and it is trivially provable that news changes no
// score / action / probability / order: computeRocket has already run and is never
// re-invoked here. News is delayed external CONTEXT, never a trade instruction. This
// mirrors AttachETFFlow (etfflow_attach.go) exactly.
// ──────────────────────────────────────────────────────────────────────────────

// AttachNews stores the per-stock news view on WatchlistEntry.News for entries that
// have a resolved signal. It reads only the entry's symbol, mutates nothing else, and
// leaves entries with no news untouched (News stays nil → card renders as today).
func AttachNews(entries []WatchlistEntry, views map[string]*news.StockNewsView) {
	if len(views) == 0 {
		return
	}
	for i := range entries {
		if v, ok := views[entries[i].A.Symbol]; ok && v != nil && v.Computed {
			entries[i].News = v
		}
	}
}
