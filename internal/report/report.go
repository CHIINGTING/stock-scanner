package report

import (
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

type Config struct {
	OutputDir string `yaml:"output_dir"`
}

type Report struct {
	cfg Config
}

func New(cfg Config) *Report {
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./reports"
	}
	return &Report{cfg: cfg}
}

type PortfolioSummary struct {
	TotalValue  float64
	TotalCost   float64
	TotalPnL    float64
	TotalPnLPct float64
}

func calcSummary(items []scanner.StockAnalysis) PortfolioSummary {
	var s PortfolioSummary
	for _, a := range items {
		s.TotalValue += a.PortfolioValue()
		s.TotalCost += a.PortfolioCost()
	}
	s.TotalPnL = s.TotalValue - s.TotalCost
	if s.TotalCost > 0 {
		s.TotalPnLPct = s.TotalPnL / s.TotalCost * 100
	}
	return s
}

type reportData struct {
	Date         string
	MarketLabel  string
	Market       []scanner.StockAnalysis
	Portfolio    []scanner.StockAnalysis
	Watchlist    []scanner.WatchlistEntry
	Rotation     []scanner.SectorRotation
	PortfolioSum PortfolioSummary
}

func (r *Report) Generate(
	market, portfolio []scanner.StockAnalysis,
	watchlist []scanner.WatchlistEntry,
	rotation []scanner.SectorRotation,
	marketLabel string,
	date time.Time,
) error {
	if err := os.MkdirAll(r.cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if marketLabel == "" {
		marketLabel = "-"
	}
	data := reportData{
		Date:         date.Format("2006-01-02"),
		MarketLabel:  marketLabel,
		Market:       market,
		Portfolio:    portfolio,
		Watchlist:    watchlist,
		Rotation:     rotation,
		PortfolioSum: calcSummary(portfolio),
	}

	fname := filepath.Join(r.cfg.OutputDir, fmt.Sprintf("report_%s.html", date.Format("20060102")))
	f, err := os.Create(fname)
	if err != nil {
		return fmt.Errorf("create %s: %w", fname, err)
	}
	defer f.Close()

	funcs := template.FuncMap{
		"f2":      func(v float64) string { return fmt.Sprintf("%.2f", v) },
		"f1":      func(v float64) string { return fmt.Sprintf("%.1f", v) },
		"pct":     func(v float64) string { return fmt.Sprintf("%.1f%%", v) },
		"pctSign": func(v float64) string { return fmt.Sprintf("%+.1f%%", v) },
		"fmtMoney": func(v float64) string {
			if v < 0 {
				return fmt.Sprintf("▼ %.0f", -v)
			}
			return fmt.Sprintf("%.0f", v)
		},
		"fmtVol": fmtVolume,
		"inc":    func(i int) int { return i + 1 },
		"actionCSS": func(a scanner.Action) string {
			return scanner.ActionCSS[a]
		},
		"pnlCSS": func(v float64) string {
			if v > 0 {
				return "pos"
			}
			if v < 0 {
				return "neg"
			}
			return "neu"
		},
		"volCSS": func(r float64) string {
			if r >= 1.5 {
				return "pos"
			}
			if r < 0.8 && r > 0 {
				return "neg"
			}
			return "neu"
		},
		"rsiCSS": func(rsi float64) string {
			if rsi < 30 {
				return "pos"
			}
			if rsi > 70 {
				return "neg"
			}
			return "neu"
		},
		"sub50": func(v float64) float64 { return v - 50 },
		"joinReasons": func(rs []string) template.HTML {
			var parts []string
			for _, r := range rs {
				if r != "" {
					parts = append(parts, template.HTMLEscapeString(r))
				}
			}
			return template.HTML(strings.Join(parts, "<br>"))
		},
		"bfpDots": func(points int) template.HTML {
			s := ""
			for i := 1; i <= 5; i++ {
				if i <= points {
					s += `<span class="bfp-dot pass">●</span>`
				} else {
					s += `<span class="bfp-dot fail">○</span>`
				}
			}
			return template.HTML(fmt.Sprintf(`<span class="bfp-wrap">%s <span class="bfp-count">%d/5</span></span>`, s, points))
		},
		"scoreBar": func(s int) template.HTML {
			cls := "bar-low"
			if s >= 62 {
				cls = "bar-high"
			} else if s >= 47 {
				cls = "bar-mid"
			}
			return template.HTML(fmt.Sprintf(
				`<div class="score-bar"><div class="%s" style="width:%dpx"></div><span>%d</span></div>`,
				cls, s, s))
		},
		"pvCSS": func(sig string) string {
			switch sig {
			case "價漲量增":
				return "pv-up-vol-up"
			case "價漲量縮":
				return "pv-up-vol-down"
			case "價跌量增":
				return "pv-down-vol-up"
			case "漲停鎖量":
				return "pv-locked"
			case "漲停失敗":
				return "pv-failed"
			default:
				return "pv-down-vol-down"
			}
		},
		"f0pct":   func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
		"pctSign1": func(v float64) string { return fmt.Sprintf("%+.1f%%", v) },
		"sectorScoreBar": func(v float64) template.HTML {
			s := int(v + 0.5)
			cls := "bar-low"
			if s >= 70 {
				cls = "bar-high"
			} else if s >= 50 {
				cls = "bar-mid"
			}
			return template.HTML(fmt.Sprintf(
				`<div class="score-bar"><div class="%s" style="width:%dpx"></div><span>%d</span></div>`,
				cls, s, s))
		},
		"stageCSS": func(s scanner.RotationStage) string {
			switch s {
			case scanner.EarlyRotation:
				return "stage-early"
			case scanner.ConfirmedRotation:
				return "stage-confirmed"
			case scanner.HotRotation:
				return "stage-hot"
			case scanner.LateRotation:
				return "stage-late"
			default:
				return "stage-early"
			}
		},
		"stageLabel": func(s scanner.RotationStage) string {
			switch s {
			case scanner.EarlyRotation:
				return "醞釀 EARLY"
			case scanner.ConfirmedRotation:
				return "確認 CONFIRMED"
			case scanner.HotRotation:
				return "過熱 HOT"
			case scanner.LateRotation:
				return "末段 LATE"
			default:
				return string(s)
			}
		},
		"boolMark": func(b bool) template.HTML {
			if b {
				return template.HTML(`<span class="yes">✓</span>`)
			}
			return template.HTML(`<span class="no">·</span>`)
		},
		"flowCSS": func(state string) string {
			switch state {
			case "流入":
				return "flow-in"
			case "流出":
				return "flow-out"
			default:
				return "flow-neutral"
			}
		},
		"flowLabel": func(state string) string {
			switch state {
			case "流入":
				return "流入 ↑"
			case "流出":
				return "流出 ↓"
			default:
				return "中性"
			}
		},
		"flowArrow": func(v float64) template.HTML {
			switch {
			case v >= 0.2:
				return template.HTML(`<span class="flow-in">↑</span>`)
			case v <= -0.2:
				return template.HTML(`<span class="flow-out">↓</span>`)
			default:
				return template.HTML(`<span class="flow-neutral">→</span>`)
			}
		},
		"shortDirCSS": func(dir string) string {
			switch dir {
			case scanner.FlowInflow:
				return "flow-in"
			case scanner.FlowOutflow:
				return "flow-out"
			default:
				return "flow-neutral"
			}
		},
		"shortDirLabel": func(dir string) string {
			switch dir {
			case scanner.FlowInflow:
				return "流入 ↑"
			case scanner.FlowOutflow:
				return "流出 ↓"
			default:
				return "中性"
			}
		},
		"shortStageCSS": func(st string) string {
			switch st {
			case scanner.STEarlyRotation:
				return "stage-early"
			case scanner.STConfirmedRotation:
				return "stage-confirmed"
			case scanner.STOverheated:
				return "stage-hot"
			default: // WEAKENING
				return "stage-late"
			}
		},
		"shortStageLabel": func(st string) string {
			switch st {
			case scanner.STEarlyRotation:
				return "早期輪動 EARLY"
			case scanner.STConfirmedRotation:
				return "確認輪動 CONFIRMED"
			case scanner.STOverheated:
				return "過熱 OVERHEATED"
			default:
				return "轉弱 WEAKENING"
			}
		},
		"midCSS": func(label string) string {
			switch label {
			case "強":
				return "lvl-strong"
			case "中":
				return "lvl-mid"
			default:
				return "lvl-weak"
			}
		},
		"trendCSS": func(label string) string {
			switch label {
			case "確認上升":
				return "lvl-strong"
			case "尚未確認":
				return "lvl-mid"
			default:
				return "lvl-weak"
			}
		},
		"rocketStageLabel": func(st scanner.RocketStage) string { return rocketStageText(st) },
		"rocketStageCSS": func(st scanner.RocketStage) string {
			switch st {
			case scanner.StagePreBreakout, scanner.StageBreakoutStart:
				return "rk-go"
			case scanner.StageMainRun:
				return "rk-run"
			case scanner.StageBaseBuilding:
				return "rk-base"
			case scanner.StageOverheated:
				return "rk-hot"
			case scanner.StageFailed:
				return "rk-fail"
			default:
				return "rk-wait"
			}
		},
		"watchActionLabel": func(a scanner.WatchAction) string { return string(a) },
		"watchActionCSS": func(a scanner.WatchAction) string {
			switch a {
			case scanner.ActBreakoutBuy, scanner.ActPullbackBuy:
				return "act-buy"
			case scanner.ActPrepare:
				return "act-prepare"
			case scanner.ActWatchClose:
				return "act-watch"
			case scanner.ActTakeProfit:
				return "act-tp"
			case scanner.ActRemove:
				return "act-remove"
			default:
				return "act-wait"
			}
		},
		// stagePriority：階段排序優先級（數字越大＝越接近噴出，DESC 時排最前）
		"stagePriority": func(st scanner.RocketStage) int {
			switch st {
			case scanner.StageBreakoutStart:
				return 7 // 起漲 — 最可能噴
			case scanner.StagePreBreakout:
				return 6 // 突破前 — 最接近突破
			case scanner.StageMainRun:
				return 5 // 主升
			case scanner.StageBaseBuilding:
				return 4 // 築底 — 需要準備
			case scanner.StageOverheated:
				return 3 // 過熱
			case scanner.StageNotReady:
				return 2 // 未就緒
			case scanner.StageFailed:
				return 1 // 失敗
			default:
				return 0
			}
		},
		// actionPriority：操作建議排序優先級（數字越大＝越該出手，DESC 時排最前）
		"actionPriority": func(a scanner.WatchAction) int {
			switch a {
			case scanner.ActBreakoutBuy:
				return 7 // 突破買進
			case scanner.ActPullbackBuy:
				return 6 // 回拉買進
			case scanner.ActPrepare:
				return 5 // 準備進場
			case scanner.ActWatchClose:
				return 4 // 密切觀察
			case scanner.ActTakeProfit:
				return 3 // 停利
			case scanner.ActWait:
				return 2 // 等待
			case scanner.ActRemove:
				return 1 // 移除
			default:
				return 0
			}
		},
		// riskPriority：風險排序優先級（數字越大＝風險越高，DESC 時排最前）
		"riskPriority": func(label string) int {
			switch label {
			case "跌破支撐":
				return 7
			case "追高":
				return 6
			case "族群轉弱":
				return 5
			case "假突破":
				return 4
			case "量不足":
				return 3
			case "上影/收弱":
				return 2
			default: // "—" 或無風險
				return 1
			}
		},
		"bucketLabel": func(b scanner.ConsolBucket) string {
			switch b {
			case scanner.MicroBase:
				return "極短整理 MICRO (3~5日)"
			case scanner.ShortBase:
				return "短線平台 SHORT (6~10日)"
			case scanner.SwingBase:
				return "波段平台 SWING (11~20日)"
			case scanner.MidBase:
				return "中期平台 MID (21~40日)"
			case scanner.LongBase:
				return "長期底部 LONG (41~60日)"
			default:
				return "無明顯整理"
			}
		},
		"confCSS": func(c string) string {
			switch c {
			case "HIGH":
				return "lvl-strong"
			case "MEDIUM":
				return "lvl-mid"
			default:
				return "lvl-weak"
			}
		},
		"rocketGauge": func(score int) template.HTML {
			cls := "bar-low"
			if score >= 75 {
				cls = "bar-high"
			} else if score >= 60 {
				cls = "bar-mid"
			}
			return template.HTML(fmt.Sprintf(
				`<div class="score-bar"><div class="%s" style="width:%dpx"></div><span>%d</span></div>`,
				cls, score, score))
		},
		"riskCSS": func(label string) string {
			if label == "—" || label == "" {
				return "lvl-weak"
			}
			return "risk-tag"
		},
		"probCSS": func(p string) string {
			switch p {
			case "HIGH":
				return "lvl-strong"
			case "MEDIUM":
				return "lvl-mid"
			default:
				return "lvl-weak"
			}
		},
	}

	tmpl, err := template.New("report").Funcs(funcs).Parse(htmlTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("render: %w", err)
	}

	log.Printf("report: %s", fname)
	printConsole(market, portfolio, watchlist, rotation, marketLabel, date)
	return nil
}

func stageText(s scanner.RotationStage) string {
	switch s {
	case scanner.EarlyRotation:
		return "醞釀 EARLY"
	case scanner.ConfirmedRotation:
		return "確認 CONFIRMED"
	case scanner.HotRotation:
		return "過熱 HOT"
	case scanner.LateRotation:
		return "末段 LATE"
	default:
		return string(s)
	}
}

func shortDirText(dir string) string {
	switch dir {
	case scanner.FlowInflow:
		return "流入↑"
	case scanner.FlowOutflow:
		return "流出↓"
	default:
		return "中性"
	}
}

func shortStageText(st string) string {
	switch st {
	case scanner.STEarlyRotation:
		return "早期輪動"
	case scanner.STConfirmedRotation:
		return "確認輪動"
	case scanner.STOverheated:
		return "過熱"
	default:
		return "轉弱"
	}
}

func fmtVolume(v int64) string {
	switch {
	case v >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(v)/1_000_000_000)
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.0fK", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func printConsole(market, portfolio []scanner.StockAnalysis, watchlist []scanner.WatchlistEntry, rotation []scanner.SectorRotation, marketLabel string, date time.Time) {
	sep := "═══════════════════════════════════════════════════════════════════"
	fmt.Printf("\n%s\n  台股掃描報告  %s\n%s\n", sep, date.Format("2006-01-02"), sep)

	if len(rotation) > 0 {
		fmt.Printf("\n[🔄 族群輪動 (Rotation) — 三層：短線(1~5日)/中期(20日)/趨勢(60日)，依機會排序]\n")
		fmt.Printf("%-4s  %-14s  %-8s  %-14s  %6s  %4s  %-10s  %6s\n",
			"Rank", "族群", "短線流向", "短線階段", "短線分", "20日", "60日趨勢", "機會分")
		var earlyCandidates []string
		for i, sr := range rotation {
			fmt.Printf("%-4d  %-14s  %-8s  %-14s  %6.0f  %4s  %-10s  %6.0f\n",
				i+1, sr.Name, shortDirText(sr.ShortTermFlowDir), shortStageText(sr.ShortTermFlowStage),
				sr.ShortTermFlowScore, sr.MidTermLabel, sr.TrendLabel, sr.OppScore)
			if sr.ShortTermFlowStage == scanner.STEarlyRotation && sr.ShortTermFlowDir == scanner.FlowInflow {
				earlyCandidates = append(earlyCandidates, sr.Name)
			}
		}
		if len(earlyCandidates) > 0 {
			fmt.Printf("  ▶ 早期輪動候選（資金剛流入、20日尚未反映）：%s\n", strings.Join(earlyCandidates, "、"))
		}
	}

	if len(portfolio) > 0 {
		fmt.Printf("\n[💼 持倉 (Positions)]\n")
		fmt.Printf("%-6s  %-8s  %7s  %7s  %8s  %-12s\n",
			"代號", "名稱", "成本", "現價", "損益%", "建議")
		for _, a := range portfolio {
			fmt.Printf("%-6s  %-8s  %7.2f  %7.2f  %+8.1f%%  %-12s\n",
				a.Symbol, a.Name, a.CostBasis, a.Close, a.PnLPct, a.Action)
		}
		sum := calcSummary(portfolio)
		fmt.Printf("  ▶ 總市值 %.0f  總成本 %.0f  損益 %+.0f (%+.1f%%)\n",
			sum.TotalValue, sum.TotalCost, sum.TotalPnL, sum.TotalPnLPct)
	}

	if len(watchlist) > 0 {
		fmt.Printf("\n[🚀 觀察清單 — 飆股候選追蹤]\n")
		fmt.Printf("%-6s  %-9s  %-12s  %5s  %-15s  %-22s  %s\n",
			"代號", "名稱", "族群", "飆股分", "階段", "操作建議", "風險")
		var imminent []string
		for _, e := range watchlist {
			fmt.Printf("%-6s  %-9s  %-12s  %5d  %-15s  %-22s  %s\n",
				e.A.Symbol, e.A.Name, e.Sector, e.RocketScore,
				rocketStageText(e.RocketStage), string(e.WatchAction), e.RiskLabel)
			if e.RocketStage == scanner.StagePreBreakout || e.RocketStage == scanner.StageBreakoutStart {
				imminent = append(imminent, fmt.Sprintf("%s(%d)", e.A.Name, e.RocketScore))
			}
		}
		if len(imminent) > 0 {
			fmt.Printf("  ▶ 最接近發動：%s\n", strings.Join(imminent, "、"))
		}
	}

	if len(market) > 0 {
		fmt.Printf("\n[📊 市場掃描(Top %s)]\n", marketLabel)
		fmt.Printf("%-6s  %-8s  %7s  %4s  %3s/5  %-12s  %7s  %7s\n",
			"代號", "名稱", "現價", "分數", "BFP", "建議", "停損", "目標1")
		for _, a := range market {
			fmt.Printf("%-6s  %-8s  %7.2f  %4d  %5d  %-12s  %7.2f  %7.2f\n",
				a.Symbol, a.Name, a.Close, a.Score, a.BFPPoints, a.Action,
				a.StopLoss, a.Target1)
		}
	}

	// Detailed reasons for positions
	if len(portfolio) > 0 {
		fmt.Printf("\n[持倉原因詳細]\n")
		for _, a := range portfolio {
			fmt.Printf("  %s %s:\n", a.Symbol, a.Name)
			for _, r := range a.Reasons {
				if r != "" {
					fmt.Printf("    %s\n", r)
				}
			}
			fmt.Printf("    → 進場 %.2f  停損 %.2f  目標1 %.2f  目標2 %.2f\n\n",
				a.EntryPrice, a.StopLoss, a.Target1, a.Target2)
		}
	}

	// Watchlist decision detail
	if len(watchlist) > 0 {
		fmt.Printf("\n[飆股候選決策詳細]\n")
		for _, e := range watchlist {
			fmt.Printf("  %s %s｜%s｜%s｜飆股分 %d｜噴出機率 %s｜觀察 %s\n",
				e.A.Symbol, e.A.Name, e.Sector, rocketStageText(e.RocketStage),
				e.RocketScore, e.ExplosionProb, e.DaysToWatch)
			fmt.Printf("    輪動: 短線 %s／20日 %s／族群階段 %s\n",
				shortDirText(e.SectorFlowDir), e.SectorMidLabel, stageText(e.SectorStage))
			fmt.Printf("    型態: %s／整理 %d 天／回測 %s 個股勝率 %.0f%%(%d)／族群勝率 %.0f%%(%d)／信心 %s\n",
				bucketText(e.Consol.Bucket), e.Consol.Days, e.Backtest.PatternName,
				e.Backtest.StockWinRate, e.Backtest.StockSampleCount,
				e.Backtest.SectorWinRate, e.Backtest.SectorSampleCount, e.Backtest.Confidence)
			fmt.Printf("    價位: 現價 %.2f／進場 %s／突破 %.2f／支撐 %.2f／停損 %.2f／停利 %s\n",
				e.A.Close, e.EntryZone, e.BreakoutPrice, e.SupportPrice, e.StopLossPrice, e.TakeProfitZone)
			fmt.Printf("    操作: %s｜風險: %s\n\n", string(e.WatchAction), e.RiskWarning)
		}
	}
}

func rocketStageText(st scanner.RocketStage) string {
	switch st {
	case scanner.StageNotReady:
		return "未就緒 NOT_READY"
	case scanner.StageBaseBuilding:
		return "築底 BASE_BUILDING"
	case scanner.StagePreBreakout:
		return "突破前 PRE_BREAKOUT"
	case scanner.StageBreakoutStart:
		return "起漲 BREAKOUT_START"
	case scanner.StageMainRun:
		return "主升 MAIN_RUN"
	case scanner.StageOverheated:
		return "過熱 OVERHEATED"
	case scanner.StageFailed:
		return "失敗 FAILED"
	default:
		return string(st)
	}
}

func bucketText(b scanner.ConsolBucket) string {
	switch b {
	case scanner.MicroBase:
		return "MICRO"
	case scanner.ShortBase:
		return "SHORT"
	case scanner.SwingBase:
		return "SWING"
	case scanner.MidBase:
		return "MID"
	case scanner.LongBase:
		return "LONG"
	default:
		return "NO_BASE"
	}
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="zh-Hant">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>股票雷達 {{ .Date }}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,"PingFang TC","Noto Sans TC",sans-serif;background:#0c1220;color:#e2e8f0;min-height:100vh;font-size:13px}
.container{max-width:1800px;margin:0 auto;padding:18px 14px}
h1{font-size:1.4rem;font-weight:700;color:#f8fafc;border-bottom:2px solid #1e3a5f;padding-bottom:10px;margin-bottom:14px}
h1 small{font-size:.78rem;color:#64748b;font-weight:400;margin-left:8px}

/* Tabs */
.tabs{display:flex;gap:0;margin-bottom:18px;border-bottom:2px solid #1e3a5f}
.tab-btn{padding:9px 20px;background:none;border:none;color:#64748b;font-size:.85rem;cursor:pointer;border-bottom:2px solid transparent;margin-bottom:-2px;font-family:inherit;transition:all .15s}
.tab-btn:hover{color:#94a3b8}
.tab-btn.active{color:#38bdf8;border-bottom-color:#38bdf8;font-weight:600}
.tab-pane{display:none}.tab-pane.active{display:block}

/* Tables */
table{width:100%;border-collapse:collapse;background:#111827;border-radius:10px;overflow:hidden;margin-bottom:6px}
thead{background:#0c1220}
th{padding:8px 10px;text-align:left;font-weight:600;color:#475569;font-size:.68rem;text-transform:uppercase;letter-spacing:.04em;border-bottom:1px solid #1e3a5f;white-space:nowrap}
th.sortable{cursor:pointer;user-select:none}
th.sortable:hover{color:#93c5fd}
th.sort-active{color:#60a5fa}
.sort-ico{display:inline-block;width:.8em;color:#60a5fa;font-size:.85em}
th.r,td.r{text-align:right}
td{padding:7px 10px;border-bottom:1px solid #131e2e;vertical-align:top}
tr:last-child td{border-bottom:none}
tr:hover td{background:#0f1d30}

/* Action badges */
.action-badge{display:inline-block;padding:3px 8px;border-radius:4px;font-weight:700;font-size:.71rem;white-space:nowrap;letter-spacing:.02em}
.action-strong-buy{background:#052e16;color:#4ade80;border:1px solid #16a34a}
.action-buy{background:#0f2d1a;color:#86efac;border:1px solid #22c55e66}
.action-watch{background:#0c2340;color:#7dd3fc;border:1px solid #0284c766}
.action-hold{background:#1c1500;color:#fcd34d;border:1px solid #ca8a0466}
.action-reduce{background:#2d1200;color:#fdba74;border:1px solid #ea580c66}
.action-take-profit{background:#052e16;color:#a3e635;border:2px solid #65a30d;letter-spacing:.05em}
.action-stop-loss{background:#3b0a0a;color:#fca5a5;border:2px solid #dc2626;font-size:.75rem}
.action-sell{background:#1c0202;color:#f87171;border:1px solid #dc262666}

/* Score bar */
.score-bar{display:flex;align-items:center;gap:5px}
.score-bar>div{height:5px;border-radius:3px;min-width:2px}
.bar-high{background:#4ade80}.bar-mid{background:#fbbf24}.bar-low{background:#f87171}
.score-bar span{font-weight:700;font-size:.8rem;color:#f1f5f9;min-width:24px}

/* BFP dots */
.bfp-wrap{white-space:nowrap;font-size:.85rem}
.bfp-dot{margin:0 1px}
.bfp-dot.pass{color:#4ade80}.bfp-dot.fail{color:#374151}
.bfp-count{font-size:.72rem;color:#94a3b8;margin-left:3px}

/* Price targets */
.t-entry{color:#38bdf8!important}.t-stop{color:#f87171!important}
.t-t1{color:#4ade80!important}.t-t2{color:#a3e635!important}

/* P&L */
.pos{color:#4ade80}.neg{color:#f87171}.neu{color:#94a3b8}
.sym{font-weight:700;color:#f8fafc}.name-col{color:#94a3b8}

/* Reasons */
.reasons{font-size:.71rem;color:#94a3b8;line-height:1.6;max-width:360px}

/* Price-Volume signal */
.pv-up-vol-up{color:#4ade80;font-weight:600}
.pv-up-vol-down{color:#fbbf24}
.pv-down-vol-up{color:#f87171;font-weight:600}
.pv-down-vol-down{color:#64748b}
.pv-locked{color:#a3e635;font-weight:700}
.pv-failed{color:#f87171;font-weight:700}

/* Portfolio summary */
.pf-summary{background:#111827;border:1px solid #1e3a5f;border-radius:10px;padding:14px 20px;margin-bottom:14px;display:flex;gap:24px;flex-wrap:wrap;align-items:center}
.pf-item label{display:block;font-size:.68rem;color:#475569;text-transform:uppercase;letter-spacing:.05em;margin-bottom:2px}
.pf-item .val{font-size:1.15rem;font-weight:700}

/* Alert banner for STOP LOSS / TAKE PROFIT */
.alert-sl{background:#3b0a0a55;border:1px solid #dc262677;border-radius:6px;padding:4px 10px;font-size:.72rem;color:#fca5a5;margin-bottom:2px}
.alert-tp{background:#05280f55;border:1px solid #16a34a77;border-radius:6px;padding:4px 10px;font-size:.72rem;color:#86efac;margin-bottom:2px}

.empty{text-align:center;padding:30px;color:#475569;font-size:.9rem}
footer{margin-top:20px;font-size:.68rem;color:#374151;text-align:center;padding:10px 0}

/* Volume score badge */
.vol-score{display:inline-block;background:#0c1f3a;border:1px solid #1e3a5f;border-radius:4px;padding:1px 6px;font-size:.7rem;color:#38bdf8;font-weight:600}

/* ── Rotation ─────────────────────────────────────────────────────────────── */
.rot-intro{background:#0d1a2e;border:1px solid #1e3a5f;border-radius:8px;padding:10px 14px;margin-bottom:12px;font-size:.76rem;color:#94a3b8;line-height:1.7}
.rot-intro b{color:#e2e8f0}
th.c,td.c{text-align:center}
th.rotscore{min-width:120px}
.stage-badge{display:inline-block;padding:3px 9px;border-radius:4px;font-weight:700;font-size:.7rem;white-space:nowrap;letter-spacing:.02em}
.stage-early{background:#06263b;color:#38bdf8;border:1px solid #0ea5e9}          /* 機會：醞釀 */
.stage-confirmed{background:#052e16;color:#4ade80;border:1px solid #16a34a}      /* 確認 */
.stage-hot{background:#2d1200;color:#fdba74;border:1px solid #ea580c}            /* 過熱（淡化） */
.stage-late{background:#1a1a1a;color:#9ca3af;border:1px solid #4b5563}           /* 末段（淡化） */
.sector-row{cursor:pointer}
.sector-row:hover td{background:#10243d}
.exp-caret{color:#64748b;text-align:center;font-size:.8rem}
.sector-detail{display:none}
.sector-detail.open{display:table-row}
.sector-detail>td{padding:0 0 10px 0;background:#0b1422}
.sub-table{margin:0;border-radius:0;background:#0b1422;box-shadow:inset 3px 0 0 #1e3a5f}
.sub-table th{background:#0b1422;color:#475569}
.sub-table td{border-bottom:1px solid #111c2c}
.yes{color:#4ade80;font-weight:700}
.no{color:#475569}
.flow-badge{display:inline-block;padding:2px 8px;border-radius:4px;font-weight:700;font-size:.7rem;white-space:nowrap}
.flow-in{color:#4ade80}
.flow-out{color:#f87171}
.flow-neutral{color:#94a3b8}
.flow-badge.flow-in{background:#052e16;border:1px solid #16a34a66}
.flow-badge.flow-out{background:#3b0a0a;border:1px solid #dc262666}
.flow-badge.flow-neutral{background:#1a2436;border:1px solid #33415566}
.lvl-strong{color:#4ade80;font-weight:700}
.lvl-mid{color:#fbbf24;font-weight:600}
.lvl-weak{color:#94a3b8}
.sector-meta{padding:8px 14px;font-size:.72rem;color:#94a3b8;line-height:1.8;background:#0b1422}
.meta-concl{display:block;color:#e2e8f0;font-weight:600;font-size:.78rem;margin-bottom:3px}
.meta-nums b{font-weight:700}

/* ── Watchlist 飆股候選 ───────────────────────────────────────────────────── */
.rk-go{background:#06263b;color:#38bdf8;border:1px solid #0ea5e9}      /* PRE/BREAKOUT */
.rk-run{background:#052e16;color:#4ade80;border:1px solid #16a34a}      /* MAIN_RUN */
.rk-base{background:#1c2433;color:#cbd5e1;border:1px solid #475569}     /* BASE_BUILDING */
.rk-hot{background:#2d1200;color:#fdba74;border:1px solid #ea580c}      /* OVERHEATED */
.rk-fail{background:#3b0a0a;color:#fca5a5;border:1px solid #dc2626}     /* FAILED */
.rk-wait{background:#161e2e;color:#94a3b8;border:1px solid #334155}     /* NOT_READY */
.act-badge{display:inline-block;padding:2px 8px;border-radius:4px;font-weight:700;font-size:.68rem;white-space:nowrap}
.act-buy{background:#052e16;color:#4ade80;border:1px solid #16a34a}
.act-prepare{background:#06263b;color:#38bdf8;border:1px solid #0ea5e9}
.act-watch{background:#1c2433;color:#cbd5e1;border:1px solid #475569}
.act-tp{background:#052e16;color:#a3e635;border:1px solid #65a30d}
.act-remove{background:#3b0a0a;color:#fca5a5;border:1px solid #dc2626}
.act-wait{background:#161e2e;color:#94a3b8;border:1px solid #334155}
.risk-tag{display:inline-block;color:#fca5a5;font-weight:600;font-size:.72rem}
.wl-card{padding:12px 16px;background:#0b1422}
.wl-head{font-size:.85rem;color:#e2e8f0;font-weight:600;padding-bottom:10px;margin-bottom:10px;border-bottom:1px solid #1e3a5f}
.wl-head b{color:#38bdf8;font-size:1rem}
.wl-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:12px}
.wl-sec{background:#0f1d30;border:1px solid #16263d;border-radius:8px;padding:10px 12px;font-size:.74rem;color:#cbd5e1;line-height:1.85}
.wl-sec h4{font-size:.72rem;color:#7dd3fc;margin-bottom:5px;font-weight:700}
.wl-sec b{color:#f1f5f9}
.wl-sec ul{margin:0;padding-left:16px}
.wl-sec li{margin:2px 0}
.wl-note{color:#94a3b8;font-size:.7rem;margin-top:3px;font-style:italic}
.wl-risk{border-color:#3b1414;background:#1a0f12}
.wl-risk h4{color:#fca5a5}
@media(max-width:900px){.wl-grid{grid-template-columns:1fr}}
</style>
</head>
<body>
<div class="container">
<h1>📡 股票雷達<small>{{ .Date }} 盤後分析</small></h1>

<div class="tabs">
  <button class="tab-btn active" onclick="tab(event,'positions')">💼 持倉 ({{ len .Portfolio }})</button>
  <button class="tab-btn" onclick="tab(event,'watchlist')">🚀 飆股候選 ({{ len .Watchlist }})</button>
  <button class="tab-btn" onclick="tab(event,'market')">📊 市場掃描({{ .MarketLabel }})</button>
  <button class="tab-btn" onclick="tab(event,'rotation')">🔄 輪動 ({{ len .Rotation }})</button>
</div>

<!-- ══ POSITIONS ════════════════════════════════════════════════════════════ -->
<div id="tab-positions" class="tab-pane active">
{{ if eq (len .Portfolio) 0 }}
<div class="empty">持倉無資料。請在 stocks.yaml 的 positions: 區段加入持股。</div>
{{ else }}
<div class="pf-summary">
  <div class="pf-item"><label>總市值</label><div class="val">{{ fmtMoney .PortfolioSum.TotalValue }}</div></div>
  <div class="pf-item"><label>總成本</label><div class="val">{{ fmtMoney .PortfolioSum.TotalCost }}</div></div>
  <div class="pf-item"><label>損益%</label>
    <div class="val {{ pnlCSS .PortfolioSum.TotalPnL }}">{{ pctSign .PortfolioSum.TotalPnLPct }}</div></div>
  <div class="pf-item"><label>損益額(元)</label>
    <div class="val {{ pnlCSS .PortfolioSum.TotalPnL }}">{{ fmtMoney .PortfolioSum.TotalPnL }}</div></div>
</div>
<table>
  <thead><tr>
    <th>代號</th><th>名稱</th>
    <th class="r">成本</th><th class="r">現價</th>
    <th class="r">股數</th><th class="r">市值</th>
    <th class="r">損益%</th><th class="r">損益額</th>
    <th>BFP</th><th>評分</th><th>交易建議</th>
    <th class="r t-stop">停損</th><th class="r t-t1">目標1</th><th class="r t-t2">目標2</th>
    <th class="r">RSI</th><th>MA20</th><th class="r">量比</th><th>量價</th>
    <th>分析原因</th>
  </tr></thead>
  <tbody>
  {{ range .Portfolio }}
  <tr>
    <td class="sym">{{ .Symbol }}</td>
    <td class="name-col">{{ .Name }}</td>
    <td class="r">{{ f2 .CostBasis }}</td>
    <td class="r">{{ f2 .Close }}</td>
    <td class="r">{{ .Shares }}</td>
    <td class="r">{{ fmtMoney .PortfolioValue }}</td>
    <td class="r {{ pnlCSS .PnLPct }}">{{ pctSign .PnLPct }}</td>
    <td class="r {{ pnlCSS .PnLValue }}">{{ fmtMoney .PnLValue }}</td>
    <td>{{ bfpDots .BFPPoints }}</td>
    <td>{{ scoreBar .Score }}</td>
    <td>
      {{ if eq .Action "STOP LOSS" }}<div class="alert-sl">⛔ 建議停損</div>{{ end }}
      {{ if eq .Action "TAKE PROFIT" }}<div class="alert-tp">✅ 建議獲利了結</div>{{ end }}
      <span class="action-badge {{ actionCSS .Action }}">{{ .Action }}</span>
    </td>
    <td class="r t-stop">{{ f2 .StopLoss }}</td>
    <td class="r t-t1">{{ f2 .Target1 }}</td>
    <td class="r t-t2">{{ f2 .Target2 }}</td>
    <td class="r {{ rsiCSS .RSI }}">{{ f1 .RSI }}</td>
    <td>{{ .MA20Trend }}</td>
    <td class="r {{ volCSS .VolumeRatio }}">{{ f1 .VolumeRatio }}x</td>
    <td class="{{ pvCSS .PriceVolumeSignal }}">{{ .PriceVolumeSignal }}</td>
    <td><div class="reasons">{{ joinReasons .Reasons }}</div></td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<!-- ══ WATCHLIST：飆股候選追蹤 ══════════════════════════════════════════════ -->
<div id="tab-watchlist" class="tab-pane">
{{ if eq (len .Watchlist) 0 }}
<div class="empty">觀察清單無資料。請在 stocks.yaml 的 watchlist: 區段加入股票。</div>
{{ else }}
<div class="rot-intro">🚀 <b>飆股候選追蹤</b>：Scanner 找標的，Watchlist 判斷它是不是<b>快變飆股</b>。點任一檔展開決策卡片，回答：要等突破還是拉回？失敗跌破哪裡移除？型態過去成功率高不高？</div>
<table>
  <thead><tr>
    <th>#</th><th>股票</th>
    <th class="sortable" onclick="sortWatch('sector',this)">族群 <span class="sort-ico"></span></th>
    <th class="rotscore sortable" onclick="sortWatch('score',this)">飆股分數 <span class="sort-ico"></span></th>
    <th class="sortable" onclick="sortWatch('stage',this)">階段 <span class="sort-ico"></span></th>
    <th class="sortable" onclick="sortWatch('action',this)">操作建議 <span class="sort-ico"></span></th>
    <th class="r">噴出</th>
    <th class="sortable" onclick="sortWatch('risk',this)">風險 <span class="sort-ico"></span></th>
    <th></th>
  </tr></thead>
  <tbody>
  {{ range $i, $e := .Watchlist }}
  <tr class="sector-row" onclick="toggleWatch({{ $i }})" data-score="{{ $e.RocketScore }}" data-sector="{{ $e.Sector }}" data-stage="{{ stagePriority $e.RocketStage }}" data-action="{{ actionPriority $e.WatchAction }}" data-risk="{{ riskPriority $e.RiskLabel }}">
    <td class="neu wl-num">{{ inc $i }}</td>
    <td><span class="sym">{{ $e.A.Symbol }}</span> <span class="name-col">{{ $e.A.Name }}</span></td>
    <td class="name-col">{{ if $e.Sector }}{{ $e.Sector }}{{ else }}—{{ end }}</td>
    <td class="rotscore">{{ rocketGauge $e.RocketScore }}</td>
    <td><span class="stage-badge {{ rocketStageCSS $e.RocketStage }}">{{ rocketStageLabel $e.RocketStage }}</span></td>
    <td><span class="act-badge {{ watchActionCSS $e.WatchAction }}">{{ watchActionLabel $e.WatchAction }}</span></td>
    <td class="r {{ probCSS $e.ExplosionProb }}">{{ $e.ExplosionProb }}</td>
    <td><span class="{{ riskCSS $e.RiskLabel }}">{{ $e.RiskLabel }}</span></td>
    <td class="exp-caret"><span id="wcaret-{{ $i }}">▸</span></td>
  </tr>
  <tr class="sector-detail" id="wdetail-{{ $i }}">
    <td colspan="9">
      <div class="wl-card">
        <div class="wl-head">{{ $e.A.Symbol }} {{ $e.A.Name }}｜{{ if $e.Sector }}{{ $e.Sector }}{{ else }}無族群{{ end }}｜<span class="stage-badge {{ rocketStageCSS $e.RocketStage }}">{{ rocketStageLabel $e.RocketStage }}</span>　飆股分數 <b>{{ $e.RocketScore }}</b>　操作 <span class="act-badge {{ watchActionCSS $e.WatchAction }}">{{ watchActionLabel $e.WatchAction }}</span>　建議觀察 {{ $e.DaysToWatch }}</div>
        <div class="wl-grid">
          <div class="wl-sec">
            <h4>① 族群輪動</h4>
            {{ if $e.HasSector }}
            <div>短線流向：<span class="{{ shortDirCSS $e.SectorFlowDir }}">{{ shortDirLabel $e.SectorFlowDir }}</span></div>
            <div>20日強度：<span class="{{ midCSS $e.SectorMidLabel }}">{{ $e.SectorMidLabel }}</span>　族群階段：{{ stageLabel $e.SectorStage }}</div>
            <div class="wl-note">{{ $e.SectorNote }}</div>
            {{ else }}<div class="wl-note">此股不在任何族群清單中，無族群輪動連動。</div>{{ end }}
          </div>
          <div class="wl-sec">
            <h4>② 型態狀態</h4>
            <div>整理型態：{{ bucketLabel $e.Consol.Bucket }}</div>
            <div>整理天數：{{ $e.Consol.Days }} 天　區間：{{ f1 $e.Consol.RangePct }}%</div>
            <div>價格壓縮：{{ f0pct $e.Consol.PriceCompressionScore }}　量縮比：{{ f2 $e.Consol.VolumeDryUpRatio }}</div>
            <div>支撐守住：{{ f0pct $e.Consol.SupportHoldScore }}　base 品質：<b>{{ f0pct $e.Consol.BaseQualityScore }}</b></div>
          </div>
          <div class="wl-sec">
            <h4>③ 回測結果</h4>
            <div>型態：{{ $e.Backtest.PatternName }}</div>
            <div>個股：{{ $e.Backtest.StockSampleCount }} 筆／勝率 {{ f0pct $e.Backtest.StockWinRate }}</div>
            <div>族群：{{ $e.Backtest.SectorSampleCount }} 筆／勝率 {{ f0pct $e.Backtest.SectorWinRate }}</div>
            <div>平均5日 <b class="{{ pnlCSS $e.Backtest.AvgReturn }}">{{ pctSign1 $e.Backtest.AvgReturn }}</b>　回撤 <b class="neg">{{ pctSign1 $e.Backtest.AvgDrawdown }}</b>　風報 {{ f2 $e.Backtest.RiskReward }}</div>
            <div>信心：<span class="{{ confCSS $e.Backtest.Confidence }}">{{ $e.Backtest.Confidence }}</span></div>
          </div>
          <div class="wl-sec">
            <h4>④ 價位計畫</h4>
            <div>現價：<b>{{ f2 $e.A.Close }}</b></div>
            <div>進場區：{{ $e.EntryZone }}</div>
            <div>突破價：<span class="t-t1">{{ f1 $e.BreakoutPrice }}</span>　支撐價：{{ f1 $e.SupportPrice }}</div>
            <div>停損價：<span class="t-stop">{{ f1 $e.StopLossPrice }}</span>　停利區：<span class="t-t2">{{ $e.TakeProfitZone }}</span></div>
          </div>
          <div class="wl-sec">
            <h4>⑤ 理由</h4>
            <ul>{{ range $e.Reasons }}<li>{{ . }}</li>{{ end }}</ul>
          </div>
          <div class="wl-sec wl-risk">
            <h4>⑥ 風險</h4>
            <div class="risk-tag">{{ $e.RiskLabel }}</div>
            <div class="wl-note">{{ $e.RiskWarning }}</div>
          </div>
        </div>
      </div>
    </td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<!-- ══ MARKET SCAN ══════════════════════════════════════════════════════════ -->
<div id="tab-market" class="tab-pane">
{{ if eq (len .Market) 0 }}
<div class="empty">市場掃描無資料（請執行 make run 或 make run-top100）</div>
{{ else }}
<table>
  <thead><tr>
    <th>#</th><th>代號</th><th>名稱</th>
    <th class="r">現價</th><th class="r">量比</th>
    <th>BFP</th><th>評分</th><th>交易建議</th>
    <th class="r t-stop">停損</th><th class="r t-t1">目標1</th><th class="r t-t2">目標2</th>
    <th class="r">RSI</th><th>MA20</th><th class="r">K</th><th class="r">D</th>
    <th>量價</th><th class="r">量分</th>
    <th>分析原因</th>
  </tr></thead>
  <tbody>
  {{ range $i, $a := .Market }}
  <tr>
    <td class="neu">{{ inc $i }}</td>
    <td class="sym">{{ $a.Symbol }}</td>
    <td class="name-col">{{ $a.Name }}</td>
    <td class="r">{{ f2 $a.Close }}</td>
    <td class="r {{ volCSS $a.VolumeRatio }}">{{ f1 $a.VolumeRatio }}x</td>
    <td>{{ bfpDots $a.BFPPoints }}</td>
    <td>{{ scoreBar $a.Score }}</td>
    <td><span class="action-badge {{ actionCSS $a.Action }}">{{ $a.Action }}</span></td>
    <td class="r t-stop">{{ f2 $a.StopLoss }}</td>
    <td class="r t-t1">{{ f2 $a.Target1 }}</td>
    <td class="r t-t2">{{ f2 $a.Target2 }}</td>
    <td class="r {{ rsiCSS $a.RSI }}">{{ f1 $a.RSI }}</td>
    <td>{{ $a.MA20Trend }}</td>
    <td class="r">{{ f1 $a.KDJK }}</td>
    <td class="r">{{ f1 $a.KDJD }}</td>
    <td class="{{ pvCSS $a.PriceVolumeSignal }}">{{ $a.PriceVolumeSignal }}</td>
    <td class="r"><span class="vol-score">{{ $a.VolumeScore }}</span></td>
    <td><div class="reasons">{{ joinReasons $a.Reasons }}</div></td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<!-- ══ ROTATION ═════════════════════════════════════════════════════════════ -->
<div id="tab-rotation" class="tab-pane">
{{ if eq (len .Rotation) 0 }}
<div class="empty">族群輪動無資料。請在 configs/sectors.yaml 設定族群與成員，並確認未加 --no-rotation。</div>
{{ else }}
<div class="rot-intro">🔄 <b>輪動</b>：不看「今天最強是誰」，而是找「下一波資金可能去哪裡」。三層架構 — <b>短線(1~5日)</b> 最早反映資金轉向、<b>20日</b> 中期強度較慢、<b>60日</b> 波段趨勢。排序混合短線流向與中期強度，讓<b>資金剛流入、20日尚未反映</b>的早期輪動族群提早浮上來，再淡化已過熱者。點族群可展開成員與結論。</div>
<table>
  <thead><tr>
    <th>#</th><th>族群</th>
    <th>短線流向</th><th>短線階段(1~5日)</th><th class="rotscore">短線分數</th>
    <th class="c">20日</th><th class="c">60日趨勢</th>
    <th class="rotscore">機會分數</th>
    <th class="r">新高%</th><th class="r">突破%</th>
    <th class="r">成員</th><th></th>
  </tr></thead>
  <tbody>
  {{ range $i, $s := .Rotation }}
  <tr class="sector-row" onclick="toggleSector({{ $i }})">
    <td class="neu">{{ inc $i }}</td>
    <td class="sym">{{ $s.Name }}</td>
    <td><span class="flow-badge {{ shortDirCSS $s.ShortTermFlowDir }}">{{ shortDirLabel $s.ShortTermFlowDir }}</span></td>
    <td><span class="stage-badge {{ shortStageCSS $s.ShortTermFlowStage }}">{{ shortStageLabel $s.ShortTermFlowStage }}</span></td>
    <td class="rotscore">{{ sectorScoreBar $s.ShortTermFlowScore }}</td>
    <td class="c {{ midCSS $s.MidTermLabel }}">{{ $s.MidTermLabel }}</td>
    <td class="c {{ trendCSS $s.TrendLabel }}">{{ $s.TrendLabel }}</td>
    <td class="rotscore">{{ sectorScoreBar $s.Score }}</td>
    <td class="r">{{ f0pct $s.NewHighRatio }}</td>
    <td class="r">{{ f0pct $s.BreakoutRatio }}</td>
    <td class="r">{{ len $s.Stocks }}</td>
    <td class="exp-caret"><span id="caret-{{ $i }}">▸</span></td>
  </tr>
  <tr class="sector-detail" id="detail-{{ $i }}">
    <td colspan="12">
      <div class="sector-meta">
        <span class="meta-concl">📌 {{ $s.ShortTermNote }}</span>
        <span class="meta-nums">短線 1/3/5日：<b class="{{ pnlCSS $s.Avg1dGain }}">{{ pctSign1 $s.Avg1dGain }}</b> / <b class="{{ pnlCSS $s.Avg3dGain }}">{{ pctSign1 $s.Avg3dGain }}</b> / <b class="{{ pnlCSS $s.Avg5dGain }}">{{ pctSign1 $s.Avg5dGain }}</b>　上漲家數 {{ f0pct $s.UpRatio }}　站上5/10MA {{ f0pct $s.AboveShortMARatio }}　創20日高 {{ f0pct $s.NewHigh20Ratio }}　量能放大 {{ f0pct $s.VolExpansion }}　｜　中期：相對強度 {{ f0pct $s.RelStrength }}　平均20日 <b class="{{ pnlCSS $s.AvgReturn20 }}">{{ pctSign1 $s.AvgReturn20 }}</b>　｜　趨勢：MA60↑ {{ f0pct $s.MA60Slope }}　站上MA60 {{ f0pct $s.AboveMA60Ratio }}</span>
      </div>
      <table class="sub-table">
        <thead><tr>
          <th>代號</th><th>名稱</th><th class="r">現價</th>
          <th class="r">5日</th><th class="r">20日報酬</th>
          <th class="c">20日高</th><th class="c">突破</th><th class="c">新高60</th>
          <th class="r">量比</th><th class="c">MA60↑</th><th class="c">資金</th><th>建議</th>
        </tr></thead>
        <tbody>
        {{ range $s.Stocks }}
        <tr>
          <td class="sym">{{ .Symbol }}</td>
          <td class="name-col">{{ .Name }}</td>
          <td class="r">{{ f2 .Close }}</td>
          <td class="r {{ pnlCSS .Gain5 }}">{{ pctSign1 .Gain5 }}</td>
          <td class="r {{ pnlCSS .Return20 }}">{{ pctSign1 .Return20 }}</td>
          <td class="c">{{ boolMark .NewHigh20 }}</td>
          <td class="c">{{ boolMark .Breakout }}</td>
          <td class="c">{{ boolMark .NewHigh }}</td>
          <td class="r {{ volCSS .VolumeRatio }}">{{ f1 .VolumeRatio }}x</td>
          <td class="c">{{ if .MA60Valid }}{{ boolMark .MA60Up }}{{ else }}<span class="no">—</span>{{ end }}</td>
          <td class="c">{{ flowArrow .MoneyFlow }}</td>
          <td><span class="action-badge {{ actionCSS .Action }}">{{ .Action }}</span></td>
        </tr>
        {{ end }}
        </tbody>
      </table>
    </td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<footer>Stock Radar｜資料來源：TWSE / Yahoo Finance｜僅供研究參考，非投資建議</footer>
</div>
<script>
function tab(e,n){
  document.querySelectorAll('.tab-btn').forEach(b=>b.classList.remove('active'));
  document.querySelectorAll('.tab-pane').forEach(p=>p.classList.remove('active'));
  e.currentTarget.classList.add('active');
  document.getElementById('tab-'+n).classList.add('active');
}
function toggleSector(i){
  var row=document.getElementById('detail-'+i);
  var caret=document.getElementById('caret-'+i);
  if(!row)return;
  var open=row.classList.toggle('open');
  if(caret)caret.textContent=open?'▾':'▸';
}
function toggleWatch(i){
  var row=document.getElementById('wdetail-'+i);
  var caret=document.getElementById('wcaret-'+i);
  if(!row)return;
  var open=row.classList.toggle('open');
  if(caret)caret.textContent=open?'▾':'▸';
}
// 飆股候選表格排序：第一次點 DESC，再點 ASC。
// score 用數字；stage/action/risk 用自訂優先級；sector 用字串（同族群內再依分數高到低）。
var wlSort={key:null,dir:null};
function sortWatch(key,th){
  var tbody=th.closest('table').querySelector('tbody');
  // 同欄再點則反向，否則預設 DESC（高到低／優先級高到低）
  var dir=(wlSort.key===key&&wlSort.dir==='desc')?'asc':'desc';
  wlSort={key:key,dir:dir};
  // 收集主列＋明細列成對
  var pairs=[];
  tbody.querySelectorAll('tr.sector-row').forEach(function(main){
    pairs.push([main,main.nextElementSibling]);
  });
  var sign=(dir==='desc')?-1:1;
  pairs.sort(function(a,b){
    var ra=a[0],rb=b[0],c;
    if(key==='sector'){
      var sa=ra.getAttribute('data-sector')||'',sb=rb.getAttribute('data-sector')||'';
      c=sa.localeCompare(sb,'zh-Hant');
      if(c!==0)return sign*c;
      // 同族群：分數高到低，方便看「某族群裡誰最強」
      return (+rb.getAttribute('data-score'))-(+ra.getAttribute('data-score'));
    }
    c=(+ra.getAttribute('data-'+key))-(+rb.getAttribute('data-'+key));
    return sign*c;
  });
  pairs.forEach(function(p,idx){
    tbody.appendChild(p[0]);
    if(p[1])tbody.appendChild(p[1]);
    var num=p[0].querySelector('.wl-num');
    if(num)num.textContent=idx+1;
  });
  // 更新表頭排序方向 icon
  th.closest('tr').querySelectorAll('th').forEach(function(h){
    h.classList.remove('sort-active');
    var ico=h.querySelector('.sort-ico');
    if(ico)ico.textContent='';
  });
  th.classList.add('sort-active');
  var ico=th.querySelector('.sort-ico');
  if(ico)ico.textContent=(dir==='desc')?'▼':'▲';
}
</script>
</body>
</html>`
