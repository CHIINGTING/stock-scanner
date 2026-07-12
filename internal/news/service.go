package news

import (
	"context"
	"log"
	"time"
)

// Result is the full output of a news collection pass.
type Result struct {
	Signals []NewsSignal              // all unified signals (also persisted to snapshot)
	Views   map[string]*StockNewsView // per-stock-code display views (for AttachNews)
	Summary *MarketNewsSummary        // market banner
}

// Service orchestrates providers → dedup → classify → views/summary → snapshot. It NEVER
// touches any scanner scoring; it only produces display context + a historical snapshot.
type Service struct {
	providers []NewsProvider
	index     *ResolveIndex
	cfg       NewsConfig
	logf      func(format string, args ...any)
}

// NewService builds a Service. A nil logger defaults to the standard logger.
func NewService(providers []NewsProvider, index *ResolveIndex, cfg NewsConfig, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = log.Printf
	}
	return &Service{providers: providers, index: index, cfg: cfg.Defaulted(), logf: logf}
}

// Collect runs the full pass. observedAt MUST be the real wall-clock fetch time (never a
// historical analysis date) — every signal's ObservedAt and the snapshot filename derive
// from it, which is the look-ahead guard. Provider failures are ISOLATED: a failing source
// is logged and skipped, the rest still produce signals. Collect never returns an error
// that would fail the scan — the scanner always completes (news is optional enrichment).
func (s *Service) Collect(ctx context.Context, observedAt time.Time) *Result {
	var raw []RawNewsItem
	for _, p := range s.providers {
		items, err := p.Fetch(ctx)
		if err != nil {
			s.logf("news provider=%s error=%q → skipped", p.Name(), err.Error())
			continue
		}
		s.logf("news provider=%s fetched=%d", p.Name(), len(items))
		raw = append(raw, items...)
	}

	events := Dedup(raw)
	s.logf("news dedup raw=%d unique_events=%d", len(raw), len(events))

	var signals []NewsSignal
	for _, ev := range events {
		signals = append(signals, BuildSignals(ev, s.index, observedAt)...)
	}

	views := BuildViews(signals, s.index)
	summary := BuildSummary(signals, observedAt, s.cfg)
	s.logf("news signals total=%d stocks=%d sectors=%d (fresh=%d active=%d expired=%d)",
		len(signals), len(views), len(summary.Sectors), summary.Fresh, summary.Active, summary.Expired)

	if dir := s.cfg.SnapshotDir; dir != "" {
		if path, err := SaveSnapshot(dir, observedAt, signals); err != nil {
			s.logf("news snapshot error=%q", err.Error())
		} else {
			s.logf("news snapshot file=%s signals=%d", path, len(signals))
		}
	}

	return &Result{Signals: signals, Views: views, Summary: summary}
}

// SameDay reports whether two times fall on the same calendar day (used to gate live
// news fetch: only fetch when the analysis date is today, else look-ahead is possible).
func SameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
