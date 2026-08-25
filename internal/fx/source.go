package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// USD/TWD acquisition and archive.
//
// The series is fetched ONCE into a file and read from there afterwards, for the same reason
// internal/institution and internal/news keep snapshots: the research has to be reproducible,
// and a number that changes because someone re-ran a fetch is not evidence.

const (
	// Symbol is the internal name. USDTWD = 1 USD in TWD; see the package comment.
	Symbol = "USDTWD"
	// SymbolDXY is the ICE US Dollar Index — the BROAD dollar, against a basket that does
	// not contain TWD. It is here so the research can ask whether a USD/TWD move was the
	// dollar moving or the local currency moving; those are different facts with different
	// implications, and USD/TWD alone cannot tell them apart.
	SymbolDXY = "DXY"

	yahooChart   = "https://query1.finance.yahoo.com/v8/finance/chart/"
	defaultStore = "data/fx"
)

// SourceSpec is one series' acquisition contract.
//
// MinPlausible/MaxPlausible are not paranoia. A ticker that silently starts returning the
// INVERSE pair, or a different instrument, would reverse or scramble every conclusion drawn
// from it while looking entirely normal in a report — so the level is checked rather than the
// ticker name trusted.
type SourceSpec struct {
	Symbol       string
	YahooTicker  string
	File         string
	MinPlausible float64
	MaxPlausible float64
}

// SpecUSDTWD is the local pair. Yahoo's "TWD=X" IS USD/TWD (the quote currency names the
// ticker), which is easy to read as the inverse — hence the range check.
var SpecUSDTWD = SourceSpec{
	Symbol: Symbol, YahooTicker: "TWD=X", File: "usdtwd.json",
	MinPlausible: 15, MaxPlausible: 60,
}

// SpecDXY is the ICE US Dollar Index. It has ranged roughly 70–120 since inception; anything
// outside that is a different instrument.
var SpecDXY = SourceSpec{
	Symbol: SymbolDXY, YahooTicker: "DX-Y.NYB", File: "dxy.json",
	MinPlausible: 50, MaxPlausible: 200,
}

// yahooChartOverride redirects the endpoint in tests. Empty in every real build — the
// production path reads the const above and cannot be pointed elsewhere by configuration.
var yahooChartOverride string

// londonTZ is where Yahoo's daily FX buckets are anchored — the bar labelled D runs from
// 00:00 to 23:59 London on D. Used only to decide which bars are finished.
var londonTZ = func() *time.Location {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// Fetch downloads the daily USD/TWD series. Thin wrapper kept so existing callers read the
// same as before.
func Fetch(ctx context.Context, httpc *http.Client, years int) (*Series, error) {
	return FetchSeries(ctx, httpc, SpecUSDTWD, years)
}

// FetchSeries downloads one daily series.
//
// It returns only FINISHED bars. Yahoo appends the in-progress session — with a null close,
// and again as a live intraday row — and persisting that would freeze a partial price into
// the archive as though it were a settled one.
//
// "Finished" is decided in the SERIES' OWN exchange day, not ours: the bars are bucketed by
// the venue's local midnight (London for the FX pair, New York for the dollar index), so the
// cutoff has to be taken there or the newest complete bar would be discarded for half a day.
func FetchSeries(ctx context.Context, httpc *http.Client, spec SourceSpec, years int) (*Series, error) {
	if httpc == nil {
		httpc = &http.Client{Timeout: 30 * time.Second}
	}
	if years <= 0 {
		years = 5
	}
	base := yahooChart
	if yahooChartOverride != "" {
		base = yahooChartOverride
	}
	url := fmt.Sprintf("%s%s?range=%dy&interval=1d", base, spec.YahooTicker, years)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fx: request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fx: fetch %s: %w", spec.YahooTicker, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fx: fetch %s: HTTP %d", spec.YahooTicker, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("fx: read: %w", err)
	}

	var payload struct {
		Chart struct {
			Result []struct {
				Meta struct {
					ExchangeTimezoneName string `json:"exchangeTimezoneName"`
				} `json:"meta"`
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []*float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error *struct {
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("fx: parse: %w", err)
	}
	if payload.Chart.Error != nil {
		return nil, fmt.Errorf("fx: yahoo: %s", payload.Chart.Error.Description)
	}
	if len(payload.Chart.Result) == 0 || len(payload.Chart.Result[0].Indicators.Quote) == 0 {
		return nil, fmt.Errorf("fx: yahoo returned no series for %s", spec.YahooTicker)
	}

	r := payload.Chart.Result[0]
	closes := r.Indicators.Quote[0].Close

	// Bucket in the VENUE's timezone, taken from the response rather than assumed.
	venue := londonTZ
	if name := r.Meta.ExchangeTimezoneName; name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			venue = loc
		}
	}
	// A bar is finished only once its whole venue day is past.
	cutoff := time.Now().In(venue).Format("2006-01-02")

	seen := map[string]bool{}
	s := &Series{Symbol: spec.Symbol}
	for i, ts := range r.Timestamp {
		if i >= len(closes) || closes[i] == nil {
			continue // the unformed session
		}
		c := *closes[i]
		if c < spec.MinPlausible || c > spec.MaxPlausible {
			return nil, fmt.Errorf("fx: %s quote %.4f is outside the plausible %s range "+
				"[%v, %v] — the instrument or its direction is not what this code assumes",
				spec.YahooTicker, c, spec.Symbol, spec.MinPlausible, spec.MaxPlausible)
		}
		date := time.Unix(ts, 0).In(venue).Format("2006-01-02")
		if date >= cutoff {
			continue // today's session is still trading
		}
		if seen[date] {
			continue // the appended live row duplicates its own day
		}
		seen[date] = true
		s.Bars = append(s.Bars, Bar{Date: date, Close: c})
	}
	if len(s.Bars) == 0 {
		return nil, fmt.Errorf("fx: no finished bars in the response")
	}
	sort.Slice(s.Bars, func(i, j int) bool { return s.Bars[i].Date < s.Bars[j].Date })
	return s, nil
}

// Save writes the USD/TWD series. Thin wrapper over SaveSeries.
func Save(dir string, s *Series) (string, error) { return SaveSeries(dir, SpecUSDTWD, s) }

// SaveSeries writes a series atomically (temp + rename), matching the other snapshot stores.
func SaveSeries(dir string, spec SourceSpec, s *Series) (string, error) {
	if s == nil || len(s.Bars) == 0 {
		return "", fmt.Errorf("fx: refusing to save an empty series")
	}
	if dir == "" {
		dir = defaultStore
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("fx: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, spec.File)

	// Never shrink the archive. A truncated upstream response must not silently delete
	// history the research already depends on.
	if old, err := LoadSeries(dir, spec); err == nil && len(old.Bars) > len(s.Bars) {
		return "", fmt.Errorf("fx: refusing to replace %d stored bars with %d — "+
			"the upstream response is shorter than the archive", len(old.Bars), len(s.Bars))
	}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("fx: encode: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", fmt.Errorf("fx: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("fx: replace %s: %w", path, err)
	}
	return path, nil
}

// Load reads the archived USD/TWD series. Thin wrapper over LoadSeries.
func Load(dir string) (*Series, error) { return LoadSeries(dir, SpecUSDTWD) }

// LoadSeries reads an archived series. A missing file is an error, not an empty series: the
// caller must be able to tell "no data configured" from "the rate did not move".
func LoadSeries(dir string, spec SourceSpec) (*Series, error) {
	if dir == "" {
		dir = defaultStore
	}
	b, err := os.ReadFile(filepath.Join(dir, spec.File))
	if err != nil {
		return nil, fmt.Errorf("fx: load: %w", err)
	}
	var s Series
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("fx: decode: %w", err)
	}
	if len(s.Bars) == 0 {
		return nil, fmt.Errorf("fx: archive is empty")
	}
	sort.Slice(s.Bars, func(i, j int) bool { return s.Bars[i].Date < s.Bars[j].Date })
	return &s, nil
}

// DefaultStoreDir is where the archive lives when the caller does not say.
func DefaultStoreDir() string { return defaultStore }
