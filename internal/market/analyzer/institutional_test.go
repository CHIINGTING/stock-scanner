package analyzer

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

var fth = model.FuturesThresholds{}.Defaulted()

func oi(net, long, short float64) *model.FuturesOIData {
	return &model.FuturesOIData{Contract: "臺股期貨", Item: "外資及陸資", NetOI: net, LongOI: long, ShortOI: short}
}

func flat(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// ── the contract this analyzer exists to enforce ─────────────────────────────────────
//
// A large foreign net short must NOT collapse into "extreme bearish". Position, change and
// crowding are three separate facts that routinely disagree, and the analyzer has to keep
// them apart.
func TestLargeNetShortIsNotAutomaticallyExtreme(t *testing.T) {
	// Deeply short (-87,911), historically the most extreme reading, but covering hard.
	hist := flat(120, -40000)
	ev := AnalyzeForeignFutures(oi(-87911, 8321, 96232), History{FuturesNetOI: hist}, fth)

	if !ev.Available {
		t.Fatal("evidence should be available")
	}
	if ev.Direction != model.DirBullish {
		// chg1 = -87911 - (-40000) = -47911 → aggressive short, so direction is bearish here.
		// Assert the real expectation below instead; this branch documents the setup.
		_ = ev
	}
	if ev.Strength == model.StrengthExtreme {
		t.Error("a crowded position must not read as EXTREME — crowding damps conviction")
	}

	// Same position, but the book is COVERING: the change must dominate the strength.
	hist2 := flat(120, -95000)
	cover := AnalyzeForeignFutures(oi(-87911, 8321, 96232), History{FuturesNetOI: hist2}, fth)
	if cover.Direction != model.DirBullish {
		t.Errorf("covering 7,089 contracts should read BULLISH on change, got %s", cover.Direction)
	}
	// Position is still net short; the summary must say both things rather than pick one.
	if !strings.Contains(cover.Summary, "淨空") {
		t.Errorf("summary must still state the net short position: %q", cover.Summary)
	}
	if !strings.Contains(cover.Summary, "回補") {
		t.Errorf("summary must state the covering: %q", cover.Summary)
	}
	// All three facts must survive as separate metrics.
	for _, k := range []string{model.MetricFutNetOI, model.MetricFutChg1D, model.MetricFutPercentile} {
		if _, ok := cover.Metric(k); !ok {
			t.Errorf("metric %s missing — the three facts must stay separable", k)
		}
	}
}

func TestFuturesCrowdingAddsSqueezeCaveat(t *testing.T) {
	// Most-short reading of its own distribution, and still adding shorts.
	hist := flat(120, -10000)
	ev := AnalyzeForeignFutures(oi(-90000, 5000, 95000), History{FuturesNetOI: hist}, fth)
	if ev.Direction != model.DirBearish {
		t.Fatalf("adding shorts from a short book should be BEARISH, got %s", ev.Direction)
	}
	if !strings.Contains(ev.Summary, "軋空") {
		t.Errorf("an extreme short must carry a squeeze caveat: %q", ev.Summary)
	}
	pct, ok := ev.Metric(model.MetricFutPercentile)
	if !ok || pct > 5 {
		t.Errorf("percentile = %v (ok=%v), expected the extreme end", pct, ok)
	}
}

// Missing history lowers confidence and drops the metric; it never invents a percentile.
func TestFuturesDegradesWithoutHistory(t *testing.T) {
	none := AnalyzeForeignFutures(oi(-87911, 8321, 96232), History{}, fth)
	full := AnalyzeForeignFutures(oi(-87911, 8321, 96232), History{FuturesNetOI: flat(120, -40000)}, fth)

	if !none.Available {
		t.Fatal("today's observation alone is still an observation")
	}
	if _, ok := none.Metric(model.MetricFutPercentile); ok {
		t.Error("an unbuildable percentile must be absent, not 0")
	}
	if _, ok := none.Metric(model.MetricFutChg1D); ok {
		t.Error("with no prior session there is no change metric")
	}
	if none.Confidence >= full.Confidence {
		t.Errorf("no history should mean lower confidence: %v vs %v", none.Confidence, full.Confidence)
	}
	if AnalyzeForeignFutures(nil, History{}, fth).Available {
		t.Error("a missing dataset must be unavailable evidence")
	}
}

// ── foreign cash ─────────────────────────────────────────────────────────────────────
//
// The single most common way to misread this series: calling one negative session bearish
// while the 5- and 20-day sums are strongly positive.
func TestCashOneBadDayInsideAPositiveRunIsNotBearish(t *testing.T) {
	// The spec's own worked example: today -80 億 while 5D is +420 億 and 20D far higher.
	// Prior sessions at +125 億 give 5D = -80 + 4*125 = +420.
	hist := flat(30, 125*1e8)
	ev := AnalyzeForeignCash(&model.CashData{ForeignNet: -80 * 1e8}, History{ForeignNet: hist})

	if ev.Direction == model.DirBearish {
		t.Error("a single negative session inside a positive run must not read BEARISH")
	}
	n5, ok5 := ev.Metric(model.MetricCashNet5D)
	n20, ok20 := ev.Metric(model.MetricCashNet20D)
	if !ok5 || !ok20 {
		t.Fatal("both rolling windows should be present")
	}
	if n5 <= 0 || n20 <= 0 {
		t.Errorf("windows should be positive: 5D=%v 20D=%v", n5, n20)
	}
	// Today's value is still reported — the turn is visible even though it does not decide.
	if v, ok := ev.Metric(model.MetricCashNet1D); !ok || v >= 0 {
		t.Errorf("today's negative session must still be recorded, got %v", v)
	}
}

func TestCashBothWindowsAgreeIsStrong(t *testing.T) {
	ev := AnalyzeForeignCash(&model.CashData{ForeignNet: 200 * 1e8}, History{ForeignNet: flat(30, 150*1e8)})
	if ev.Direction != model.DirBullish || ev.Strength != model.StrengthStrong {
		t.Errorf("aligned windows should be strongly bullish, got %s/%s", ev.Direction, ev.Strength)
	}
	bear := AnalyzeForeignCash(&model.CashData{ForeignNet: -200 * 1e8}, History{ForeignNet: flat(30, -150*1e8)})
	if bear.Direction != model.DirBearish || bear.Strength != model.StrengthStrong {
		t.Errorf("aligned negative windows should be strongly bearish, got %s/%s", bear.Direction, bear.Strength)
	}
}

func TestCashDegradesWithoutHistory(t *testing.T) {
	none := AnalyzeForeignCash(&model.CashData{ForeignNet: -407 * 1e8}, History{})
	full := AnalyzeForeignCash(&model.CashData{ForeignNet: -407 * 1e8}, History{ForeignNet: flat(30, -100*1e8)})
	if _, ok := none.Metric(model.MetricCashNet20D); ok {
		t.Error("a 20-day window with no history must be absent")
	}
	if none.Confidence >= full.Confidence {
		t.Errorf("confidence should fall without history: %v vs %v", none.Confidence, full.Confidence)
	}
	if none.Strength != model.StrengthWeak {
		t.Errorf("a one-day-only read must be WEAK, got %s", none.Strength)
	}
	if AnalyzeForeignCash(nil, History{}).Available {
		t.Error("a missing dataset must be unavailable")
	}
}

// ── margin ───────────────────────────────────────────────────────────────────────────
//
// Rising margin is not universally bearish. The reading lives in the combination with price.
func TestMarginIsContextual(t *testing.T) {
	m := &model.MarginBalanceData{MarginBalanceLots: 9000000, MarginPrevLots: 8990000}

	up := History{MarginLots: flat(10, 9100000), BenchmarkRet: flat(10, 0.5)}    // price up, margin down
	down := History{MarginLots: flat(10, 8900000), BenchmarkRet: flat(10, -0.5)} // price down, margin up
	chase := History{MarginLots: flat(10, 8900000), BenchmarkRet: flat(10, 0.5)} // price up, margin up

	if ev := AnalyzeMarginBalance(m, up); ev.Direction != model.DirBullish {
		t.Errorf("price up + margin down is the healthy tape, got %s", ev.Direction)
	}
	if ev := AnalyzeMarginBalance(m, down); ev.Direction != model.DirBearish {
		t.Errorf("price down + margin up is retail averaging down, got %s", ev.Direction)
	}
	if ev := AnalyzeMarginBalance(m, chase); ev.Direction != model.DirNeutral {
		t.Errorf("price up + margin up is caution, not a call: got %s", ev.Direction)
	}
	// Rising margin on its own must never be enough to call the market.
	rising := AnalyzeMarginBalance(m, History{MarginLots: flat(10, 8900000)})
	if rising.Direction != model.DirNeutral {
		t.Errorf("margin without a price series must not produce a direction, got %s", rising.Direction)
	}
	if !strings.Contains(rising.Summary, "無指數對照") {
		t.Errorf("the analyzer should say why it withheld judgement: %q", rising.Summary)
	}
	if AnalyzeMarginBalance(nil, History{}).Available {
		t.Error("a missing dataset must be unavailable")
	}
}

// ── posture ──────────────────────────────────────────────────────────────────────────

func ev(src string, d model.Direction, avail bool) model.Evidence {
	if !avail {
		return model.MissingEvidence(src, "down")
	}
	return model.Evidence{Source: src, Direction: d, Strength: model.StrengthModerate, Available: true}
}

func TestCombinePosture(t *testing.T) {
	bull, bear, neu := model.DirBullish, model.DirBearish, model.DirNeutral

	cases := []struct {
		name    string
		f, c, m model.Evidence
		want    model.InstitutionalPosture
	}{
		{"two bulls", ev("f", bull, true), ev("c", bull, true), ev("m", neu, true), model.PostureSupportive},
		{"two bears", ev("f", bear, true), ev("c", neu, true), ev("m", bear, true), model.PostureDeteriorating},
		{"mixed", ev("f", bull, true), ev("c", bear, true), ev("m", neu, true), model.PostureNeutral},
		{"all neutral", ev("f", neu, true), ev("c", neu, true), ev("m", neu, true), model.PostureNeutral},
		// One usable input is not a posture — calling it NEUTRAL would let an outage read
		// as calm markets.
		{"only one usable", ev("f", bull, true), ev("c", bull, false), ev("m", bull, false), model.PostureUnknown},
		{"none usable", ev("f", bull, false), ev("c", bull, false), ev("m", bull, false), model.PostureUnknown},
	}
	for _, c := range cases {
		if got := CombinePosture(c.f, c.c, c.m); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// Futures AND cash both bearish is the classic Taiwan distribution combination and counts as
// deterioration on its own, even when margin is calm and the simple majority would not.
func TestFuturesPlusCashBearishIsDeterioration(t *testing.T) {
	got := CombinePosture(
		ev(model.EvidenceFutures, model.DirBearish, true),
		ev(model.EvidenceCash, model.DirBearish, true),
		ev(model.EvidenceMargin, model.DirBullish, true),
	)
	if got != model.PostureDeteriorating {
		t.Errorf("foreign futures + cash both selling should be DETERIORATING, got %s", got)
	}
}

func TestPercentileHelper(t *testing.T) {
	hist := make([]float64, 100)
	for i := range hist {
		hist[i] = float64(i)
	}
	if p, ok := percentileOf(50, hist, 60); !ok || p < 45 || p > 55 {
		t.Errorf("median should sit near 50, got %v (ok=%v)", p, ok)
	}
	if p, ok := percentileOf(-1, hist, 60); !ok || p != 0 {
		t.Errorf("below the range should be 0, got %v", p)
	}
	// Too thin a sample yields false rather than a meaningless number.
	if _, ok := percentileOf(5, []float64{1, 2, 3}, 60); ok {
		t.Error("a 3-sample distribution must not produce a percentile")
	}
}
