package analyzer

import (
	"fmt"
	"math"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// The three institutional analyzers and the posture they combine into (layer 2b).
//
// All three are pure functions over a raw observation plus whatever history the caller could
// supply. History is optional everywhere: a missing window lowers the evidence's confidence
// and drops the metric, it never invents a value. That is the difference between "we do not
// know how crowded this is" and "this is not crowded".
//
// None of them decides a regime. They emit Evidence; DecideRegime reads the posture those
// evidences combine into, and even then only as a refinement — R1 shows that a bearish
// structure ignores institutions entirely.

// History is the trailing series each analyzer needs for context, oldest-first and ending on
// the session BEFORE the one being analyzed. Any of them may be empty.
type History struct {
	FuturesNetOI []float64 // foreign net open interest, contracts
	ForeignNet   []float64 // foreign cash net, NTD
	MarginLots   []float64 // margin balance, 交易單位
	BenchmarkRet []float64 // benchmark daily percent returns, aligned with MarginLots
}

// ── Foreign futures ──────────────────────────────────────────────────────────────────

// AnalyzeForeignFutures reads foreign index-futures positioning.
//
// The design constraint that matters: a large net short is NOT automatically extreme bearish.
// Three independent facts are kept apart, because they routinely disagree —
//
//	POSITION  where the book sits right now
//	CHANGE    which way it moved (adding shorts vs covering them)
//	CROWDING  how extreme it is against its own recent history
//
// A book that is deeply short, historically crowded, and covering is bearish in position,
// stretched in crowding, and improving in change — three true statements at once. Collapsing
// them into one number is what produces the "foreigners are max short, therefore crash"
// reading that this analyzer exists to avoid. Crowding therefore DAMPENS strength and adds a
// squeeze caveat rather than amplifying the bearish call.
func AnalyzeForeignFutures(f *model.FuturesOIData, h History, th model.FuturesThresholds) model.Evidence {
	if f == nil {
		return model.MissingEvidence(model.EvidenceFutures, "TAIFEX 未平倉資料缺漏")
	}
	th = th.Defaulted()

	metrics := map[string]float64{
		model.MetricFutNetOI:   round2(f.NetOI),
		model.MetricFutLongOI:  round2(f.LongOI),
		model.MetricFutShortOI: round2(f.ShortOI),
	}

	// CHANGE — needs at least one prior session.
	var chg1, chg5 float64
	var haveChg bool
	if n := len(h.FuturesNetOI); n >= 1 {
		chg1 = f.NetOI - h.FuturesNetOI[n-1]
		metrics[model.MetricFutChg1D] = round2(chg1)
		haveChg = true
	}
	if n := len(h.FuturesNetOI); n >= 5 {
		chg5 = f.NetOI - h.FuturesNetOI[n-5]
		metrics[model.MetricFutChg5D] = round2(chg5)
	}

	// CROWDING — needs a distribution to sit inside.
	pct, haveCrowd := percentileOf(f.NetOI, h.FuturesNetOI, 60)
	if haveCrowd {
		metrics[model.MetricFutPercentile] = round2(pct)
	}

	dir, strength := futuresDirection(f, chg1, haveChg, th)

	// Crowding damps conviction instead of reinforcing it: an extreme is a stretched rubber
	// band, not a stronger signal.
	var caveat string
	if haveCrowd && dir == model.DirBearish && pct <= 10 {
		strength = weaken(strength)
		caveat = "；部位處於近期最空的 10%，具軋空風險"
	}
	if haveCrowd && dir == model.DirBullish && pct >= 90 {
		strength = weaken(strength)
		caveat = "；部位處於近期最多的 10%，追價空間有限"
	}

	conf := 0.5
	if haveChg {
		conf += 0.25
	}
	if haveCrowd {
		conf += 0.25
	}

	return model.Evidence{
		Source: model.EvidenceFutures, Direction: dir, Strength: strength,
		Confidence: round2(conf),
		Summary: fmt.Sprintf("外資期貨淨%s %.0f 口%s%s%s",
			sideWord(f.NetOI), math.Abs(f.NetOI),
			changePhrase(chg1, haveChg), crowdPhrase(pct, haveCrowd), caveat),
		Metrics: metrics, Available: true,
	}
}

// futuresDirection reads POSITION for the sign and CHANGE for the strength. Change is
// weighted deliberately: what foreigners did today is more informative about tomorrow than
// where their book happens to sit.
func futuresDirection(f *model.FuturesOIData, chg float64, haveChg bool, th model.FuturesThresholds) (model.Direction, model.Strength) {
	dir := model.DirNeutral
	switch {
	case f.NetOI <= th.ShortNet:
		dir = model.DirBearish
	case f.NetOI >= -th.ShortNet:
		dir = model.DirBullish
	case f.NetOI < 0:
		dir = model.DirBearish
	case f.NetOI > 0:
		dir = model.DirBullish
	}
	if !haveChg {
		return dir, model.StrengthWeak
	}
	// A book moving AGAINST its own position is a softening signal, not a confirming one.
	switch {
	case chg <= th.AggressiveShortChange:
		if dir == model.DirBearish {
			return dir, model.StrengthStrong
		}
		return model.DirBearish, model.StrengthModerate
	case chg >= th.CoverShortChange:
		if dir == model.DirBullish {
			return dir, model.StrengthStrong
		}
		return model.DirBullish, model.StrengthModerate
	case chg <= th.MoreShortChange:
		return dir, pickStrength(dir == model.DirBearish)
	case chg >= th.StableChange:
		return dir, pickStrength(dir == model.DirBullish)
	}
	return dir, model.StrengthWeak
}

func pickStrength(aligned bool) model.Strength {
	if aligned {
		return model.StrengthModerate
	}
	return model.StrengthWeak
}

func weaken(s model.Strength) model.Strength {
	switch s {
	case model.StrengthExtreme:
		return model.StrengthStrong
	case model.StrengthStrong:
		return model.StrengthModerate
	case model.StrengthModerate:
		return model.StrengthWeak
	}
	return s
}

// ── Foreign cash ─────────────────────────────────────────────────────────────────────

// AnalyzeForeignCash reads market-level foreign cash flow over rolling windows.
//
// One day is noise. The verdict is driven by the 5- and 20-day sums where they exist, with
// today used only to note a turn: a -407 億 session inside a +1,100 億 twenty-day run is a
// pause, not a reversal, and calling it bearish would be the single most common way to
// misread this series.
func AnalyzeForeignCash(c *model.CashData, h History) model.Evidence {
	if c == nil {
		return model.MissingEvidence(model.EvidenceCash, "TWSE 三大法人買賣金額缺漏")
	}
	const oku = 1e8 // 億

	metrics := map[string]float64{model.MetricCashNet1D: round2(c.ForeignNet / oku)}
	sum5, have5 := trailingSum(h.ForeignNet, 4, c.ForeignNet)
	sum20, have20 := trailingSum(h.ForeignNet, 19, c.ForeignNet)
	if have5 {
		metrics[model.MetricCashNet5D] = round2(sum5 / oku)
	}
	if have20 {
		metrics[model.MetricCashNet20D] = round2(sum20 / oku)
	}

	dir, strength := model.DirNeutral, model.StrengthWeak
	switch {
	case have20 && have5:
		// Both windows agreeing is the strongest reading available.
		switch {
		case sum5 > 0 && sum20 > 0:
			dir, strength = model.DirBullish, model.StrengthStrong
		case sum5 < 0 && sum20 < 0:
			dir, strength = model.DirBearish, model.StrengthStrong
		case sum20 > 0:
			dir, strength = model.DirBullish, model.StrengthWeak // long-run buying, short-run pause
		case sum20 < 0:
			dir, strength = model.DirBearish, model.StrengthWeak
		}
	case have5:
		if sum5 > 0 {
			dir, strength = model.DirBullish, model.StrengthModerate
		} else if sum5 < 0 {
			dir, strength = model.DirBearish, model.StrengthModerate
		}
	default:
		// Only today. Report it, but at the lowest conviction the vocabulary allows.
		if c.ForeignNet > 0 {
			dir = model.DirBullish
		} else if c.ForeignNet < 0 {
			dir = model.DirBearish
		}
	}

	conf := 0.4
	if have5 {
		conf += 0.3
	}
	if have20 {
		conf += 0.3
	}

	return model.Evidence{
		Source: model.EvidenceCash, Direction: dir, Strength: strength,
		Confidence: round2(conf),
		Summary: fmt.Sprintf("外資現貨 今日 %+.0f 億%s%s",
			c.ForeignNet/oku, windowPhrase("5日", sum5/oku, have5), windowPhrase("20日", sum20/oku, have20)),
		Metrics: metrics, Available: true,
	}
}

// ── Margin ───────────────────────────────────────────────────────────────────────────

// AnalyzeMargin reads retail leverage IN THE CONTEXT of what price did.
//
// Rising margin is not universally bearish — in an advance it is ordinary participation. The
// signal is in the COMBINATION: price up while margin falls is the healthiest tape (the move
// is not retail-driven); price down while margin rises is the worst (retail averaging into a
// decline). The 5-day window is the primary read because a single session of either series
// is noise.
func AnalyzeMarginBalance(m *model.MarginBalanceData, h History) model.Evidence {
	if m == nil {
		return model.MissingEvidence(model.EvidenceMargin, "TWSE 信用交易統計缺漏")
	}
	metrics := map[string]float64{
		model.MetricMarginBalance: round2(m.MarginBalanceLots),
		model.MetricMarginChg1D:   round2(m.MarginChangeLots()),
		model.MetricShortBalance:  round2(m.ShortBalanceLots),
		model.MetricShortChg1D:    round2(m.ShortChangeLots()),
	}

	marginChg, haveMargin := 0.0, false
	if n := len(h.MarginLots); n >= 5 {
		marginChg = m.MarginBalanceLots - h.MarginLots[n-5]
		metrics[model.MetricMarginChg5D] = round2(marginChg)
		haveMargin = true
	} else {
		marginChg = m.MarginChangeLots()
	}

	priceChg, havePrice := 0.0, false
	if n := len(h.BenchmarkRet); n >= 5 {
		for _, r := range h.BenchmarkRet[n-5:] {
			priceChg += r
		}
		metrics[model.MetricBenchRet5D] = round2(priceChg)
		havePrice = true
	}

	// Without a price series there is no context, and margin alone means nothing. Say so
	// rather than guess a direction from the balance moving.
	if !havePrice {
		return model.Evidence{
			Source: model.EvidenceMargin, Direction: model.DirNeutral, Strength: model.StrengthWeak,
			Confidence: 0.3,
			Summary:    fmt.Sprintf("融資餘額 %.0f（日變化 %+.0f）；無指數對照，不做方向判讀", m.MarginBalanceLots, m.MarginChangeLots()),
			Metrics:    metrics, Available: true,
		}
	}

	dir, strength, phrase := marginRead(priceChg, marginChg)
	conf := 0.5
	if haveMargin {
		conf += 0.3
	}
	return model.Evidence{
		Source: model.EvidenceMargin, Direction: dir, Strength: strength,
		Confidence: round2(conf),
		Summary: fmt.Sprintf("近5日 指數 %+.1f%%、融資 %+.0f 交易單位——%s",
			priceChg, marginChg, phrase),
		Metrics: metrics, Available: true,
	}
}

func marginRead(priceChg, marginChg float64) (model.Direction, model.Strength, string) {
	switch {
	case priceChg > 0 && marginChg < 0:
		return model.DirBullish, model.StrengthModerate, "上漲但融資減少，籌碼健康、非散戶推動"
	case priceChg < 0 && marginChg > 0:
		return model.DirBearish, model.StrengthModerate, "下跌但融資增加，散戶攤平"
	case priceChg > 0 && marginChg > 0:
		return model.DirNeutral, model.StrengthWeak, "上漲且融資增加，追價需留意"
	case priceChg < 0 && marginChg < 0:
		return model.DirNeutral, model.StrengthWeak, "下跌且融資減少，去槓桿"
	}
	return model.DirNeutral, model.StrengthWeak, "無明顯組合"
}

// ── Posture ──────────────────────────────────────────────────────────────────────────

// CombinePosture folds the three institutional evidences into layer 2b.
//
// UNKNOWN below two usable inputs: with a single data point there is no posture, and calling
// that NEUTRAL would let one outage read as calm markets — the same MISSING-is-not-NEUTRAL
// rule the rest of the feature runs on.
func CombinePosture(futures, cash, margin model.Evidence) model.InstitutionalPosture {
	usable := model.UsableEvidence([]model.Evidence{futures, cash, margin})
	if len(usable) < 2 {
		return model.PostureUnknown
	}
	var bull, bear int
	for _, e := range usable {
		switch e.Direction {
		case model.DirBullish:
			bull++
		case model.DirBearish:
			bear++
		}
	}
	// The classic Taiwan distribution combination: foreigners selling futures AND cash at
	// the same time counts as deterioration on its own, even when margin is calm.
	futBear := futures.Usable() && futures.Direction == model.DirBearish
	cashBear := cash.Usable() && cash.Direction == model.DirBearish
	if futBear && cashBear {
		return model.PostureDeteriorating
	}
	switch {
	case bear >= 2:
		return model.PostureDeteriorating
	case bull >= 2 && bear == 0:
		return model.PostureSupportive
	}
	return model.PostureNeutral
}

// ── helpers ──────────────────────────────────────────────────────────────────────────

// percentileOf returns where x sits inside hist (0..100) and whether the distribution was
// large enough to mean anything. Too thin a sample yields false — an unknown percentile must
// not masquerade as percentile 0.
func percentileOf(x float64, hist []float64, min int) (float64, bool) {
	if len(hist) < min {
		return 0, false
	}
	sorted := append([]float64(nil), hist...)
	sort.Float64s(sorted)
	below := sort.SearchFloat64s(sorted, x)
	return float64(below) / float64(len(sorted)) * 100, true
}

// trailingSum adds today to the last n history entries, reporting false when history is short.
func trailingSum(hist []float64, n int, today float64) (float64, bool) {
	if len(hist) < n {
		return 0, false
	}
	sum := today
	for _, v := range hist[len(hist)-n:] {
		sum += v
	}
	return sum, true
}

func sideWord(v float64) string {
	if v < 0 {
		return "空"
	}
	return "多"
}

func changePhrase(chg float64, have bool) string {
	if !have {
		return ""
	}
	if chg == 0 {
		return "，日變化持平"
	}
	verb := "增空"
	if chg > 0 {
		verb = "回補"
	}
	return fmt.Sprintf("，單日%s %.0f 口", verb, math.Abs(chg))
}

func crowdPhrase(pct float64, have bool) string {
	if !have {
		return ""
	}
	return fmt.Sprintf("（近期第 %.0f 百分位）", pct)
}

func windowPhrase(label string, v float64, have bool) string {
	if !have {
		return ""
	}
	return fmt.Sprintf("、%s %+.0f 億", label, v)
}
