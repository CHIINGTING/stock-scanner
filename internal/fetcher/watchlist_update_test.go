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

	// The return value is now the RESULTING watchlist, not the newly-added subset: the
	// function rebuilds rather than appends, so "what was added" is no longer the useful
	// answer — "what the list is now" is.
	got, err := UpdateWatchlistFile(path, []WatchCandidate{
		{Code: "3290", Name: "東浦"},
		{Code: "2330", Name: "台積電"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want both candidates in the rebuilt list", got)
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

const pinnedStocksYAML = `# 檔頭註解必須保留
positions:
  - code: "2337"
    name: "旺宏"
    entry: 173.5
    shares: 1000

watchlist_pinned:
  - code: "8299"
    name: "群聯"

watchlist:
  - code: "1111"
    name: "沉積一"
  - code: "2222"
    name: "沉積二"
  - code: "3333"
    name: "沉積三"
`

// The whole point of the change: entries that no longer qualify are DROPPED, not kept.
// Append-only had grown the real file to 748 entries against a 66-entry daily signal.
func TestRebuildDropsStaleEntries(t *testing.T) {
	path := writeTempStocks(t, pinnedStocksYAML)

	got, err := UpdateWatchlistFile(path, []WatchCandidate{{Code: "9999", Name: "今日候選"}})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(got) != 1 || got[0].Code != "9999" {
		t.Fatalf("result = %v, want exactly today's candidate", got)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, stale := range []string{"1111", "2222", "3333"} {
		if strings.Contains(text, stale) {
			t.Errorf("stale entry %s survived the rebuild", stale)
		}
	}
	if !strings.Contains(text, "9999") {
		t.Error("today's candidate is missing from the rebuilt list")
	}
}

// positions and watchlist_pinned are human-owned. No rebuild may touch either.
func TestRebuildNeverTouchesHumanOwnedSections(t *testing.T) {
	path := writeTempStocks(t, pinnedStocksYAML)

	// Today's scan surfaces a held stock and a pinned stock as well as a fresh one. Neither
	// may be duplicated into the machine list, and neither may be removed from its own.
	if _, err := UpdateWatchlistFile(path, []WatchCandidate{
		{Code: "2337", Name: "旺宏"},   // held
		{Code: "8299", Name: "群聯"},   // pinned
		{Code: "9999", Name: "今日候選"}, // genuinely new
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)

	if !strings.Contains(text, "watchlist_pinned") || !strings.Contains(text, "8299") {
		t.Error("the pinned section was damaged")
	}
	if !strings.Contains(text, `entry: 173.5`) || !strings.Contains(text, `shares: 1000`) {
		t.Error("the positions section was damaged")
	}
	if !strings.Contains(text, "# 檔頭註解必須保留") {
		t.Error("the header comment was lost")
	}
	// Each code appears exactly once in the whole file — no duplication across sections.
	for _, code := range []string{"2337", "8299"} {
		if n := strings.Count(text, `"`+code+`"`); n != 1 {
			t.Errorf("code %s appears %d times, want 1", code, n)
		}
	}
}

// A scan that produced nothing and a scan that failed to produce anything are
// indistinguishable here, so an empty candidate set must never empty the shortlist.
func TestRebuildRefusesToEmptyOnZeroCandidates(t *testing.T) {
	path := writeTempStocks(t, pinnedStocksYAML)
	before, _ := os.ReadFile(path)

	got, err := UpdateWatchlistFile(path, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got != nil {
		t.Errorf("no candidates should report nothing, got %v", got)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("an empty scan wiped the watchlist")
	}
}

// Re-running the same scan must leave no diff, so a scheduled job does not produce a commit
// on every run.
func TestRebuildIsIdempotent(t *testing.T) {
	path := writeTempStocks(t, pinnedStocksYAML)
	cands := []WatchCandidate{{Code: "9999", Name: "甲"}, {Code: "8888", Name: "乙"}}

	if _, err := UpdateWatchlistFile(path, cands); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if _, err := UpdateWatchlistFile(path, cands); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Error("re-running the same scan rewrote the file")
	}
}
