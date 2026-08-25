package r6backtest

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/fx"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
)

// WriteFXMarkdown renders the market-context study.
//
// The header is not decoration. Every row below is a difference from a baseline over a single
// regime with overlapping forward windows, and a reader who takes a win rate at face value
// will over-trust it — so the caveats are printed above the numbers, not after them.
func WriteFXMarkdown(path string, panel *FXPanel, stats []FXStat, horizons []int,
	series *fx.Series, flow *provider.ForeignFlowSeries) error {

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder

	b.WriteString("# USD/TWD × Foreign Flow — Market Context Study\n\n")
	b.WriteString("> 研究輸出，非交易指令。所有數字皆為 shadow-only，未進入任何 scoring。\n\n")

	b.WriteString("## Sample framing\n\n")
	fmt.Fprintf(&b, "- **trading dates (the sample size): %d**\n", len(panel.Days))
	fmt.Fprintf(&b, "- stock observations the per-stock framing would have counted: %d\n", panel.StockObservations)
	b.WriteString("- USD/TWD、外資買賣超與大盤都是**每日一個觀測值**，由全 universe 共用。\n")
	b.WriteString("  把同一天的每檔股票各算一個樣本，會把 N 灌大約三個數量級 —— 本研究一律以\n")
	b.WriteString("  **交易日**為觀測單位，股票數只作為對照。\n")
	if len(panel.Days) > 0 {
		fmt.Fprintf(&b, "- coverage: %s → %s\n", panel.Days[0].Date, panel.Days[len(panel.Days)-1].Date)
	}
	if series != nil && len(series.Bars) > 0 {
		fmt.Fprintf(&b, "- FX archive: %d bars, %s → %s\n",
			len(series.Bars), series.Bars[0].Date, series.Bars[len(series.Bars)-1].Date)
	}
	if flow != nil && len(flow.Points) > 0 {
		fmt.Fprintf(&b, "- foreign-flow archive: %d dates, %s → %s (%s)\n",
			len(flow.Points), flow.Points[0].Date, flow.Points[len(flow.Points)-1].Date, flow.Source)
	} else {
		b.WriteString("- **foreign-flow archive: ABSENT — every interaction row is empty**\n")
	}
	b.WriteString("\n")

	b.WriteString("## Causality\n\n")
	b.WriteString("- FX bar D 收在倫敦 23:59 = 台北 D+1 06:59，**台股 D 日 13:30 收盤時尚未形成**。\n")
	b.WriteString("  故每個台灣交易日只使用 **嚴格早於該日** 的 FX bar。\n")
	b.WriteString("- BFI82U 於當日約 15:00 公布，掃描器收盤後執行時已可取得，故外資使用當日值。\n")
	b.WriteString("- 前瞻報酬為 universe 各股 **中位數**（掃描器選的是個股，指數會換成另一個問題）。\n\n")

	b.WriteString("## Caveats\n\n")
	b.WriteString("- **統計信心未建立**：單一 regime、前瞻視窗重疊、未做顯著性檢定。\n")
	b.WriteString("- universe 有倖存者偏誤，絕對報酬對每一列同樣偏樂觀 —— **只讀與 baseline 的差**。\n")
	b.WriteString("- threshold 皆為未驗證起點，且**未依本結果調整過**。\n\n")

	b.WriteString("## Results\n\n")
	b.WriteString("**Effective N 才是樣本數。** Raw 是重疊觀測值的計數，僅供對照 —— 20 日\n")
	b.WriteString("前瞻視窗連續每天取樣，等於把同一段 20 天數了二十次。\n")
	b.WriteString("CI 為 95% bootstrap，在非重疊樣本上重抽（blocking 已由建構完成）。\n\n")

	for _, h := range horizons {
		fmt.Fprintf(&b, "### %dd forward\n\n", h)
		b.WriteString("| condition | raw n | **eff n** | win% | 95% CI | median | 95% CI | raw win% |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|\n")
		for _, st := range stats {
			eff := st.EffectiveN[h]
			if eff == 0 {
				fmt.Fprintf(&b, "| %s | %d | 0 | — | — | — | — | — |\n", st.Name, st.RawDates)
				continue
			}
			ci := "—"
			if !math.IsNaN(st.WinRateLo[h]) {
				ci = fmt.Sprintf("%.0f–%.0f%%", st.WinRateLo[h], st.WinRateHi[h])
			}
			mci := "—"
			if !math.IsNaN(st.MedianLo[h]) {
				mci = fmt.Sprintf("%+.2f–%+.2f%%", st.MedianLo[h], st.MedianHi[h])
			}
			fmt.Fprintf(&b, "| %s | %d | **%d** | %.1f%% | %s | %+.2f%% | %s | %.1f%% |\n",
				st.Name, st.RawDates, eff, st.WinRate[h], ci, st.Median[h], mci, st.RawWinRate[h])
		}
		b.WriteString("\n")
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}
