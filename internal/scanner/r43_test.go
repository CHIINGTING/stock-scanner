package scanner

import (
	"fmt"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func wlEntry(code string, score int, sig string) WatchlistEntry {
	e := WatchlistEntry{RocketScore: score}
	e.A.Symbol = code
	if sig != "" {
		e.Shadow = &ShadowSignals{MultiTimeframe: &MultiTimeframe{SignalStrength: sig}}
	}
	return e
}

// sortGated runs the cluster-based tie-breaker and returns the resulting symbol order.
func sortGated(es []WatchlistEntry, gap int) []string {
	sortWatchlistWithMTFTieBreaker(es, gap)
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.A.Symbol
	}
	return out
}

// assertNoOverturn fails if any earlier (lower) score sits above a later score by > gap.
func assertNoOverturn(t *testing.T, es []WatchlistEntry, gap int) {
	t.Helper()
	for i := 0; i < len(es); i++ {
		for j := i + 1; j < len(es); j++ {
			if es[i].RocketScore < es[j].RocketScore-gap {
				t.Fatalf("MTF tie-breaker overturned beyond gap: %s(%d) before %s(%d)",
					es[i].A.Symbol, es[i].RocketScore, es[j].A.Symbol, es[j].RocketScore)
			}
		}
	}
}

// 2. Within a cluster (score gap <= gap), MTF rank breaks the tie (STRONG before WEAK).
func TestR43TieBreakerWithinGap(t *testing.T) {
	es := []WatchlistEntry{wlEntry("WEAK72", 72, "WEAK"), wlEntry("STRONG70", 70, "STRONG")}
	got := sortGated(es, 3) // diff 2 <= 3 → same cluster → STRONG first
	if got[0] != "STRONG70" {
		t.Errorf("within cluster, STRONG should sort first, got %v", got)
	}
}

// 3. Beyond gap (different clusters) → high RocketScore stays first (no overturn).
func TestR43NoOverturnBeyondGap(t *testing.T) {
	es := []WatchlistEntry{wlEntry("HIGH72_WEAK", 72, "WEAK"), wlEntry("LOW66_STRONG", 66, "STRONG")}
	got := sortGated(es, 3) // diff 6 > 3 → different clusters → score wins
	if got[0] != "HIGH72_WEAK" {
		t.Errorf("beyond gap, high score must stay first (no overturn), got %v", got)
	}
}

// 4. Equal MTF rank within a cluster → stable order preserved.
func TestR43SameRankStable(t *testing.T) {
	es := []WatchlistEntry{wlEntry("A", 60, "MODERATE"), wlEntry("B", 60, "MODERATE")}
	got := sortGated(es, 3)
	if got[0] != "A" || got[1] != "B" {
		t.Errorf("equal score+rank → stable order [A B], got %v", got)
	}
}

// 4b. Dense list with low scores deliberately given high MTF rank: the cluster sort
// must NEVER overturn beyond gap (the bug the pairwise comparator had).
func TestR43DenseListInvariant(t *testing.T) {
	gap := 3
	scores := []int{72, 71, 70, 69, 68, 67, 66}
	// low scores STRONG, high scores WEAK → maximal pressure to overturn.
	ranks := []string{"WEAK", "WEAK", "WEAK", "WEAK", "STRONG", "STRONG", "STRONG"}
	var es []WatchlistEntry
	for i, s := range scores {
		es = append(es, wlEntry(fmt.Sprintf("S%d", s), s, ranks[i]))
	}
	sortWatchlistWithMTFTieBreaker(es, gap)
	assertNoOverturn(t, es, gap)

	// Mixed ranks inside a cluster should still respect the invariant.
	es2 := []WatchlistEntry{
		wlEntry("A72", 72, "WEAK"), wlEntry("B71", 71, "STRONG"),
		wlEntry("C70", 70, "WEAK"), wlEntry("D69", 69, "STRONG"),
		wlEntry("E60", 60, "STRONG"), wlEntry("F59", 59, "WEAK"),
	}
	sortWatchlistWithMTFTieBreaker(es2, gap)
	assertNoOverturn(t, es2, gap)
}

// 5. mtfRiskNote wording per branch (risk note, never a trade instruction).
func TestR43RiskNote(t *testing.T) {
	mk := func(sig, dt, wt, ltf, align string) MultiTimeframe {
		return MultiTimeframe{SignalStrength: sig, AlignmentLabel: align, LongTermFilter: ltf,
			Daily: TimeframeView{TrendState: dt}, Weekly: TimeframeView{TrendState: wt}}
	}
	cases := []struct {
		m    MultiTimeframe
		want string
	}{
		{mk("CONFLICTED", "UPTREND", "DOWNTREND", "BULLISH", "CONFLICT"), "短線反彈、週線仍弱，追高需謹慎"},
		{mk("CONFLICTED", "DOWNTREND", "UPTREND", "BULLISH", "CONFLICT"), "週線轉強但日線未跟上，留意拉回"},
		{mk("MODERATE", "UPTREND", "RANGE", "BULLISH", "DAILY_LEADS"), "日線領先、週線中性，屬早期 / 短線訊號"},
		{mk("WEAK", "DOWNTREND", "DOWNTREND", "BULLISH", "FULL_BEAR"), "多週期偏弱"},
		{mk("UNKNOWN", "UNKNOWN", "UNKNOWN", "UNKNOWN", "UNKNOWN"), "週線資料不足，多週期未知"},
		{mk("STRONG", "UPTREND", "UPTREND", "BULLISH", "FULL_BULL"), "日週同步走強"},
	}
	for _, c := range cases {
		if got := mtfRiskNote(c.m); got != c.want {
			t.Errorf("note: got %q want %q", got, c.want)
		}
	}
	// LongTermFilter BEARISH is appended (not replacing) for a non-empty base note.
	if got := mtfRiskNote(MultiTimeframe{SignalStrength: "STRONG", LongTermFilter: "BEARISH",
		Daily: TimeframeView{TrendState: "UPTREND"}, Weekly: TimeframeView{TrendState: "UPTREND"}, AlignmentLabel: "FULL_BULL"}); got != "日週同步走強；位於 200 日線下，長期偏空" {
		t.Errorf("BEARISH append: got %q", got)
	}
}

// 6. Integration: R4-3 changes only ordering(+note), never RocketScore/WatchAction/ExplosionProb.
func TestR43DoesNotChangeScoring(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
	so, rt, mb := map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}

	off := New(Config{}).EnrichWatchlist(items, so, rt, mb, nil)
	base := map[string][3]string{}
	for _, e := range off {
		base[e.A.Symbol] = [3]string{itoa(e.RocketScore), string(e.WatchAction), e.ExplosionProb}
		if e.MTFRiskNote != "" {
			t.Errorf("%s: ungated must have empty MTFRiskNote", e.A.Symbol)
		}
	}

	on := New(Config{
		EnableMultiTimeframe:         true,
		EnableSignalGuardrailScoring: true,
		MTFRiskWarningEnabled:        true,
		MTFSortTieBreakerEnabled:     true,
	}).EnrichWatchlist(items, so, rt, mb, nil)

	for _, e := range on {
		want := base[e.A.Symbol]
		if itoa(e.RocketScore) != want[0] || string(e.WatchAction) != want[1] || e.ExplosionProb != want[2] {
			t.Errorf("%s: R4-3 changed scoring (score/action/prob)", e.A.Symbol)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
