package r6backtest

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// recentBullCSVHeader is the fixed R6-6 per-cell schema.
var recentBullCSVHeader = []string{
	"setup_name", "bucket", "stop_policy", "window",
	"signal_count", "matured_20d_count", "unmatured_20d_count", "available_20d", "status",
	"win_20d", "avg_return_20d_stopadj", "median_20d", "hold_return_20d", "stop_delta_20d",
	"stop_hit_rate", "rdd_avg", "rdd_p90",
	"win_5d", "avg_5d", "win_10d", "avg_10d", "win_60d", "avg_60d", "avail_60d",
}

func recentBullRow(c RecentBullCell) []string {
	return []string{
		c.SetupName, strconv.Itoa(c.Bucket), c.StopPolicy, c.Window,
		strconv.Itoa(c.SignalCount), strconv.Itoa(c.Matured20dCount), strconv.Itoa(c.Unmatured20dCount),
		strconv.Itoa(c.Available20d), c.Status,
		f(c.Win20d), f(c.AvgReturn20d), f(c.MedReturn20d), f(c.HoldReturn20d), f(c.StopDelta20d),
		f(c.StopHitRate), f(c.RddAvg), f(c.RddP90),
		f(c.Win5d), f(c.Avg5d), f(c.Win10d), f(c.Avg10d), f(c.Win60d), f(c.Avg60d), strconv.Itoa(c.Avail60d),
	}
}

// WriteRecentBullCSV writes the full (setup × policy × window) grid.
func WriteRecentBullCSV(path string, cells []RecentBullCell) error {
	var b strings.Builder
	b.WriteString(strings.Join(recentBullCSVHeader, ",") + "\n")
	for _, c := range cells {
		b.WriteString(strings.Join(recentBullRow(c), ",") + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func cellOf(cells []RecentBullCell, setup, policy, window string) (RecentBullCell, bool) {
	for _, c := range cells {
		if c.SetupName == setup && c.StopPolicy == policy && c.Window == window {
			return c, true
		}
	}
	return RecentBullCell{}, false
}

// setupOrder / policyOrder fix the row order in the markdown tables.
func recentBullSetupOrder(cells []RecentBullCell) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cells {
		if !seen[c.SetupName] {
			seen[c.SetupName] = true
			out = append(out, c.SetupName)
		}
	}
	return out
}

func recentBullPolicyOrder(cells []RecentBullCell) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cells {
		if !seen[c.StopPolicy] {
			seen[c.StopPolicy] = true
			out = append(out, c.StopPolicy)
		}
	}
	return out
}

// WriteRecentBullMarkdown emits the data tables + the mandatory red-line caveats.
// Interpretation / verdict lives in docs/SPEC_R6_6_RECENT_BULL_REGIME_VALIDATION.md.
func WriteRecentBullMarkdown(path, title string, meta []string, cells []RecentBullCell, windows []RecentBullWindow, baseline string, stopAnchors []string) error {
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	b.WriteString("> **Current Bull Regime Verdict — recent strong-bull window ONLY.**\n")
	b.WriteString("> 本檔為 R6-6 recent bull regime validation 的資料表，**不是 cross-regime validation**。\n")
	b.WriteString("> 結論只適用於近期強多頭 / 超級多頭環境，**不能外推到空頭 / 盤整 / 殺盤 regime**。\n")
	b.WriteString("> primary_horizon = **20d**（對應強勢股 處置~10d + 再處置/出處置 ≈ 20 交易日循環）；\n")
	b.WriteString("> 5d/10d = early reaction / path observation；60d = optional reference，**不作主判斷**。\n")
	b.WriteString("> 最近 20 交易日內的訊號標 UNMATURED_20D，**不計入 20d 統計**。\n")
	b.WriteString("> status：available_20d ≥30 OK／12–29 LOW_SAMPLE／<12 INSUFFICIENT。\n")
	b.WriteString("> **不改 live scanner / config / RocketScore / WatchAction / ExplosionProb / stop profile 預設；不下單、不接 broker。**\n\n")
	for _, m := range meta {
		b.WriteString("- " + m + "\n")
	}
	b.WriteString("\n")

	setups := recentBullSetupOrder(cells)

	for _, w := range windows {
		b.WriteString(fmt.Sprintf("## %s  (signal_date %s → %s)\n\n", w.Name, w.Start, w.End))

		// 1) Setup comparison at the baseline stop policy.
		b.WriteString(fmt.Sprintf("### Setup comparison @ stop=%s — 20d primary\n\n", baseline))
		b.WriteString("| setup | status | signal | matured_20d | unmatured_20d | avail_20d | win_20d | avg_20d | med_20d | hold_20d | stop_Δ20 | stop_hit | rdd_p90 | win_5d | win_10d | avg_60d(avail) |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
		for _, s := range setups {
			c, ok := cellOf(cells, s, baseline, w.Name)
			if !ok {
				continue
			}
			b.WriteString(recentBullMDRow(c))
		}
		b.WriteString("\n")

		// 2) Stop-policy comparison for the anchor setups.
		for _, anchor := range stopAnchors {
			if _, ok := cellOf(cells, anchor, baseline, w.Name); !ok {
				continue
			}
			b.WriteString(fmt.Sprintf("### Stop policy comparison — %s — 20d primary\n\n", anchor))
			b.WriteString("| stop_policy | status | avail_20d | win_20d | avg_20d | med_20d | hold_20d | stop_Δ20 | stop_hit | rdd_avg | rdd_p90 |\n")
			b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
			for _, pol := range recentBullPolicyOrder(cells) {
				c, ok := cellOf(cells, anchor, pol, w.Name)
				if !ok {
					continue
				}
				b.WriteString(fmt.Sprintf("| %s | %s | %d | %s | %s | %s | %s | %s | %s | %s | %s |\n",
					c.StopPolicy, c.Status, c.Available20d,
					pct(c.Win20d), pct(c.AvgReturn20d), pct(c.MedReturn20d), pct(c.HoldReturn20d),
					pct(c.StopDelta20d), pct(c.StopHitRate), pct(c.RddAvg), pct(c.RddP90)))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("---\n\n")
	b.WriteString("```text\n")
	b.WriteString("本次 R6-6 是 recent bull regime validation，不是 cross-regime validation。\n")
	b.WriteString("結論只適用於近期強多頭，不能外推到空頭 / 盤整 / 殺盤。\n")
	b.WriteString("不改 live scanner / config / stop profile 預設；不下單、不接 broker。\n")
	b.WriteString("```\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func recentBullMDRow(c RecentBullCell) string {
	avail60 := fmt.Sprintf("%s(%d)", pct(c.Avg60d), c.Avail60d)
	return fmt.Sprintf("| %s | %s | %d | %d | %d | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
		c.SetupName, c.Status, c.SignalCount, c.Matured20dCount, c.Unmatured20dCount, c.Available20d,
		pct(c.Win20d), pct(c.AvgReturn20d), pct(c.MedReturn20d), pct(c.HoldReturn20d),
		pct(c.StopDelta20d), pct(c.StopHitRate), pct(c.RddP90),
		pct(c.Win5d), pct(c.Win10d), avail60)
}
