package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/candlestick"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// candlestickItems builds a couple of watchlist stocks with enough history to enrich.
func candlestickItems() []fetcher.StockData {
	return []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
}

func enrich(cfg Config) []WatchlistEntry {
	return New(cfg).EnrichWatchlist(
		candlestickItems(),
		map[string]string{},
		map[string]*SectorRotation{},
		map[string][]fetcher.StockData{},
		nil,
	)
}

func TestCandlestickAttachNilWhenDisabled(t *testing.T) {
	out := enrich(Config{}) // EnableCandlestick false (default)
	for _, e := range out {
		if e.Candlestick != nil {
			t.Errorf("%s: Candlestick must be nil when disabled (disabled ≠ no-signal), got %+v", e.A.Symbol, e.Candlestick)
		}
	}
}

func TestCandlestickAttachNonNilWhenEnabled(t *testing.T) {
	out := enrich(Config{EnableCandlestick: true})
	for _, e := range out {
		if e.Candlestick == nil {
			t.Fatalf("%s: Candlestick must be non-nil when enabled", e.A.Symbol)
		}
		// Status must be one of the analyzer statuses (analyzer actually ran).
		switch e.Candlestick.Status {
		case candlestick.StatusOK, candlestick.StatusNoPattern, candlestick.StatusInvalidBar, candlestick.StatusNoData:
		default:
			t.Errorf("%s: unexpected Status %q", e.A.Symbol, e.Candlestick.Status)
		}
	}
}

// TestCandlestickDoesNotAffectScoring is the golden-regression guard using ACTUAL scanner
// output: enabling candlestick must not change RocketScore / WatchAction / ExplosionProb /
// RocketStage or the output order — it only attaches shadow data.
func TestCandlestickDoesNotAffectScoring(t *testing.T) {
	off := enrich(Config{})
	on := enrich(Config{EnableCandlestick: true})

	if len(off) != len(on) {
		t.Fatalf("entry count changed: off=%d on=%d", len(off), len(on))
	}
	for i := range off {
		// order preserved (same symbol at each rank)
		if off[i].A.Symbol != on[i].A.Symbol {
			t.Errorf("rank %d symbol changed: %s → %s", i, off[i].A.Symbol, on[i].A.Symbol)
		}
		if off[i].RocketScore != on[i].RocketScore {
			t.Errorf("%s RocketScore changed: %d → %d", off[i].A.Symbol, off[i].RocketScore, on[i].RocketScore)
		}
		if off[i].WatchAction != on[i].WatchAction {
			t.Errorf("%s WatchAction changed: %s → %s", off[i].A.Symbol, off[i].WatchAction, on[i].WatchAction)
		}
		if off[i].RocketStage != on[i].RocketStage {
			t.Errorf("%s RocketStage changed: %s → %s", off[i].A.Symbol, off[i].RocketStage, on[i].RocketStage)
		}
		if off[i].ExplosionProb != on[i].ExplosionProb {
			t.Errorf("%s ExplosionProb changed: %s → %s", off[i].A.Symbol, off[i].ExplosionProb, on[i].ExplosionProb)
		}
		if off[i].A.Score != on[i].A.Score {
			t.Errorf("%s A.Score changed: %d → %d", off[i].A.Symbol, off[i].A.Score, on[i].A.Score)
		}
		// disabled must be nil; enabled must be populated
		if off[i].Candlestick != nil {
			t.Errorf("%s: off must have nil candlestick", off[i].A.Symbol)
		}
		if on[i].Candlestick == nil {
			t.Errorf("%s: on must have non-nil candlestick", on[i].A.Symbol)
		}
	}
}

func TestConfigValidateShowRequiresEnable(t *testing.T) {
	if err := (Config{ShowCandlestick: true, EnableCandlestick: false}).Validate(); err == nil {
		t.Errorf("show_candlestick without enable_candlestick must be a config error")
	}
	if err := (Config{ShowCandlestick: true, EnableCandlestick: true}).Validate(); err != nil {
		t.Errorf("show+enable must be valid, got %v", err)
	}
	if err := (Config{}).Validate(); err != nil {
		t.Errorf("both off must be valid, got %v", err)
	}
}
