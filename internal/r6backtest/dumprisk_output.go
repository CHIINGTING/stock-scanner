package r6backtest

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// num renders a float for a report cell, writing an em dash rather than "NaN" when the value
// was never measurable — a reader must be able to tell "no data" from "zero".
func num(v float64, dec int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "—"
	}
	return fmt.Sprintf("%.*f", dec, v)
}

var dumpCSVHeader = []string{
	"condition", "note", "count", "unique_dates", "unique_stocks", "max_date_share_pct",
	"t1_open_mean", "t1_open_median", "t1_close_mean", "t1_close_median",
	"t1_low_mean", "t1_low_median", "t2_close_mean", "t2_close_median",
	"gap_down_rate", "neg_close_rate", "selloff_rate",
	"t1_mae_atr_mean", "t1_mae_atr_median", "t2_mae_atr_mean", "t2_mae_atr_median",
	"atr_coverage", "t2_coverage", "daytrade_coverage",
	"med_price", "med_avg_vol20", "med_atr_pct", "med_turnover_ntd", "limit_up_share",
}

// WriteDumpCSV writes one row per condition.
func WriteDumpCSV(path string, stats []DumpStat) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write(dumpCSVHeader); err != nil {
		return err
	}
	for _, s := range stats {
		row := []string{
			s.Name, s.Note, fmt.Sprint(s.Count), fmt.Sprint(s.UniqueDates), fmt.Sprint(s.UniqueStocks),
			num(s.MaxDateShare*100, 1),
			num(s.T1OpenMean, 3), num(s.T1OpenMedian, 3),
			num(s.T1CloseMean, 3), num(s.T1CloseMedian, 3),
			num(s.T1LowMean, 3), num(s.T1LowMedian, 3),
			num(s.T2CloseMean, 3), num(s.T2CloseMedian, 3),
			num(s.GapDownRate, 2), num(s.NegCloseRate, 2), num(s.SelloffRate, 2),
			num(s.T1MAEATRMean, 3), num(s.T1MAEATRMedian, 3),
			num(s.T2MAEATRMean, 3), num(s.T2MAEATRMedian, 3),
			num(s.ATRCoverage, 1), num(s.T2Coverage, 1), num(s.DayTradeCoverage, 1),
			num(s.MedPrice, 2), num(s.MedAvgVol20, 0), num(s.MedATRPct, 2),
			num(s.MedTurnover, 0), num(s.LimitUpShare, 1),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return w.Error()
}

// WriteDumpMarkdown renders the human-readable summary. Every table is prefixed by the
// baseline row so a reader cannot see a bucket's number without the control beside it.
func WriteDumpMarkdown(path, title string, meta []string, stats []DumpStat,
	dists []DumpQuantileReport, th DumpThresholds, regime map[string][]DumpStat) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	for _, m := range meta {
		fmt.Fprintf(&b, "- %s\n", m)
	}

	fmt.Fprintf(&b, "\n## Thresholds (derived from the observed distribution, not chosen)\n\n")
	fmt.Fprintf(&b, "| cut | value | source |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| large gain | %s%% | scanner.DefaultPriceMoveThresholds — the SHIPPED band |\n", num(th.LargeGainPct, 2))
	fmt.Fprintf(&b, "| volume spike | %sx | analyzeVolume's shipped expansion band |\n", num(th.VolSpikeRatio, 2))
	fmt.Fprintf(&b, "| weak close (CLV <=) | %s | lower tercile of the trigger population |\n", num(th.WeakCloseCLV, 4))
	fmt.Fprintf(&b, "| strong close (CLV >=) | %s | upper tercile of the trigger population |\n", num(th.StrongCloseCLV, 4))
	fmt.Fprintf(&b, "| long upper shadow (>=) | %s | upper tercile of the trigger population |\n", num(th.LongUpperShadowPct, 4))
	if th.DayTradingAvailable {
		fmt.Fprintf(&b, "| high day-trading (>=) | %s | upper tercile of rows WITH 當沖 data |\n", num(th.HighDayTradingRatio, 4))
	} else {
		fmt.Fprintf(&b, "| high day-trading | — | no 當沖 archive covering this period |\n")
	}
	fmt.Fprintf(&b, "| selloff (T+1 MAE <=) | %s ATR | the %sth percentile of the BASELINE distribution |\n",
		num(th.SelloffMAEATR, 3), num(th.SelloffPctile, 1))

	fmt.Fprintf(&b, "\n## Feature distributions\n\n")
	fmt.Fprintf(&b, "| feature | n | p10 | p25 | p33 | p50 | p67 | p75 | p90 |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, d := range dists {
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s | %s | %s | %s |\n", d.Feature, d.N,
			num(d.P10, 3), num(d.P25, 3), num(d.P33, 3), num(d.P50, 3), num(d.P67, 3), num(d.P75, 3), num(d.P90, 3))
	}

	fmt.Fprintf(&b, "\n## T+1 / T+2 outcomes by condition\n\n")
	writeDumpTable(&b, stats)

	fmt.Fprintf(&b, "\n## Confounder profile — what each bucket is made of\n\n")
	fmt.Fprintf(&b, "A downside edge means nothing until this table shows the bucket is not simply a\n")
	fmt.Fprintf(&b, "selection of cheaper, thinner or more volatile names than the baseline.\n\n")
	fmt.Fprintf(&b, "| condition | n | median price | median 20d volume | median ATR%% | median turnover (NTD) | limit-up share %% |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|---:|\n")
	for _, s := range stats {
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s | %s |\n", s.Name, s.Count,
			num(s.MedPrice, 2), num(s.MedAvgVol20, 0), num(s.MedATRPct, 2),
			num(s.MedTurnover, 0), num(s.LimitUpShare, 1))
	}

	if len(regime) > 0 {
		fmt.Fprintf(&b, "\n## By market regime\n\n")
		for _, name := range sortedRegimeKeys(regime) {
			fmt.Fprintf(&b, "\n### %s\n\n", name)
			writeDumpTable(&b, regime[name])
		}
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeDumpTable(b *strings.Builder, stats []DumpStat) {
	fmt.Fprintf(b, "| condition | n | dates | stocks | max date %% | T+1 open | T+1 close | T+1 low | T+2 close | gap-down %% | neg-close %% | selloff %% | T+1 MAE (ATR) | T+2 MAE (ATR) |\n")
	fmt.Fprintf(b, "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, s := range stats {
		fmt.Fprintf(b, "| %s | %d | %d | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			s.Name, s.Count, s.UniqueDates, s.UniqueStocks, num(s.MaxDateShare*100, 1),
			num(s.T1OpenMean, 3), num(s.T1CloseMean, 3), num(s.T1LowMean, 3), num(s.T2CloseMean, 3),
			num(s.GapDownRate, 2), num(s.NegCloseRate, 2), num(s.SelloffRate, 2),
			num(s.T1MAEATRMean, 3), num(s.T2MAEATRMean, 3))
	}
}

func sortedRegimeKeys(m map[string][]DumpStat) []string {
	// A fixed order keeps two runs diffable; unknown labels sort last.
	order := []string{RegimeBreadthStrong, RegimeBreadthMid, RegimeBreadthWeak, "UNKNOWN"}
	var out []string
	seen := map[string]bool{}
	for _, k := range order {
		if _, ok := m[k]; ok {
			out = append(out, k)
			seen[k] = true
		}
	}
	for k := range m {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}
