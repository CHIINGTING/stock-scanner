package validator

import (
	"fmt"
	"math"
	"sort"
)

// Evaluator turns parsed snapshots into validated results by joining them with
// forward prices and applying the BUY / REDUCE verdict rules from the design.
//
// Verdict rules (benchmark-aware; absolute fallback when no benchmark):
//
//	BUY_GROUP  CORRECT: shortRet>0 & shortEx>=0   OR longRet>0 & longEx>=0
//	           WRONG:   shortRet<=-3%             OR longRet<=-5%
//	REDUCE_GRP CORRECT: shortRet<0 & shortEx<=0   OR longRet<0 & longEx<=0  OR maxDD<=-5%
//	           WRONG:   shortRet>=5% & shortEx>=3% OR longRet>=8%
//
// "short" is the T+5 horizon (falls back to T+3), "long" is T+10 (falls back to
// T+20). WATCH_GROUP is measured but never counted toward hit rate.
type Evaluator struct {
	Store     *PriceStore
	Benchmark *Benchmark
	Horizons  []int
}

// CountsTowardHitRate reports whether a group participates in the headline hit rate.
func (g ActionGroup) CountsTowardHitRate() bool {
	return g == BuyGroup || g == ReduceGroup
}

// EvaluateAll evaluates every snapshot, preserving input order.
func (e *Evaluator) EvaluateAll(snaps []SignalSnapshot) []ValidationResult {
	out := make([]ValidationResult, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, e.Evaluate(s))
	}
	return out
}

// Evaluate joins one snapshot with prices and assigns a verdict.
func (e *Evaluator) Evaluate(snap SignalSnapshot) ValidationResult {
	res := ValidationResult{
		Signal:          snap,
		ReturnByHorizon: map[int]float64{},
		ExcessByHorizon: map[int]float64{},
	}

	series := e.Store.Series(snap.Code)
	if series == nil {
		res.Result = ResultNoPriceData
		res.Reason = "找不到該股票的價格資料（cache 無對應檔案）"
		return res
	}
	outcome, ok := series.Outcome(snap.SignalDate, e.Horizons)
	if !ok {
		res.Result = ResultNoPriceData
		res.Reason = "無法定位訊號日或缺少隔日進場價"
		return res
	}

	res.Signal.SignalPrice = outcome.SignalPrice
	res.EntryDate = outcome.EntryDate
	res.EntryPrice = outcome.EntryPrice
	res.ReturnByHorizon = outcome.Returns
	res.MaxDrawdown = outcome.MaxDrawdown
	res.MaxRunup = outcome.MaxRunup

	benchReturns := e.Benchmark.Returns(snap.SignalDate, e.Horizons)
	for h, r := range outcome.Returns {
		if br, okB := benchReturns[h]; okB {
			res.ExcessByHorizon[h] = r - br
		} else {
			res.ExcessByHorizon[h] = math.NaN()
		}
	}

	if len(outcome.Returns) == 0 {
		res.Result = ResultPending
		res.Reason = "後續交易日不足，尚無法驗證"
		return res
	}

	shortRet, shortEx, hasShort := pickHorizon(res, []int{5, 3, 4, 2, 1})
	longRet, longEx, hasLong := pickHorizon(res, []int{10, 20, 15, 8})
	benchOn := e.Benchmark.Enabled()

	switch snap.ActionGroup {
	case BuyGroup:
		e.evalBuy(&res, shortRet, shortEx, hasShort, longRet, longEx, hasLong, benchOn)
	case ReduceGroup:
		e.evalReduce(&res, shortRet, shortEx, hasShort, longRet, longEx, hasLong, outcome.MaxDrawdown, benchOn)
	case EntryCautionGroup:
		e.evalEntryCaution(&res, shortRet, hasShort, longRet, hasLong, outcome.MaxDrawdown)
	default: // WATCH_GROUP / UNKNOWN — measured, not scored
		res.Result = ResultNeutral
		res.Reason = "觀察類訊號，僅追蹤不計入命中率"
	}
	return res
}

func (e *Evaluator) evalBuy(res *ValidationResult, sr, se float64, hasS bool, lr, le float64, hasL bool, benchOn bool) {
	correct := (hasS && sr > 0 && (!benchOn || se >= 0)) ||
		(hasL && lr > 0 && (!benchOn || le >= 0))
	wrong := (hasS && sr <= -3) || (hasL && lr <= -5)

	switch {
	case correct:
		res.Result = ResultCorrect
		res.Reason = fmt.Sprintf("加買後上漲（T+5 %s，T+10 %s）%s", fmtPctPtr(sr, hasS), fmtPctPtr(lr, hasL), benchNote(benchOn, true))
	case wrong:
		res.Result = ResultWrong
		res.Reason = fmt.Sprintf("加買後下跌（T+5 %s，T+10 %s），可能追高或短線漲幅已過", fmtPctPtr(sr, hasS), fmtPctPtr(lr, hasL))
	case hasS || hasL:
		res.Result = ResultNeutral
		res.Reason = "加買後無明顯漲跌，方向未定"
	default:
		res.Result = ResultPending
		res.Reason = "後續交易日不足，尚無法驗證"
	}
}

func (e *Evaluator) evalReduce(res *ValidationResult, sr, se float64, hasS bool, lr, le float64, hasL bool, maxDD float64, benchOn bool) {
	correct := (hasS && sr < 0 && (!benchOn || se <= 0)) ||
		(hasL && lr < 0 && (!benchOn || le <= 0)) ||
		maxDD <= -5
	wrong := (hasS && sr >= 5 && (!benchOn || se >= 3)) || (hasL && lr >= 8)

	switch {
	case wrong:
		res.Result = ResultWrong
		res.Reason = fmt.Sprintf("減碼後續漲（T+5 %s，T+10 %s），可能洗盤誤判或主升段中途震盪", fmtPctPtr(sr, hasS), fmtPctPtr(lr, hasL))
	case correct:
		res.Result = ResultCorrect
		res.Reason = fmt.Sprintf("減碼後走弱（T+5 %s，T+10 %s，最大跌幅 %.1f%%）%s", fmtPctPtr(sr, hasS), fmtPctPtr(lr, hasL), maxDD, benchNote(benchOn, false))
	case hasS || hasL:
		res.Result = ResultNeutral
		res.Reason = "減碼後無明顯漲跌，方向未定"
	default:
		res.Result = ResultPending
		res.Reason = "後續交易日不足，尚無法驗證"
	}
}

// evalEntryCaution judges an OVERHEATED "太熱不要追" watchlist warning on its own
// terms. The warning does NOT predict a decline — a momentum name can stay
// overheated and keep rising — so it is graded on whether standing aside actually
// paid off:
//
//	CORRECT: a pullback (maxDD<=-5%) appeared → a cheaper/safer entry than chasing,
//	         or the stock stalled/fell short-term (chasing would have been red).
//	WRONG:   no pullback (maxDD>-5%) yet it ran away (long>=12% or short>=15%) →
//	         the caution cost the user the main run; the OVERHEATED bar was too tight.
//	NEUTRAL: mild chop, chasing-or-not made little difference.
func (e *Evaluator) evalEntryCaution(res *ValidationResult, sr float64, hasS bool, lr float64, hasL bool, maxDD float64) {
	pullback := maxDD <= -5
	ranAway := (hasL && lr >= 12) || (hasS && sr >= 15)
	stalled := (hasS && sr <= 0) || (hasL && lr <= 0)

	switch {
	case !pullback && ranAway:
		res.Result = ResultWrong
		res.Reason = fmt.Sprintf("過熱警告後未回檔即噴出（T+5 %s，T+10 %s，最大回檔僅 %.1f%%），追高其實可賺，OVERHEATED 門檻過嚴", fmtPctPtr(sr, hasS), fmtPctPtr(lr, hasL), maxDD)
	case pullback:
		res.Result = ResultCorrect
		res.Reason = fmt.Sprintf("過熱後出現回檔（最大回檔 %.1f%%），提供更佳進場點、避開追高套牢", maxDD)
	case stalled:
		res.Result = ResultCorrect
		res.Reason = fmt.Sprintf("過熱後短線走弱（T+5 %s，T+10 %s），避開追高正確", fmtPctPtr(sr, hasS), fmtPctPtr(lr, hasL))
	case hasS || hasL:
		res.Result = ResultNeutral
		res.Reason = "過熱後小幅震盪，追高與否影響不大"
	default:
		res.Result = ResultPending
		res.Reason = "後續交易日不足，尚無法驗證"
	}
}

// pickHorizon returns the return and excess for the first horizon in prefs that
// has price data, plus whether one was found.
func pickHorizon(res ValidationResult, prefs []int) (ret, excess float64, ok bool) {
	for _, h := range prefs {
		if r, has := res.ReturnByHorizon[h]; has {
			return r, res.ExcessByHorizon[h], true
		}
	}
	return 0, 0, false
}

func fmtPctPtr(v float64, ok bool) string {
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%+.1f%%", v)
}

func benchNote(benchOn, bullish bool) string {
	if !benchOn {
		return "（絕對報酬）"
	}
	if bullish {
		return "且不弱於大盤"
	}
	return "且弱於大盤"
}

// SortedHorizons returns the requested horizons in ascending order (for stable
// table columns and CSV headers).
func SortedHorizons(hs []int) []int {
	out := append([]int(nil), hs...)
	sort.Ints(out)
	return out
}
