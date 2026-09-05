package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
)

// Errors a caller must be able to distinguish. An unknown stock is the user's mistake (404);
// a bad date is the request's (400); anything else is ours (500).
var (
	// ErrUnknownStock — the catalog cannot resolve the symbol to one identity.
	ErrUnknownStock = errors.New("healthcheck: unknown stock")
	// ErrBadDate — asOf is malformed or in the future.
	ErrBadDate = errors.New("healthcheck: bad as-of date")
)

// Config points the service at the archives the fetch commands already write.
//
// It carries the SCANNER's config so every indicator is computed with exactly the settings
// the daily scan uses — a dashboard whose RSI period differed from the report's would be a
// second opinion wearing the same words.
type Config struct {
	Scanner scanner.Config

	FundamentalDir    string
	ValuationDir      string
	MacroDir          string
	MarketSnapshotDir string
	// NewsSnapshotDir is where news_YYYYMMDD_*.json live — the report output directory.
	NewsSnapshotDir string

	// AI configures the health judge. It holds no API key: the token is read from the
	// environment at request time. See JudgeConfig.
	AI JudgeConfig
	// History configures persistence. Disabled by default — writing into a database another
	// system reads is not something to switch on by accident.
	History HistoryConfig
}

// Defaulted fills zero values with the repo's layout, matching the Defaulted() convention
// used across this codebase.
func (c Config) Defaulted() Config {
	if c.FundamentalDir == "" {
		c.FundamentalDir = "data/fundamental"
	}
	if c.ValuationDir == "" {
		c.ValuationDir = "data/valuation"
	}
	if c.MacroDir == "" {
		c.MacroDir = "data/macro"
	}
	if c.MarketSnapshotDir == "" {
		c.MarketSnapshotDir = "data/market"
	}
	if c.NewsSnapshotDir == "" {
		c.NewsSnapshotDir = "reports"
	}
	// The judge defaults its own copy, but leaving this one blank means s.Cfg.AI.Model and
	// s.Judge.Cfg.Model disagree — harmless today, and exactly the kind of split that makes
	// a future "what is this service configured with?" endpoint report the wrong thing.
	c.AI = c.AI.Defaulted()
	return c
}

// Service answers on-demand health checks.
//
// It holds the repo's existing fetcher and scanner rather than clients of its own, so every
// indicator is computed with the settings the daily scan uses.
//
// The price CACHE is the deliberate exception. internal/fetcher writes what it fetches, so a
// dashboard sharing the scan's cache_dir would write an intraday, half-formed daily bar into
// the series the next scan reads. cmd/health-dashboard therefore keeps its own cache
// directory and refuses to start if pointed at the scanner's. Two caches, one set of
// analyzers.
type Service struct {
	Catalog *stockcatalog.Catalog
	// Prices is the price series source. It is an interface for ONE reason: a test that
	// mocked the whole evidence builder would prove nothing, while a test that supplies bars
	// and then exercises the real truncation, the real EnrichWatchlist and the real archives
	// proves everything except the HTTP call to Yahoo.
	Prices  PriceSource
	Scanner *scanner.Scanner
	Cfg     Config
	// Judge is the AI reading. Nil disables it, and a nil judge reports DISABLED rather
	// than panicking — an absent judge is a configuration state, not a failure.
	Judge *Judge
	// History persists completed checks. Nil means nothing is written, which is the default.
	History *History
	// Logf is optional progress output. It is never handed a prompt, a header or a key.
	Logf func(string, ...any)
	// Now is injectable so tests can pin "today" without touching the clock.
	Now func() time.Time
}

// PriceSource supplies one stock's full OHLCV history.
//
// The implementation used in production is FetcherPrices, wrapping the repo's existing
// fetcher pointed at the DASHBOARD's own cache directory — never the scanner's. See
// cmd/health-dashboard for the guard that enforces it.
type PriceSource interface {
	Fetch(info fetcher.StockInfo) (fetcher.StockData, error)
}

// FetcherPrices adapts the repo's fetcher to PriceSource.
type FetcherPrices struct{ F *fetcher.Fetcher }

// Fetch returns one stock's history through the existing "fetch exactly these symbols" entry
// point.
//
// That entry point WRITES what it fetches. Which cache directory the fetcher was built with
// is therefore a correctness property, not a performance one — see the package doc on
// Service and the startup guard in cmd/health-dashboard.
func (p FetcherPrices) Fetch(info fetcher.StockInfo) (fetcher.StockData, error) {
	if p.F == nil {
		return fetcher.StockData{}, errors.New("healthcheck: no fetcher configured")
	}
	stocks, err := p.F.FetchSectorStocks([]fetcher.StockInfo{info})
	if err != nil {
		return fetcher.StockData{}, err
	}
	if len(stocks) == 0 {
		return fetcher.StockData{}, fmt.Errorf("healthcheck: no price data for %s", info.Symbol)
	}
	return stocks[0], nil
}

// NewService wires a service from the existing fetcher and scanner.
func NewService(cat *stockcatalog.Catalog, f *fetcher.Fetcher, sc *scanner.Scanner,
	cfg Config, logf func(string, ...any)) *Service {
	cfg = cfg.Defaulted()
	return &Service{
		Catalog: cat, Prices: FetcherPrices{F: f}, Scanner: sc,
		Cfg: cfg, Logf: logf, Judge: NewJudge(cfg.AI),
	}
}

func (s *Service) logf(format string, args ...any) {
	if s == nil || s.Logf == nil {
		return
	}
	s.Logf(format, args...)
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Health is one stock's answer. Every field is a BLOCK carrying its own status, so a page can
// render honestly however much of the evidence arrived.
type Health struct {
	Identity    Identity  `json:"identity"`
	AsOf        string    `json:"as_of"`
	GeneratedAt time.Time `json:"generated_at"`

	Price       PriceBlock       `json:"price"`
	Market      MarketBlock      `json:"market"`
	Technical   TechnicalBlock   `json:"technical"`
	Fundamental FundamentalBlock `json:"fundamental"`
	Valuation   ValuationBlock   `json:"valuation"`
	Target      TargetPriceBlock `json:"target_price"`
	// Assessment is the deterministic "would I enter now?" verdict. It is computed from the
	// blocks above, BEFORE the judge runs, and uses its own vocabulary rather than the
	// scanner's BUY / WATCH / SELL.
	Assessment  AssessmentBlock  `json:"assessment"`
	Institution InstitutionBlock `json:"institution"`
	News        NewsBlock        `json:"news"`
	// AI is prose only, and is merged in LAST — after every number above is final.
	AI AIBlock `json:"ai"`

	DataQuality DataQuality `json:"data_quality"`

	// SnapshotID is the stock_snapshots row this check was recorded as, when history is
	// enabled. Zero means nothing was written — persistence is off, or the write failed and
	// was logged. It is the handle a later outcome is measured against.
	SnapshotID int64 `json:"snapshot_id,omitempty"`
}

// Check runs one health check.
//
// It returns an error ONLY for a request that cannot be answered at all — an unknown stock or
// an impossible date. A missing feed is never an error: the check still has whatever else
// arrived, and refusing to answer would throw that away.
func (s *Service) Check(ctx context.Context, req Request) (*Health, error) {
	if s == nil || s.Catalog == nil {
		return nil, errors.New("healthcheck: no catalog loaded")
	}
	stock, ok := s.Catalog.Resolve(req.Symbol)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownStock, req.Symbol)
	}

	asOfStr := req.AsOf
	if asOfStr == "" {
		asOfStr = s.now().In(Taipei).Format(dateLayout)
	}
	asOf, err := ParseDate(asOfStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadDate, err)
	}
	// A check dated in the future has no evidence behind it and would silently read as
	// today's — which is precisely the look-ahead every archive here is built to prevent.
	today := s.now().In(Taipei).Format(dateLayout)
	if asOf.Format(dateLayout) > today {
		return nil, fmt.Errorf("%w: %s is in the future (today is %s)",
			ErrBadDate, asOf.Format(dateLayout), today)
	}
	ev, err := s.buildEvidence(ctx, stock, asOf)
	if err != nil {
		return nil, err
	}

	h := &Health{
		Identity:    ev.Identity,
		AsOf:        ev.AsOf,
		GeneratedAt: s.now().UTC(),
		Price:       priceBlock(ev),
		Market:      marketBlock(ev),
		Technical:   technicalBlock(ev),
		Fundamental: fundamentalBlock(ev),
		Valuation:   valuationBlock(ev),
		Institution: institutionBlock(ev),
		News:        newsBlock(ev),
	}
	h.Target = buildTargetPrice(ev, h.Price)
	// After the target, because one of its CAUTION notes refers to the target's own output.
	// That does not make the target an input to the verdict: the class is decided from the
	// earnings evidence alone, and cautions are appended afterwards.
	h.Valuation.ModelSuitability = buildSuitability(ev, h)
	h.DataQuality = dataQuality(ev, h)
	// Deterministic, and computed before the model is called: the judge is TOLD the verdict
	// as evidence it may explain, and has no path by which it could produce one.
	h.Assessment = assess(h)

	// The judge runs LAST, on a Health that is already complete and already correct.
	//
	// Ordering is the guarantee, not a convention: every number the response carries exists
	// before this line, so there is no execution path on which a model's reply could become
	// one. Assess never returns an error — if the model is off, unreachable, slow or wrong,
	// the block below says so and everything above it is served unchanged.
	if !req.SkipAI {
		h.AI = s.Judge.Assess(ctx, h)
	} else {
		h.AI = AIBlock{Status: StatusDisabled, Reason: "本次請求指定跳過 AI 判讀"}
	}

	// Persistence is the LAST thing, and it cannot fail the request.
	//
	// The answer is already complete and already correct by this point. A full disk, a locked
	// database or a schema newer than this binary are all reasons to log and carry on: the
	// person waiting for this page asked a question, not for a database write, and refusing
	// to answer because the audit trail failed would be the wrong trade in both directions.
	if s.History != nil {
		if id, err := s.History.Save(ctx, h); err != nil {
			s.logf("healthcheck: history not saved for %s: %v", h.Identity.Symbol, err)
		} else {
			h.SnapshotID = id
		}
	}
	return h, nil
}

// dataQuality assembles the honesty report.
//
// It summarises EVIDENCE COMPLETENESS, never the stock: a company with perfect data and
// terrible prospects is COMPLETE. INSUFFICIENT is reserved for the one case where nothing
// could be built at all — no usable price series.
func dataQuality(ev *Evidence, h *Health) DataQuality {
	blocks := []BlockStatus{
		{Name: "price", Status: h.Price.Status, Detail: ev.PriceReason},
		{Name: "technical", Status: h.Technical.Status, Detail: h.Technical.Reason},
		{Name: "fundamental", Status: h.Fundamental.Status, Detail: h.Fundamental.Reason},
		{Name: "valuation", Status: h.Valuation.Status, Detail: h.Valuation.Reason},
		{Name: "target_price", Status: h.Target.Status, Detail: h.Target.Reason},
		{Name: "institution", Status: h.Institution.Status, Detail: h.Institution.Reason},
		{Name: "news", Status: h.News.Status, Detail: h.News.Reason},
		{Name: "market", Status: h.Market.Status, Detail: h.Market.Reason},
	}
	dq := DataQuality{
		Blocks:      blocks,
		PointInTime: ev.PIT,
		Limitations: ev.Limitations,
	}
	dq.Level = qualityLevel(blocks, h.Price.Status)
	return dq
}

// qualityLevel folds the block statuses into one word.
func qualityLevel(blocks []BlockStatus, price Status) QualityLevel {
	if price != StatusAvailable && price != StatusPartial {
		return QualityInsufficient
	}
	for _, b := range blocks {
		if b.Status != StatusAvailable {
			return QualityPartial
		}
	}
	return QualityComplete
}
