package r6backtest

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// csvHeader is the FIXED per-trade schema (keep in sync with Trade + csvRow).
var csvHeader = []string{
	"setup_name", "stock_code", "stock_name", "is_watchlist_member",
	"entry_date", "entry_price", "signal_date", "signal_close",
	"return_5d", "return_10d", "return_20d", "return_60d",
	"hold_return_5d", "hold_return_10d", "hold_return_20d", "hold_return_60d",
	"max_drawdown_after_entry", "hit_stop", "stop_reason", "stop_date", "stop_price",
	"rs_rank_at_entry", "distance_from_52w_high", "pullback_pct_from_recent_high",
	"ma20_distance_pct", "ma60_distance_pct",
	"vcp_valid", "momentum_flow", "mtf_signal", "sector", "pullback_bucket",
}

// forbiddenTokens must never appear in any R6 output (decision-support only,
// not a trading bot). Enforced by a test.
var forbiddenTokens = []string{"BUY", "AUTO_BUY", "PLACE_ORDER"}

func f(x float64) string {
	if math.IsNaN(x) {
		return ""
	}
	return strconv.FormatFloat(x, 'f', 2, 64)
}

func dateOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func csvRow(t Trade) []string {
	return []string{
		t.SetupName, t.StockCode, t.StockName, strconv.FormatBool(t.IsWatchlistMember),
		t.EntryDate.Format("2006-01-02"), f(t.EntryPrice),
		dateOrEmpty(t.SignalDate), f(t.SignalClose),
		f(t.Return5d), f(t.Return10d), f(t.Return20d), f(t.Return60d),
		f(t.HoldReturn5d), f(t.HoldReturn10d), f(t.HoldReturn20d), f(t.HoldReturn60d),
		f(t.MaxDrawdownAfterEntry), strconv.FormatBool(t.HitStop), t.StopReason,
		dateOrEmpty(t.StopDate), f(t.StopPrice),
		f(t.RSRankAtEntry), f(t.DistanceFrom52wHigh), f(t.PullbackPctFromHigh),
		f(t.MA20DistancePct), f(t.MA60DistancePct),
		strconv.FormatBool(t.VCPValid), t.MomentumFlow, t.MTFSignal, t.Sector,
		strconv.Itoa(t.Bucket),
	}
}

// WriteCSV writes the trades (header always written, even with zero rows so the
// schema is inspectable).
func WriteCSV(path string, trades []Trade) error {
	var b strings.Builder
	b.WriteString(strings.Join(csvHeader, ",") + "\n")
	for _, t := range trades {
		row := csvRow(t)
		for i, c := range row {
			if strings.ContainsAny(c, ",\"\n") {
				c = "\"" + strings.ReplaceAll(c, "\"", "\"\"") + "\""
			}
			row[i] = c
		}
		b.WriteString(strings.Join(row, ",") + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// CSVHeader exposes the fixed schema (for tests / external inspection).
func CSVHeader() []string { return append([]string(nil), csvHeader...) }

// ── statistics ──────────────────────────────────────────────────────────────

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}

// percentile returns the p-th percentile (0..100) using nearest-rank.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	rank := int(math.Ceil(p/100*float64(len(c)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(c) {
		rank = len(c) - 1
	}
	return c[rank]
}

// ComputeStats aggregates one group of trades (already filtered to a setup/bucket).
// confidence follows the scanner convention but is pinned to LOW when
// p.ForceLowConfidence is set (Setup D: one real crash event only).
func ComputeStats(name string, bucket int, trades []Trade, horizons []int, p Params) SetupStat {
	st := SetupStat{
		SetupName: name, Bucket: bucket, SampleCount: len(trades),
		WinRate: map[int]float64{}, AvgReturn: map[int]float64{}, MedianReturn: map[int]float64{},
		HoldWinRate: map[int]float64{}, HoldAvgReturn: map[int]float64{}, StopDelta: map[int]float64{},
	}
	stopAdjOf := func(t Trade, h int) float64 {
		switch h {
		case 5:
			return t.Return5d
		case 10:
			return t.Return10d
		case 20:
			return t.Return20d
		case 60:
			return t.Return60d
		}
		return math.NaN()
	}
	holdOf := func(t Trade, h int) float64 {
		switch h {
		case 5:
			return t.HoldReturn5d
		case 10:
			return t.HoldReturn10d
		case 20:
			return t.HoldReturn20d
		case 60:
			return t.HoldReturn60d
		}
		return math.NaN()
	}
	winAvgMed := func(get func(Trade, int) float64, h int) (win, avg, med float64) {
		var rs []float64
		wins := 0
		for _, t := range trades {
			r := get(t, h)
			if math.IsNaN(r) {
				continue
			}
			rs = append(rs, r)
			if r > 0 {
				wins++
			}
		}
		if len(rs) == 0 {
			return math.NaN(), math.NaN(), math.NaN()
		}
		return float64(wins) / float64(len(rs)) * 100, mean(rs), median(rs)
	}
	for _, h := range horizons {
		w, a, m := winAvgMed(stopAdjOf, h) // MAIN: stop-adjusted
		st.WinRate[h], st.AvgReturn[h], st.MedianReturn[h] = w, a, m
		hw, ha, _ := winAvgMed(holdOf, h) // COMPARISON: hold-to-horizon
		st.HoldWinRate[h], st.HoldAvgReturn[h] = hw, ha
		st.StopDelta[h] = a - ha // positive = stop helped
	}
	var dds []float64
	stops := 0
	for _, t := range trades {
		dds = append(dds, t.MaxDrawdownAfterEntry)
		if t.HitStop {
			stops++
		}
	}
	st.MaxDrawdownAvg = mean(dds)
	st.MaxDrawdownP90 = percentile(dds, 10) // 10th pct of signed dd = worst decile
	if len(trades) > 0 {
		st.StopHitRate = float64(stops) / float64(len(trades)) * 100
	}
	st.Confidence = confidenceFor(len(trades), p.ForceLowConfidence)
	st.BestCases, st.WorstCases = bestWorst(trades, 20, 5)
	return st
}

func confidenceFor(n int, forceLow bool) string {
	if forceLow {
		return "LOW"
	}
	switch {
	case n >= 30:
		return "HIGH"
	case n >= 12:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// bestWorst returns top/bottom symbols by the given horizon's return.
func bestWorst(trades []Trade, h, k int) (best, worst []string) {
	type pr struct {
		sym string
		r   float64
	}
	var prs []pr
	for _, t := range trades {
		r := t.Return20d // stop-adjusted
		if h == 5 {
			r = t.Return5d
		}
		if math.IsNaN(r) {
			continue
		}
		prs = append(prs, pr{t.StockCode, r})
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i].r > prs[j].r })
	for i := 0; i < len(prs) && i < k; i++ {
		best = append(best, fmt.Sprintf("%s(%.1f%%)", prs[i].sym, prs[i].r))
	}
	for i := len(prs) - 1; i >= 0 && len(worst) < k; i-- {
		worst = append(worst, fmt.Sprintf("%s(%.1f%%)", prs[i].sym, prs[i].r))
	}
	return
}

// WriteMarkdown renders the run summary. `meta` lines are printed verbatim at top
// (coverage, universe size, RS runtime, data-sufficiency caveats).
func WriteMarkdown(path, title string, meta []string, stats []SetupStat, horizons []int) error {
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	b.WriteString("> 回測結果為決策支援，僅供候選 / 勝率 / 風險 / 參考進場區之用，非買賣指令。\n\n")
	b.WriteString("> **主要統計採 stop-adjusted return**（horizon 前命中停損則以 stop price 計）。\n")
	b.WriteString("> **hold-to-horizon return 僅作為對照**（忽略停損、單純持有到期）。\n")
	b.WriteString("> stop_saved_or_hurt_delta = avg_stop_adjusted_return − avg_hold_return（正=停損保護，負=過早洗出）。\n\n")
	for _, m := range meta {
		b.WriteString("- " + m + "\n")
	}
	b.WriteString("\n")
	for _, s := range stats {
		head := s.SetupName
		if s.Bucket > 0 {
			head = fmt.Sprintf("%s — pullback %d%%", s.SetupName, s.Bucket)
		}
		b.WriteString("## " + head + "\n\n")
		b.WriteString(fmt.Sprintf("- sample_count: %d　confidence: %s　stop_hit_rate: %s\n",
			s.SampleCount, s.Confidence, pct(s.StopHitRate)))
		if s.EventCount > 0 || s.ProxySymbol != "" {
			b.WriteString(fmt.Sprintf("- event_count: %d　regime_date_range: %s　proxy_symbol: %s　market_proxy_return_20d: %.1f%%\n",
				s.EventCount, s.RegimeDateRange, s.ProxySymbol, s.MarketProxyReturn20d))
		}
		b.WriteString("\n| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")
		for _, h := range horizons {
			b.WriteString(fmt.Sprintf("| %dd | %s | %s | %s | %s | %s | %s |\n", h,
				pct(s.WinRate[h]), pct(s.AvgReturn[h]), pct(s.MedianReturn[h]),
				pct(s.HoldWinRate[h]), pct(s.HoldAvgReturn[h]), pct(s.StopDelta[h])))
		}
		b.WriteString(fmt.Sprintf("\n- max_drawdown_avg: %s　max_drawdown_p90: %s\n",
			pct(s.MaxDrawdownAvg), pct(s.MaxDrawdownP90)))
		if len(s.BestCases) > 0 {
			b.WriteString("- best_cases: " + strings.Join(s.BestCases, ", ") + "\n")
		}
		if len(s.WorstCases) > 0 {
			b.WriteString("- worst_cases: " + strings.Join(s.WorstCases, ", ") + "\n")
		}
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func pct(x float64) string {
	if math.IsNaN(x) {
		return "—"
	}
	return strconv.FormatFloat(x, 'f', 1, 64) + "%"
}
