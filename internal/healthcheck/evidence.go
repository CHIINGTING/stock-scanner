package healthcheck

import (
	"context"
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/fundamental"
	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/macro"
	marketservice "github.com/deep-huang/stock-scanner/internal/market/service"
	"github.com/deep-huang/stock-scanner/internal/news"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// The evidence builder: gather what is already known about one stock, as of one date.
//
// # Everything here is a READ
//
// It calls the scanner's own EnrichWatchlist and the existing Attach passes, and it loads the
// archives the fetch commands already wrote. It computes no indicator of its own — a second
// implementation of MA20 or RSI would agree today, drift in six months, and leave two equally
// authoritative answers on one page.
//
// # Why this is not candidate.Resolver
//
// internal/candidate does very nearly this, and the temptation to call it was real. It cannot
// be used, for one reason that matters and one that follows from it:
//
//	it does not truncate the price series   a research run reads whatever the cache holds,
//	                                        which for a historical asOf is the future
//	it is a batch over a human-ordered list  a health check is one symbol, and the order
//	                                        machinery is dead weight
//
// The first is disqualifying: §9 of the R14 spec makes point-in-time a hard requirement, and
// handing today's chart to a check dated three months ago is exactly the look-ahead the rest
// of this repo spends so much effort avoiding. So the orchestration is written here, while
// every CALCULATION still goes through the scanner's own functions.
//
// # Point-in-time, source by source
//
//	price / technical   TRUNCATED — the full cached series is read, then cut at asOf here
//	fundamental         AS_OF — LoadAsOf plus the record's own PublishedAt gate
//	valuation           AS_OF — archives at or before asOf, deduplicated by session
//	institution         AS_OF — LoadHistory is capped at the end date
//	macro               AS_OF — LoadAsOf
//	market regime       EXACT_DATE — one file per date, no as-of fallback
//	news                EXACT_DATE — snapshots are keyed on the observation date
//
// Every one of those is reported on the response, so "is this page historically honest" is
// answerable from the page rather than from this comment.

// MarketEvidence is the market-wide regime read for the date.
type MarketEvidence struct {
	Status Status
	Regime string
	// Score is nil when unavailable: a market with no snapshot has no score, and 0 is a
	// legitimate reading of a real one.
	Score  *float64
	AsOf   string
	Reason string
}

// Evidence is everything deterministically known about one stock at one moment.
//
// Entry is the SCANNER's WatchlistEntry, referenced rather than re-derived. Every indicator,
// stage and score on it is the scanner's own, so "dashboard technical == scanner technical"
// is true by construction rather than by a mapping that could be written wrong.
type Evidence struct {
	Identity Identity
	AsOf     string
	AsOfTime time.Time

	// Entry is nil when the price series was missing or too short to analyse.
	Entry *scanner.WatchlistEntry
	// Bars is the TRUNCATED series that was actually analysed. Its length is the honest
	// answer to "how much history did this reading have".
	Bars        []fetcher.Candle
	PriceStatus Status
	PriceReason string
	// PriceSource names where the series came from, for traceability.
	PriceSource string

	Fundamental       *fundamental.View
	FundamentalStatus Status
	FundamentalReason string

	Valuation       *valuation.Valuation
	ValuationStatus Status
	ValuationReason string

	Institution       *institution.StockChipView
	InstitutionStatus Status
	InstitutionReason string

	News       *news.StockNewsView
	NewsStatus Status
	NewsReason string

	Market MarketEvidence
	Macro  *macro.Research

	// PIT and Limitations accumulate as sources are consulted.
	PIT         []SourcePIT
	Limitations []Limitation
}

func (e *Evidence) addPIT(source string, mode PITMode, detail string) {
	e.PIT = append(e.PIT, SourcePIT{Source: source, Mode: mode, Detail: detail})
}

func (e *Evidence) addLimitation(area, detail string) {
	e.Limitations = append(e.Limitations, Limitation{Area: area, Detail: detail})
}

// buildEvidence assembles everything for one stock as of one date.
//
// It never returns an error for a missing source. A health check whose institutional feed is
// absent still has fundamentals, valuation and a chart, and refusing to answer at all would
// throw away everything that DID arrive. Each block carries its own status instead.
func (s *Service) buildEvidence(ctx context.Context, stock stockcatalog.Stock, asOf time.Time) (*Evidence, error) {
	asOfDate := asOf.Format(dateLayout)
	ev := &Evidence{
		Identity: identityOf(stock),
		AsOf:     asOfDate,
		AsOfTime: asOf,
	}

	// The stages run in sequence with the context re-checked between each one.
	//
	// This is a cancellation BOUNDARY rather than cancellation inside the work: attachPrice
	// can make a network call and the rest read the archive tree, and none of those take a
	// ctx today. What the boundary buys is that a client that has already hung up or timed
	// out stops paying for the stages it has not reached — so the worst case is bounded by
	// the single longest stage, the price fetch, under internal/fetcher's own HTTP timeout,
	// instead of by the sum of all seven.
	//
	// A cancelled build returns an error rather than a half-filled Evidence. Every unreached
	// block would otherwise render UNAVAILABLE, and "the source had nothing" and "we ran out
	// of time before asking" are not the same statement to put in front of someone deciding
	// whether to buy.
	stages := []struct {
		name string
		run  func()
	}{
		{"price", func() { s.attachPrice(ev, stock, asOf) }},
		{"institution", func() { s.attachInstitution(ev, asOfDate) }},
		{"news", func() { s.attachNews(ev, stock, asOf) }},
		{"fundamental", func() { s.attachFundamental(ev, asOfDate) }},
		{"valuation", func() { s.attachValuation(ev, asOfDate) }},
		{"macro", func() { s.attachMacro(ev, asOfDate) }},
		{"market", func() { s.attachMarket(ev, asOfDate) }},
	}
	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("healthcheck: aborted before the %s stage: %w", stage.name, err)
		}
		stage.run()
	}
	return ev, nil
}

// identityOf projects a catalog entry onto the identity that travels with every answer.
func identityOf(stock stockcatalog.Stock) Identity {
	id := Identity{
		Symbol:       stock.Symbol(),
		Name:         stock.Name,
		Market:       stock.MarketLabel(),
		MarketCode:   stock.Market,
		MarketSource: string(stock.MarketSource),
		Industry:     stock.Industry,
		Aliases:      stock.Aliases,
		Tags:         stock.Tags,
	}
	if sym, ok := stock.CanonicalSymbol(); ok {
		id.CanonicalSymbol = sym
	}
	return id
}

// ── price and technicals ──────────────────────────────────────────────────────────────

// attachPrice fetches the series, cuts it at asOf, and runs the scanner's own enrichment.
func (s *Service) attachPrice(ev *Evidence, stock stockcatalog.Stock, asOf time.Time) {
	ev.PriceStatus = StatusUnavailable
	ev.PriceSource = "Yahoo Finance via internal/fetcher (.cache)"
	ev.addPIT("price / technical", PITTruncated,
		"完整快取序列讀入後，於此層截斷至 asOf；截斷前的原始序列可能延伸到今天")

	if s.Prices == nil {
		ev.PriceReason = "no price source configured"
		return
	}

	sd, err := s.Prices.Fetch(fetcher.StockInfo{
		Symbol: stock.Code, Name: stock.Name, Market: stock.Market,
	})
	if err != nil {
		ev.PriceReason = err.Error()
		return
	}

	// The fetcher resolves .TW / .TWO by trying both. When the catalog did not know the
	// market, this is the first moment it becomes a fact — recorded as RESOLVED so a reader
	// can tell a discovered market from a configured one.
	if ev.Identity.MarketCode == "" && sd.Market != "" {
		ev.Identity.MarketCode = sd.Market
		ev.Identity.Market = stockcatalog.MarketLabel(sd.Market)
		ev.Identity.MarketSource = MarketSourceResolved
		ev.Identity.CanonicalSymbol = fetcher.YahooSymbol(stock.Code, sd.Market)
	}

	bars := truncateCandles(sd.Candles, asOf)
	ev.Bars = bars
	switch {
	case len(sd.Candles) == 0:
		ev.PriceReason = "the price source returned no bars"
		return
	case len(bars) == 0:
		ev.PriceStatus = StatusUnavailable
		ev.PriceReason = fmt.Sprintf("no trading bar on or before %s (the cached series starts %s)",
			asOf.Format(dateLayout), sd.Candles[0].Date.Format(dateLayout))
		return
	case len(bars) < minBarsForAnalysis:
		// The scanner's analyzers need 30 bars. Saying so beats an empty technical panel with
		// no explanation, and the last close is still a real, usable fact.
		ev.PriceStatus = StatusPartial
		ev.PriceReason = fmt.Sprintf("only %d bars on or before %s; the analyzers need %d",
			len(bars), asOf.Format(dateLayout), minBarsForAnalysis)
		return
	}

	// The scanner's OWN enrichment, with the scanner's OWN config, on the truncated series.
	// sectorOf / rot / members / rsTable are nil: a single-stock query cannot produce a
	// meaningful sector rotation panel, and deriving one from one stock would be a second,
	// weaker answer to a question the daily scan already answers properly.
	item := sd
	item.Candles = bars
	entries := s.Scanner.EnrichWatchlist([]fetcher.StockData{item}, nil, nil, nil, nil)
	if len(entries) == 0 {
		ev.PriceStatus = StatusPartial
		ev.PriceReason = "the scanner produced no entry for this series"
		return
	}
	ev.Entry = &entries[0]
	ev.PriceStatus = StatusAvailable
}

// minBarsForAnalysis mirrors the threshold inside scanner.EnrichWatchlist. It is duplicated
// as a constant only to produce a better message; the scanner still decides.
const minBarsForAnalysis = 30

// truncateCandles keeps the bars dated on or before asOf.
//
// This is where price-side point-in-time safety lives. The cache holds two years ending
// today, so without this a check dated 2026-05-01 would be handed every bar up to now and
// would compute an MA20 out of the future.
//
// Comparison is on the FORMATTED date rather than on time.Time: bars carry a source-supplied
// timestamp whose location and time-of-day are not guaranteed, and "same calendar day"
// is the question being asked.
func truncateCandles(cs []fetcher.Candle, asOf time.Time) []fetcher.Candle {
	cut := asOf.Format(dateLayout)
	end := len(cs)
	for end > 0 && cs[end-1].Date.Format(dateLayout) > cut {
		end--
	}
	if end == len(cs) {
		return cs
	}
	// A fresh slice, so a later append by a caller cannot write into the cached series.
	out := make([]fetcher.Candle, end)
	copy(out, cs[:end])
	return out
}

// ── institutional chips ───────────────────────────────────────────────────────────────

// attachInstitution runs the existing institutional pass over this stock's entry.
//
// It reads the archive directly rather than consulting enable_institution. That flag governs
// what the SCAN does; this is an on-demand sidecar that cannot affect the scan either way,
// and gating a read-only dashboard behind the scan's switch would only produce a blank panel
// nobody could explain. The same choice cmd/candidate-research already made.
func (s *Service) attachInstitution(ev *Evidence, asOfDate string) {
	ev.InstitutionStatus = StatusUnavailable
	cfg := s.Cfg.Scanner.Institution.Defaulted()
	ev.addPIT("institution", PITAsOf, "LoadHistory 以 asOf 為 end date 上限，不會讀到之後的快照")

	if ev.Entry == nil {
		ev.InstitutionReason = "no scanner entry to attach chips to"
		return
	}
	loaded, err := institution.LoadHistory(cfg.SnapshotDir, asOfDate, cfg.HistoryDays)
	if err != nil || loaded == nil {
		ev.InstitutionReason = fmt.Sprintf("institution snapshots unavailable under %s", cfg.SnapshotDir)
		return
	}
	// LoadHistory returns an EMPTY Loaded — not an error — when the directory is missing or
	// holds nothing. Reporting that as AVAILABLE would make every stock read "available, no
	// institutional activity" when in fact nothing was ever loaded.
	if len(loaded.Dates()) == 0 {
		ev.InstitutionReason = fmt.Sprintf("no institution snapshot on or before %s under %s",
			asOfDate, cfg.SnapshotDir)
		return
	}

	entries := []scanner.WatchlistEntry{*ev.Entry}
	candles := map[string][]fetcher.Candle{ev.Identity.Symbol: ev.Bars}
	scanner.AttachInstitution(entries, loaded, candles, ev.AsOfTime, cfg)
	if entries[0].Institution == nil {
		// The layer ran and had no row for THIS stock — different from the layer being off,
		// and different from a zero net.
		ev.InstitutionReason = "the snapshots hold no record for this stock"
		return
	}
	ev.Entry.Institution = entries[0].Institution
	ev.Institution = entries[0].Institution
	ev.InstitutionStatus = StatusAvailable
}

// ── news ──────────────────────────────────────────────────────────────────────────────

// attachNews reads the stored news snapshots for the date.
//
// It NEVER fetches. A live fetch during a historical check returns today's headlines and
// would file them under a past date — the look-ahead the stored snapshots exist to prevent —
// and the scanner already refuses to do it for exactly this reason.
func (s *Service) attachNews(ev *Evidence, stock stockcatalog.Stock, asOf time.Time) {
	ev.NewsStatus = StatusUnavailable
	dir := s.Cfg.NewsSnapshotDir
	ev.addPIT("news", PITExactDate,
		"新聞快照以觀察日期為檔名，沒有 as-of 回溯語意：該日沒有快照就是沒有，不會退回前一日")

	records, err := news.LoadSnapshotsForDate(dir, asOf)
	if err != nil {
		ev.NewsReason = fmt.Sprintf("news snapshots under %s are unreadable: %v", dir, err)
		return
	}
	if len(records) == 0 {
		ev.NewsReason = fmt.Sprintf("no news snapshot recorded for %s", asOf.Format(dateLayout))
		return
	}

	// The LATEST run of the day. Earlier runs are a subset of it by construction (each run
	// re-reads the same feeds), and merging them would double-count every signal.
	latest := records[len(records)-1]

	// A minimal resolve index: news.BuildViews needs sector membership to attach sector-level
	// signals, and the catalog is where this stock's sectors live.
	idx := news.NewResolveIndex()
	idx.AddStock(stock.Code, stock.Name)
	for _, sec := range stock.Sectors {
		idx.AddStockSector(stock.Code, sec)
	}
	views := news.BuildViews(latest.Signals, idx)
	v, ok := views[stock.Code]
	if !ok || v == nil {
		ev.NewsStatus = StatusAvailable
		ev.NewsReason = "news was collected for this date but nothing named this stock"
		return
	}
	// Computed=false means BuildViews could not finish the view. scanner.AttachNews already
	// drops such a view rather than attaching it, and this gate is that same rule stated
	// once here — the alternative, assigning ev.Entry.News by hand, silently disagreed with
	// the scanner about what counts as a usable news read.
	if !v.Computed {
		ev.NewsStatus = StatusInsufficientData
		ev.NewsReason = "訊號存在，但這檔的 news view 未完成計算"
		return
	}
	ev.News = v
	if ev.Entry != nil {
		// AttachNews writes into the ELEMENTS of the slice it is handed, so a literal built
		// at the call site is written to and thrown away. The result has to be read back out
		// of the slice that was passed in.
		entries := []scanner.WatchlistEntry{*ev.Entry}
		scanner.AttachNews(entries, views)
		ev.Entry.News = entries[0].News
	}
	ev.NewsStatus = StatusAvailable
}

// ── archives ──────────────────────────────────────────────────────────────────────────

// attachFundamental reads the archived MOPS view.
//
// Read-only: cmd/fundamental-fetch produces the archive. A live fetch during a historical
// check would return today's figures and label them with a past date.
func (s *Service) attachFundamental(ev *Evidence, asOfDate string) {
	ev.FundamentalStatus = StatusUnavailable
	ev.addPIT("fundamental", PITAsOf,
		"LoadAsOf 取 asOf 當日或之前最近的封存，且每筆記錄再以其 PublishedAt 過濾")

	sym := ev.Identity.CanonicalSymbol
	if sym == "" {
		ev.FundamentalReason = "the market is unknown, so there is no archive key to look under"
		ev.addLimitation("fundamental", "市場未知（catalog 沒記、價格來源也沒解出），"+
			"財報封存以 <代號>_<市場>.json 為檔名，無法查詢")
		return
	}
	svc := fundamental.NewService(nil, s.Cfg.FundamentalDir, s.logf)
	v, err := svc.LoadView(asOfDate, sym)
	if err != nil {
		ev.FundamentalReason = err.Error()
		return
	}
	vc := v
	ev.Fundamental = &vc
	ev.FundamentalStatus = mapAvailability(string(v.Status))
	ev.FundamentalReason = v.Reason
}

// attachValuation reads the archived exchange-published price ratios.
func (s *Service) attachValuation(ev *Evidence, asOfDate string) {
	ev.ValuationStatus = StatusUnavailable
	ev.addPIT("valuation", PITAsOf,
		"只讀 asOf 當日或之前的封存目錄；同一交易 session 重複封存時取最新一份")

	sym := ev.Identity.CanonicalSymbol
	if sym == "" {
		ev.ValuationReason = "the market is unknown, so there is no archive key to look under"
		return
	}
	svc := valuation.NewService(nil, s.Cfg.ValuationDir, s.logf)
	v, err := svc.LoadValuation(asOfDate, sym)
	if err != nil {
		ev.ValuationReason = err.Error()
		return
	}
	vc := v
	ev.Valuation = &vc
	ev.ValuationStatus = mapAvailability(string(v.Status))
	ev.ValuationReason = v.Reason
}

// attachMacro loads the market-wide monetary / rate / inflation / labour / volatility view.
func (s *Service) attachMacro(ev *Evidence, asOfDate string) {
	ev.addPIT("macro", PITAsOf, "LoadAsOf 取 asOf 當日或之前最近的封存")
	snap, err := macro.LoadAsOf(s.Cfg.MacroDir, asOfDate)
	if err != nil {
		s.logf("healthcheck: macro: %v", err)
	}
	// Interpret handles a nil snapshot by reporting everything unavailable, which is the
	// honest answer and keeps this call unconditional.
	res := macro.Interpret(snap, asOfDate)
	ev.Macro = &res
}

// attachMarket reads the regime the market dashboard already computed for this date.
//
// A missing snapshot is UNAVAILABLE with no regime and no score — never a neutral default.
// "We could not look" and "the market is unremarkable" are different facts.
func (s *Service) attachMarket(ev *Evidence, asOfDate string) {
	ev.addPIT("market regime", PITExactDate,
		"市場快照一天一檔，沒有 as-of 回溯：該日沒跑 market-fetch 就是沒有")
	snap, err := marketservice.LoadSnapshot(s.Cfg.MarketSnapshotDir, asOfDate)
	if err != nil || snap == nil {
		ev.Market = MarketEvidence{
			Status: StatusUnavailable,
			Reason: fmt.Sprintf("no market snapshot for %s under %s", asOfDate, s.Cfg.MarketSnapshotDir),
		}
		return
	}
	score := snap.Score
	ev.Market = MarketEvidence{
		Status: StatusAvailable,
		Regime: string(snap.Regime),
		Score:  &score,
		AsOf:   snap.Date,
	}
}

// mapAvailability translates the fundamental / valuation packages' vocabulary into this one.
//
// They already agree on the words; the conversion exists so an unrecognised value becomes
// UNAVAILABLE instead of an undefined status leaking onto the page.
func mapAvailability(s string) Status {
	switch Status(s) {
	case StatusAvailable, StatusPartial, StatusInsufficientData, StatusUnavailable,
		StatusDisabled, StatusNotApplicable, StatusNotImplemented:
		return Status(s)
	default:
		return StatusUnavailable
	}
}
