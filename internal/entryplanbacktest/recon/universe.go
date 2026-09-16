package recon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// DateLayout is the repo's session-date format.
const DateLayout = "2006-01-02"

// cacheFile mirrors the on-disk .cache record, the same two-level shape
// internal/r6backtest/engine.go:17 and internal/validator/price.go:28 both read.
type cacheFile struct {
	Data fetcher.StockData `json:"data"`
}

// Symbol is one cached symbol with its bars and a date→index map.
type Symbol struct {
	Data  fetcher.StockData
	idxOf map[string]int
	// file is the cache filename this came from, kept only so a de-duplication can say what
	// it dropped.
	file string
}

// File is the cache filename this symbol was loaded from.
func (s *Symbol) File() string { return s.file }

// betterSeries reports whether a should win a symbol clash against b. See LoadCache.
func betterSeries(a, b *Symbol) bool {
	al, bl := a.Data.Candles[len(a.Data.Candles)-1].Date, b.Data.Candles[len(b.Data.Candles)-1].Date
	if !al.Equal(bl) {
		return al.After(bl)
	}
	if n, m := len(a.Data.Candles), len(b.Data.Candles); n != m {
		return n > m
	}
	return a.file < b.file
}

// IndexOf returns the bar index for a date key, or (-1,false). Same contract as
// r6backtest.Stock.IndexOf (internal/r6backtest/types.go:54).
func (s *Symbol) IndexOf(date string) (int, bool) {
	i, ok := s.idxOf[date]
	if !ok {
		return -1, false
	}
	return i, true
}

// BarCount is the number of cached bars.
func (s *Symbol) BarCount() int { return len(s.Data.Candles) }

// Cache is the whole local price cache, loaded ONCE and then sliced per session.
//
// Loading is the expensive part (JSON decode of ~2,000 files); slicing is not, because
// TruncateAt returns SUBSLICES of the already-decoded candle arrays rather than copies. That
// is what makes a 474-session sweep arithmetically possible at all: one decode, N slices.
//
// READ-ONLY. It never writes to .cache, and the StockData values it hands out share backing
// arrays with the cache, so a consumer that mutates a candle corrupts every later session.
// Nothing in this package mutates one.
type Cache struct {
	Symbols []*Symbol
	// Axis is the sorted unique set of dates on which SOME symbol printed a bar — the same
	// construction r6backtest.LoadUniverse uses for Universe.Axis.
	Axis []string

	// bySym is a lazily built code → symbol index. Unexported so the map cannot be widened
	// or repointed from outside; Symbol() is the only way in.
	bySym map[string]*Symbol

	// DroppedDuplicates records the cache files a de-duplication dropped, keyed by symbol.
	// EP-9 §24 found this: .cache holds both 5236_TW.json and 5236_TWO.json — the same
	// listing before and after a transfer between the two exchanges — and carrying both put
	// that stock into every session twice, DOUBLE-WEIGHTING it in every metric. The fix is a
	// de-duplication, and the dropped file is RECORDED rather than silently discarded,
	// because a population the study quietly shrank is a population nobody can audit.
	DroppedDuplicates map[string]string
}

// LoadCache reads every .cache/*.json into memory.
//
// minBars drops symbols too short to be worth carrying at all. It is NOT the study's
// eligibility rule — that is per (symbol, session) and lives in TruncateAt — it only keeps
// the resident set down.
func LoadCache(dir string, minBars int) (*Cache, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("recon: no *.json under %s", dir)
	}
	c := &Cache{DroppedDuplicates: map[string]string{}}
	dateSet := make(map[string]struct{}, 800)
	byCode := make(map[string]*Symbol, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var cf cacheFile
		if json.Unmarshal(b, &cf) != nil {
			continue
		}
		bars := cf.Data.Candles
		if len(bars) < minBars || cf.Data.Symbol == "" {
			continue
		}
		sort.Slice(bars, func(i, j int) bool { return bars[i].Date.Before(bars[j].Date) })
		s := &Symbol{Data: cf.Data, idxOf: make(map[string]int, len(bars)), file: filepath.Base(f)}
		s.Data.Candles = bars
		for i, k := range bars {
			d := k.Date.Format(DateLayout)
			s.idxOf[d] = i
		}
		// DE-DUPLICATION BY SYMBOL, and the winner is chosen by a DETERMINISTIC rule rather
		// than by whichever file the glob returned first:
		//
		//	1. the series whose LAST bar is newer — the live listing after a transfer;
		//	2. then the longer series;
		//	3. then the lexicographically smaller filename, so two identical series still
		//	   resolve the same way on every run and on every machine.
		//
		// A non-deterministic winner would make the whole study non-reproducible for that
		// symbol, which is worse than the double-count it was fixing.
		if prev, clash := byCode[s.Data.Symbol]; clash {
			win, lose := prev, s
			if betterSeries(s, prev) {
				win, lose = s, prev
			}
			byCode[s.Data.Symbol] = win
			c.DroppedDuplicates[s.Data.Symbol] = lose.file
			continue
		}
		byCode[s.Data.Symbol] = s
	}
	for _, s := range byCode {
		c.Symbols = append(c.Symbols, s)
		for d := range s.idxOf {
			dateSet[d] = struct{}{}
		}
	}
	c.Axis = make([]string, 0, len(dateSet))
	for d := range dateSet {
		c.Axis = append(c.Axis, d)
	}
	sort.Strings(c.Axis)
	sort.Slice(c.Symbols, func(i, j int) bool { return c.Symbols[i].Data.Symbol < c.Symbols[j].Data.Symbol })
	return c, nil
}

// TruncateAt returns every symbol that traded on date, with its candles cut to end ON that
// session, and only when at least minBars bars precede it inclusive.
//
// THE CUT IS THE POINT-IN-TIME GUARANTEE and it is a slice bound, exactly as
// analyzer.ReplayRegimes uses one (internal/market/analyzer/regime_replay.go:69, "the
// point-in-time guarantee; never widen this"). A symbol with no bar on date is OMITTED rather
// than carried with an older last bar: entryPlanSeriesFor would refuse the misaligned series
// anyway, and a scanner analysis whose StockAnalysis.Date is a different session from every
// other stock's is not an observation of this session.
func (c *Cache) TruncateAt(date string, minBars int) []fetcher.StockData {
	out := make([]fetcher.StockData, 0, len(c.Symbols))
	for _, s := range c.Symbols {
		i, ok := s.IndexOf(date)
		if !ok || i+1 < minBars {
			continue
		}
		d := s.Data
		d.Candles = s.Data.Candles[:i+1]
		out = append(out, d)
	}
	return out
}

// EligibleSessions returns the sessions on the axis that can carry an observation: at least
// minBars of history and at least forward sessions after them, measured on the MARKET axis.
//
// The market axis is the right axis for choosing WHICH sessions to run; per-symbol
// eligibility is TruncateAt's job and is counted separately. Using the per-symbol axis here
// would make the session list depend on which symbols happened to be loaded.
func (c *Cache) EligibleSessions(minBars, forward int) []string {
	if len(c.Axis) == 0 || minBars < 1 || forward < 0 {
		return nil
	}
	lo := minBars - 1
	hi := len(c.Axis) - 1 - forward
	if lo > hi || lo < 0 {
		return nil
	}
	out := make([]string, hi-lo+1)
	copy(out, c.Axis[lo:hi+1])
	return out
}

// ParseDate is the one place a session string becomes a time.Time in this package.
func ParseDate(d string) (time.Time, error) { return time.Parse(DateLayout, d) }

// Symbol returns the cached symbol by code, or nil.
func (c *Cache) Symbol(code string) *Symbol {
	if c.bySym == nil {
		c.bySym = make(map[string]*Symbol, len(c.Symbols))
		for _, s := range c.Symbols {
			c.bySym[s.Data.Symbol] = s
		}
	}
	return c.bySym[code]
}
