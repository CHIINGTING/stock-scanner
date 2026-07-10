package news

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testIndex() *ResolveIndex {
	idx := NewResolveIndex()
	idx.AddStock("2327", "國巨")
	idx.AddStock("2492", "華新科")
	idx.AddStockSector("2327", "被動元件")
	idx.AddSectorName("被動元件")
	idx.AddSectorName("光通訊")
	idx.ApplyAliases(&AliasDict{Themes: map[string]string{"CCL": "銅箔基板"}})
	return idx
}

type fakeProvider struct {
	name  string
	items []RawNewsItem
	err   error
}

func (f fakeProvider) Name() string                                 { return f.name }
func (f fakeProvider) Fetch(context.Context) ([]RawNewsItem, error) { return f.items, f.err }

func TestDedupMergesSameEpisodeNoDoubleCount(t *testing.T) {
	pub := time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC)
	items := []RawNewsItem{
		{Source: "socialworkerdaily", Title: "股癌筆記EP677", URL: "https://swd/ep677", PublishedAt: pub, EpisodeID: "gooaye-ep-677", Content: "國巨看好突破。"},
		{Source: "twetq", Title: "股癌 EP677", URL: "https://twetq/podcast", PublishedAt: pub.Add(time.Hour), EpisodeID: "gooaye-ep-677"},
	}
	events := Dedup(items)
	if len(events) != 1 {
		t.Fatalf("expected 1 merged event, got %d", len(events))
	}
	if len(events[0].Sources) != 2 {
		t.Errorf("expected 2 sources on merged event, got %d", len(events[0].Sources))
	}
	if !events[0].PublishedAt.Equal(pub) {
		t.Errorf("merged PublishedAt should be earliest (%v), got %v", pub, events[0].PublishedAt)
	}
}

func TestBuildSignalsResolvesAndDoesNotDoubleCount(t *testing.T) {
	idx := testIndex()
	ev := Event{
		EventID:     "gooaye-ep-677",
		Sources:     []NewsSource{{Provider: "socialworkerdaily"}, {Provider: "twetq"}},
		PublishedAt: time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC),
		Items:       []RawNewsItem{{Content: "國巨看好突破，資金流入。CCL 資金輪動進場。"}},
	}
	sigs := BuildSignals(ev, idx, time.Now())
	if len(sigs) == 0 {
		t.Fatal("expected signals")
	}
	for _, s := range sigs {
		if s.Strength < 0 || s.Strength > 10 {
			t.Errorf("strength out of range: %d", s.Strength)
		}
		// two sources must raise confidence, but strength must stay bounded (no per-source sum).
		if s.Confidence < 5 {
			t.Errorf("expected episode+2-source confidence >=5, got %d", s.Confidence)
		}
	}
	// 國巨 should be resolved to 2327 somewhere.
	var sawGuoju bool
	for _, s := range sigs {
		for _, st := range s.Stocks {
			if st.Code == "2327" && st.Resolved {
				sawGuoju = true
			}
		}
	}
	if !sawGuoju {
		t.Error("expected 國巨→2327 resolved reference")
	}
}

func TestResolveNeverGuessesUnknownNames(t *testing.T) {
	idx := testIndex()
	refs := idx.ResolveStocks("這篇提到一家不存在字典的公司「未知科技」")
	if len(refs) != 0 {
		t.Errorf("unknown name must stay unresolved/unbound, got %v", refs)
	}
}

func TestDivergenceDetected(t *testing.T) {
	idx := testIndex()
	now := time.Now()
	pub := now.Add(-24 * time.Hour)
	signals := []NewsSignal{
		{EventID: "e1", PublishedAt: pub, Signal: SignalBullish, Stocks: []StockReference{{Code: "2327", Resolved: true}}},
		{EventID: "e1", PublishedAt: pub, Signal: SignalRotationOut, Sectors: []string{"被動元件"}},
	}
	views := BuildViews(signals, idx)
	v := views["2327"]
	if v == nil {
		t.Fatal("expected view for 2327")
	}
	if !v.Divergence {
		t.Error("expected divergence: stock BULLISH vs sector ROTATION_OUT")
	}
}

func TestBuildSummaryBuckets(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	signals := []NewsSignal{
		{PublishedAt: now.Add(-24 * time.Hour), Signal: SignalRotationIn, Sectors: []string{"銅箔基板"}},   // fresh, in
		{PublishedAt: now.Add(-24 * time.Hour), Signal: SignalRotationOut, Sectors: []string{"光通訊"}},   // fresh, out
		{PublishedAt: now.Add(-40 * 24 * time.Hour), Signal: SignalBullish, Sectors: []string{"被動元件"}}, // expired
	}
	sum := BuildSummary(signals, now, NewsConfig{})
	if sum.Fresh != 2 || sum.Expired != 1 {
		t.Errorf("age tally wrong: fresh=%d expired=%d", sum.Fresh, sum.Expired)
	}
	if len(sum.RotationIn) != 1 || sum.RotationIn[0] != "銅箔基板" {
		t.Errorf("RotationIn = %v", sum.RotationIn)
	}
	if len(sum.RotationOut) != 1 || sum.RotationOut[0] != "光通訊" {
		t.Errorf("RotationOut = %v", sum.RotationOut)
	}
	// expired 被動元件 must not appear in any label group.
	all := append(append(append(append([]string{}, sum.RotationIn...), sum.RotationOut...), sum.Bullish...), sum.Bearish...)
	all = append(all, sum.RiskWarning...)
	for _, s := range all {
		if s == "被動元件" {
			t.Error("expired signal leaked into label groups")
		}
	}
}

// HIGH-1: a BULLISH sector must land in Bullish, NOT RotationIn.
func TestBuildSummaryDoesNotMislabelBullishAsRotationIn(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	signals := []NewsSignal{
		{PublishedAt: now.Add(-24 * time.Hour), Signal: SignalBullish, Sectors: []string{"被動元件"}},
	}
	sum := BuildSummary(signals, now, NewsConfig{})
	if len(sum.Bullish) != 1 || sum.Bullish[0] != "被動元件" {
		t.Errorf("被動元件 BULLISH should be in Bullish, got Bullish=%v", sum.Bullish)
	}
	for _, s := range sum.RotationIn {
		if s == "被動元件" {
			t.Error("BULLISH must not be mislabeled as ROTATION_IN")
		}
	}
}

func TestServiceIsolatesProviderFailure(t *testing.T) {
	idx := testIndex()
	now := time.Now()
	good := fakeProvider{name: "socialworkerdaily", items: []RawNewsItem{
		{Source: "socialworkerdaily", EpisodeID: "gooaye-ep-677", PublishedAt: now.Add(-24 * time.Hour), Content: "國巨看好突破。"},
	}}
	bad := fakeProvider{name: "twetq", err: errors.New("cloudflare 403")}
	svc := NewService([]NewsProvider{bad, good}, idx, NewsConfig{}, func(string, ...any) {})
	res := svc.Collect(context.Background(), now)
	if len(res.Signals) == 0 {
		t.Fatal("failing provider must not suppress the healthy provider's signals")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	observed := time.Date(2026, 7, 11, 8, 30, 0, 0, time.UTC)
	pub := time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC)
	in := []NewsSignal{{EventID: "gooaye-ep-677", PublishedAt: pub, ObservedAt: observed, Signal: SignalBullish, Strength: 6}}
	if _, err := SaveSnapshot(dir, observed, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	recs, err := LoadSnapshotsForDate(dir, observed)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != 1 || len(recs[0].Signals) != 1 || recs[0].Signals[0].EventID != "gooaye-ep-677" {
		t.Fatalf("round-trip mismatch: %+v", recs)
	}
	sig := recs[0].Signals[0]
	// PublishedAt and ObservedAt must survive and stay distinct (look-ahead guard).
	if !sig.PublishedAt.Equal(pub) || !sig.ObservedAt.Equal(observed) {
		t.Errorf("timestamps not preserved: pub=%v obs=%v", sig.PublishedAt, sig.ObservedAt)
	}
	if sig.PublishedAt.Equal(sig.ObservedAt) {
		t.Error("PublishedAt and ObservedAt must remain separable")
	}
}

// MED-5: two runs the same day must produce two snapshot files; the first is not lost.
func TestSnapshotSameDayNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	runA := time.Date(2026, 7, 11, 8, 30, 0, 0, time.UTC)
	runB := time.Date(2026, 7, 11, 13, 30, 0, 0, time.UTC)
	pA, err := SaveSnapshot(dir, runA, []NewsSignal{{EventID: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	pB, err := SaveSnapshot(dir, runB, []NewsSignal{{EventID: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if pA == pB {
		t.Fatalf("two same-day runs must not share a path: %s", pA)
	}
	recs, err := LoadSnapshotsForDate(dir, runA)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 snapshots for the day, got %d", len(recs))
	}
	// sorted by ObservedAt: morning run A preserved first, afternoon run B second.
	if !recs[0].ObservedAt.Equal(runA) || !recs[1].ObservedAt.Equal(runB) {
		t.Errorf("snapshots not preserved/ordered: %v", recs)
	}
}

// MED-5 edge: even two runs in the SAME second must not clobber each other.
func TestSnapshotSameSecondNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 11, 8, 30, 0, 0, time.UTC)
	pA, _ := SaveSnapshot(dir, ts, []NewsSignal{{EventID: "a"}})
	pB, _ := SaveSnapshot(dir, ts, []NewsSignal{{EventID: "b"}})
	if pA == pB {
		t.Fatalf("same-second runs clobbered: %s", pA)
	}
	recs, _ := LoadSnapshotsForDate(dir, ts)
	if len(recs) != 2 {
		t.Fatalf("expected 2 files, got %d", len(recs))
	}
}

// HIGH-4: historical analysis date must NOT be treated as a live-fetch day, and a
// signal's ObservedAt is the wall-clock fetch time, never the analysis date.
func TestLookAheadGuard(t *testing.T) {
	analysisDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	fetchTime := time.Date(2026, 7, 11, 5, 45, 0, 0, time.UTC)
	if SameDay(analysisDate, fetchTime) {
		t.Fatal("historical analysis date must not count as the live-fetch day")
	}
	if !SameDay(fetchTime, fetchTime) {
		t.Fatal("same day should be true")
	}
	// Collect stamps ObservedAt from the wall-clock arg, independent of any analysis date.
	idx := testIndex()
	good := fakeProvider{name: "socialworkerdaily", items: []RawNewsItem{
		{Source: "socialworkerdaily", EpisodeID: "gooaye-ep-677", PublishedAt: analysisDate, Content: "國巨看好突破。"},
	}}
	svc := NewService([]NewsProvider{good}, idx, NewsConfig{}, func(string, ...any) {})
	res := svc.Collect(context.Background(), fetchTime)
	for _, s := range res.Signals {
		if !s.ObservedAt.Equal(fetchTime) {
			t.Errorf("ObservedAt=%v want wall-clock %v", s.ObservedAt, fetchTime)
		}
		if s.ObservedAt.Equal(analysisDate) {
			t.Error("ObservedAt must never equal the historical analysis date")
		}
	}
}

// HIGH-2: same episode from two sources with opposing directions keeps both signals +
// evidence and marks the conflict (no last/longest/first-wins).
func TestCrossSourceConflictPreserved(t *testing.T) {
	idx := testIndex()
	pub := time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC)
	items := []RawNewsItem{
		{Source: "socialworkerdaily", URL: "https://swd/677", EpisodeID: "gooaye-ep-677", PublishedAt: pub, Content: "被動元件資金流出。"},
		{Source: "twetq", URL: "https://twetq/677", EpisodeID: "gooaye-ep-677", PublishedAt: pub, Content: "被動元件看好強勢。"},
	}
	events := Dedup(items)
	if len(events) != 1 {
		t.Fatalf("expected 1 merged event, got %d", len(events))
	}
	sigs := BuildSignals(events[0], idx, time.Now())

	var outSig, bullSig *NewsSignal
	for i := range sigs {
		switch sigs[i].Signal {
		case SignalRotationOut:
			outSig = &sigs[i]
		case SignalBullish:
			bullSig = &sigs[i]
		}
	}
	if outSig == nil || bullSig == nil {
		t.Fatalf("both opposing signals must survive; got %+v", sigs)
	}
	// both evidences preserved, each attributed to its own source.
	if len(outSig.Evidence) == 0 || len(bullSig.Evidence) == 0 {
		t.Error("evidence from each source must be preserved")
	}
	if outSig.Sources[0].Provider != "socialworkerdaily" || bullSig.Sources[0].Provider != "twetq" {
		t.Errorf("sources misattributed: out=%v bull=%v", outSig.Sources, bullSig.Sources)
	}
	// event carries 2 sources across its signals, and the conflict is visible.
	providers := map[string]bool{}
	for _, s := range sigs {
		for _, src := range s.Sources {
			providers[src.Provider] = true
		}
	}
	if len(providers) != 2 {
		t.Errorf("expected 2 sources across event signals, got %v", providers)
	}
	if !outSig.Conflict || !bullSig.Conflict {
		t.Error("opposing signals on the same sector must be flagged Conflict=true")
	}
}
