package fetcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleStocksYAML = `# 持股與觀察清單
positions:
  - code: "00632R"
    name: "0050反一"
    entry: 10.63
    shares: 4000

watchlist:
  - code: "3290"
    name: "東浦"
`

// writeTempStocks writes the sample file to a temp dir and returns its path.
func writeTempStocks(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stocks.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp stocks: %v", err)
	}
	return path
}

func loadCodes(t *testing.T, path string) (positions, watch map[string]bool) {
	t.Helper()
	sl, err := LoadStockList(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	positions = map[string]bool{}
	for _, p := range sl.AllPositions() {
		positions[p.Code] = true
	}
	watch = map[string]bool{}
	for _, w := range sl.Watchlist {
		watch[w.Code] = true
	}
	return positions, watch
}

func TestUpdateWatchlist_AddsBuyAndWatch(t *testing.T) {
	path := writeTempStocks(t, sampleStocksYAML)

	added, err := UpdateWatchlistFile(path, []WatchCandidate{
		{Code: "2330", Name: "台積電"}, // would be BUY
		{Code: "2454", Name: "聯發科"}, // would be WATCH
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(added) != 2 {
		t.Fatalf("added = %d, want 2 (%v)", len(added), added)
	}

	_, watch := loadCodes(t, path)
	for _, code := range []string{"2330", "2454"} {
		if !watch[code] {
			t.Errorf("watchlist missing %s after update", code)
		}
	}
}

func TestUpdateWatchlist_NoDuplicateExistingWatch(t *testing.T) {
	path := writeTempStocks(t, sampleStocksYAML)

	// 3290 already exists in watchlist; 2330 is new.
	added, err := UpdateWatchlistFile(path, []WatchCandidate{
		{Code: "3290", Name: "東浦"},
		{Code: "2330", Name: "台積電"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(added) != 1 || added[0].Code != "2330" {
		t.Fatalf("added = %v, want only 2330", added)
	}

	// Count occurrences of code 3290 in the file: must remain exactly one.
	data, _ := os.ReadFile(path)
	if n := strings.Count(string(data), `"3290"`); n != 1 {
		t.Errorf("code 3290 appears %d times, want 1 (no duplicate)", n)
	}
}

func TestUpdateWatchlist_SkipsCodeInPositions(t *testing.T) {
	path := writeTempStocks(t, sampleStocksYAML)

	// 00632R is a position; it must never be added to the watchlist.
	added, err := UpdateWatchlistFile(path, []WatchCandidate{
		{Code: "00632R", Name: "0050反一"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v, want none (position must be skipped)", added)
	}

	_, watch := loadCodes(t, path)
	if watch["00632R"] {
		t.Error("position 00632R must not appear in watchlist")
	}
}

func TestUpdateWatchlist_PositionsUnchanged(t *testing.T) {
	path := writeTempStocks(t, sampleStocksYAML)

	if _, err := UpdateWatchlistFile(path, []WatchCandidate{
		{Code: "2330", Name: "台積電"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	sl, err := LoadStockList(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	pos := sl.AllPositions()
	if len(pos) != 1 {
		t.Fatalf("positions count = %d, want 1", len(pos))
	}
	p := pos[0]
	if p.Code != "00632R" || p.Name != "0050反一" || p.EntryPrice() != 10.63 || p.Shares != 4000 {
		t.Errorf("position mutated: %+v", p)
	}
}

func TestUpdateWatchlist_PreservesComments(t *testing.T) {
	path := writeTempStocks(t, sampleStocksYAML)

	if _, err := UpdateWatchlistFile(path, []WatchCandidate{
		{Code: "2330", Name: "台積電"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# 持股與觀察清單") {
		t.Error("leading comment was not preserved")
	}
}

func TestUpdateWatchlist_NoCandidatesLeavesFileUntouched(t *testing.T) {
	path := writeTempStocks(t, sampleStocksYAML)
	before, _ := os.ReadFile(path)

	added, err := UpdateWatchlistFile(path, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v, want none", added)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("file changed even though nothing was added")
	}
}
