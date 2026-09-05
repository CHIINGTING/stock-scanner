package valuation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// Acquisition and archival, following R13-M5's shape exactly: one bulk request per market,
// a dated directory per observation, and a point-in-time read that never fetches.

const (
	twsePE         = "https://openapi.twse.com.tw/v1/exchangeReport/BWIBBU_ALL"
	tpexPE         = "https://www.tpex.org.tw/openapi/v1/tpex_mainboard_peratio_analysis"
	defaultDir     = "data/valuation"
	defaultTimeout = 45 * time.Second
	maxBody        = 32 << 20
)

// DefaultDir is the archive root.
func DefaultDir() string { return defaultDir }

var endpointOverride map[string]string

// Provider fetches the exchanges' published ratios.
type Provider struct {
	HTTP *http.Client
	Logf func(string, ...any)
}

// NewProvider builds a provider with a bounded client.
func NewProvider(logf func(string, ...any)) *Provider {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Provider{HTTP: &http.Client{Timeout: defaultTimeout}, Logf: logf}
}

// FetchMarket downloads one market's ratios, indexed by stock code.
//
// One request covers every listed company, so a hundred candidates cost two requests — not
// two hundred.
func (p *Provider) FetchMarket(ctx context.Context, market string) (map[string]Ratios, error) {
	var url string
	switch market {
	case fetcher.MarketTWSE:
		url = twsePE
	case fetcher.MarketTPEX:
		url = tpexPE
	default:
		return nil, fmt.Errorf("valuation: market %q is not supported", market)
	}
	if o, ok := endpointOverride[url]; ok {
		url = o
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "stock-scanner/valuation")

	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("valuation: fetch %s: %w", market, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Never an empty dataset: "rate limited" and "no company has a P/E" are different.
		return nil, fmt.Errorf("valuation: fetch %s: HTTP %d", market, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	var rows []map[string]string
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("valuation: parse %s: %w", market, err)
	}
	return parseRatioRows(rows), nil
}

// parseRatioRows reads either exchange's layout.
//
// TWSE and TPEx publish the same data under different key names, so the parser accepts both
// rather than existing twice.
func parseRatioRows(rows []map[string]string) map[string]Ratios {
	out := make(map[string]Ratios, len(rows))
	for _, r := range rows {
		code := first(r, "Code", "SecuritiesCompanyCode")
		if code == "" {
			continue
		}
		date := normaliseDate(first(r, "Date"))
		if date == "" {
			continue
		}
		out[code] = Ratios{
			Symbol:        code,
			Date:          date,
			PERatio:       num(first(r, "PEratio", "PriceEarningRatio")),
			PBRatio:       num(first(r, "PBratio", "PriceBookRatio")),
			DividendYield: num(first(r, "DividendYield", "YieldRatio")),
		}
	}
	return out
}

func first(row map[string]string, names ...string) string {
	for _, n := range names {
		if v, ok := row[n]; ok {
			return v
		}
	}
	return ""
}

// num parses a ratio cell. A blank or "-" is MISSING, not zero — the exchanges omit the P/E
// for a company with non-positive earnings, and reading that as 0 would place a loss-making
// stock at the cheap end of every distribution.
func num(s string) *float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	switch s {
	case "", "-", "--", "N/A":
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

// normaliseDate turns the exchanges' date formats into YYYY-MM-DD.
//
// TWSE sends a ROC date ("1150828"); TPEx sends the same. ROC year + 1911 = Gregorian —
// reading 115 as 115 AD would place the observation nineteen centuries early, and every
// point-in-time window would then include everything.
func normaliseDate(s string) string {
	s = strings.TrimSpace(s)
	switch len(s) {
	case 7: // ROC YYYMMDD
		y, err := strconv.Atoi(s[:3])
		if err != nil {
			return ""
		}
		return fmt.Sprintf("%04d-%s-%s", y+1911, s[3:5], s[5:7])
	case 8: // Gregorian YYYYMMDD
		return fmt.Sprintf("%s-%s-%s", s[:4], s[4:6], s[6:8])
	case 10: // already YYYY-MM-DD
		return s
	}
	return ""
}

// ── archive ───────────────────────────────────────────────────────────────────────────

// SnapshotPath is <dir>/<observed date>/<code>_<market>.json.
func SnapshotPath(dir, observedDate, code, market string) string {
	if dir == "" {
		dir = defaultDir
	}
	return filepath.Join(dir, observedDate, code+"_"+market+".json")
}

// SaveSnapshot writes one stock's ratios atomically.
func SaveSnapshot(dir string, s *Snapshot) (string, error) {
	if s == nil || s.Symbol == "" {
		return "", fmt.Errorf("valuation: refusing to save a snapshot with no symbol")
	}
	code, market, err := fetcher.ParseYahooSymbol(s.Symbol)
	if err != nil {
		return "", err
	}
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SchemaVersion
	}
	path := SnapshotPath(dir, s.ObservedAt.Format("2006-01-02"), code, market)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// LoadHistory reads every archived observation for one stock, on or before asOfDate.
//
// This is where the point-in-time guarantee lives for valuation: a run dated in the past
// reads only directories dated at or before it, so a percentile computed for 2026-05-01 is
// built from what had been observed by 2026-05-01. It never fetches — a live call during a
// historical run would return today's ratio and file it under a past date.
//
// Returned oldest-first, a fixed order so nothing downstream depends on directory listing.
func LoadHistory(dir, asOfDate, symbol string) ([]Ratios, *Ratios, error) {
	if dir == "" {
		dir = defaultDir
	}
	code, market, err := fetcher.ParseYahooSymbol(symbol)
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var dates []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := time.Parse("2006-01-02", e.Name()); err != nil {
			continue
		}
		if asOfDate == "" || e.Name() <= asOfDate {
			dates = append(dates, e.Name())
		}
	}
	sort.Strings(dates)

	var history []Ratios
	var latest *Ratios
	for _, d := range dates {
		b, err := os.ReadFile(SnapshotPath(dir, d, code, market))
		if err != nil {
			continue // this date has no file for this stock
		}
		var s Snapshot
		if err := json.Unmarshal(b, &s); err != nil {
			// A corrupt file is skipped with a note rather than crashing the run — but it is
			// never turned into a zero-valued observation.
			return history, latest, fmt.Errorf("valuation: %s snapshot for %s is unreadable: %w",
				d, symbol, err)
		}
		// Backfilled sessions stored in this same file. They join the DISTRIBUTION but never
		// become `latest`: the trailing P/E must stay the live current reading, and a
		// year-old session is not that however recently it was retrieved.
		for _, h := range s.History {
			hr := h
			hr.ArchiveDate = d
			if hr.Source == "" {
				hr.Source = s.Source
			}
			history = append(history, hr)
		}
		if s.Ratios == nil {
			continue
		}
		r := *s.Ratios
		// Stamp WHERE this copy came from. The exchanges publish with a lag, so the same
		// session appears in several archives; the calculator needs the archive date to keep
		// exactly one of them — the most recent the caller may see.
		r.ArchiveDate = d
		if r.Source == "" {
			r.Source = s.Source
		}
		history = append(history, r)
		rc := r
		latest = &rc
	}
	return history, latest, nil
}

// Service ties the provider to the archive.
type Service struct {
	Provider *Provider
	Dir      string
	Logf     func(string, ...any)
}

// NewService wires a provider to an archive directory.
func NewService(p *Provider, dir string, logf func(string, ...any)) *Service {
	if p == nil {
		p = NewProvider(logf)
	}
	if dir == "" {
		dir = defaultDir
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{Provider: p, Dir: dir, Logf: logf}
}

// FetchAndStore downloads each needed market once and archives one snapshot per symbol.
func (s *Service) FetchAndStore(ctx context.Context, symbols []string) ([]Snapshot, error) {
	markets := map[string]bool{}
	for _, sym := range symbols {
		if _, m, err := fetcher.ParseYahooSymbol(sym); err == nil {
			markets[m] = true
		}
	}
	data := map[string]map[string]Ratios{}
	for m := range markets {
		got, err := s.Provider.FetchMarket(ctx, m)
		if err != nil {
			s.Logf("valuation: %s ratios unavailable: %v", m, err)
			continue
		}
		data[m] = got
		s.Logf("valuation: %s — %d ratio rows", m, len(got))
	}

	now := time.Now()
	out := make([]Snapshot, 0, len(symbols))
	for _, sym := range symbols {
		code, market, err := fetcher.ParseYahooSymbol(sym)
		if err != nil {
			continue
		}
		snap := Snapshot{
			SchemaVersion: SchemaVersion, Symbol: sym, Source: SourceName,
			ObservedAt: now, Status: Unavailable,
		}
		if r, ok := data[market][code]; ok {
			rc := r
			rc.Symbol = sym
			snap.Ratios = &rc
			snap.Status = Available
		} else {
			snap.Reason = fmt.Sprintf("%s is not in the %s ratio dataset", code, market)
		}
		if _, err := SaveSnapshot(s.Dir, &snap); err != nil {
			s.Logf("valuation: %s not archived: %v", sym, err)
		}
		out = append(out, snap)
	}
	return out, nil
}

// LoadValuation reads the archive and computes the valuation visible at asOfDate.
func (s *Service) LoadValuation(asOfDate, symbol string) (Valuation, error) {
	history, latest, err := LoadHistory(s.Dir, asOfDate, symbol)
	if err != nil {
		s.Logf("valuation: %v", err)
	}
	return Calculate(Input{
		Symbol: symbol, Current: latest, History: history, AsOfDate: asOfDate,
	}), err
}
