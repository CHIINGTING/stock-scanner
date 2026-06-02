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
	Watchlist    []scanner.StockAnalysis
	PortfolioSum PortfolioSummary
}

func (r *Report) Generate(
	market, portfolio, watchlist []scanner.StockAnalysis,
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
			default:
				return "pv-down-vol-down"
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
	printConsole(market, portfolio, watchlist, marketLabel, date)
	return nil
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

func printConsole(market, portfolio, watchlist []scanner.StockAnalysis, marketLabel string, date time.Time) {
	sep := "═══════════════════════════════════════════════════════════════════"
	fmt.Printf("\n%s\n  台股掃描報告  %s\n%s\n", sep, date.Format("2006-01-02"), sep)

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
		fmt.Printf("\n[👁 觀察清單 (Watchlist)]\n")
		fmt.Printf("%-6s  %-8s  %7s  %4s  %3s/5  %-12s  %7s  %7s  %7s\n",
			"代號", "名稱", "現價", "分數", "BFP", "建議", "進場", "停損", "目標1")
		for _, a := range watchlist {
			fmt.Printf("%-6s  %-8s  %7.2f  %4d  %5d  %-12s  %7.2f  %7.2f  %7.2f\n",
				a.Symbol, a.Name, a.Close, a.Score, a.BFPPoints, a.Action,
				a.EntryPrice, a.StopLoss, a.Target1)
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

	// Detailed reasons for positions + watchlist
	fmt.Printf("\n[原因詳細]\n")
	for _, a := range append(portfolio, watchlist...) {
		tag := "持倉"
		if a.Source == "watchlist" {
			tag = "觀察"
		}
		fmt.Printf("  %s %s [%s]:\n", a.Symbol, a.Name, tag)
		for _, r := range a.Reasons {
			if r != "" {
				fmt.Printf("    %s\n", r)
			}
		}
		fmt.Printf("    → 進場 %.2f  停損 %.2f  目標1 %.2f  目標2 %.2f\n\n",
			a.EntryPrice, a.StopLoss, a.Target1, a.Target2)
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
</style>
</head>
<body>
<div class="container">
<h1>📡 股票雷達<small>{{ .Date }} 盤後分析</small></h1>

<div class="tabs">
  <button class="tab-btn active" onclick="tab(event,'positions')">💼 持倉 ({{ len .Portfolio }})</button>
  <button class="tab-btn" onclick="tab(event,'watchlist')">👁 觀察清單 ({{ len .Watchlist }})</button>
  <button class="tab-btn" onclick="tab(event,'market')">📊 市場掃描({{ .MarketLabel }})</button>
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

<!-- ══ WATCHLIST ════════════════════════════════════════════════════════════ -->
<div id="tab-watchlist" class="tab-pane">
{{ if eq (len .Watchlist) 0 }}
<div class="empty">觀察清單無資料。請在 stocks.yaml 的 watchlist: 區段加入股票。</div>
{{ else }}
<table>
  <thead><tr>
    <th>代號</th><th>名稱</th>
    <th class="r">現價</th><th class="r">量比</th>
    <th>BFP</th><th>評分</th><th>交易建議</th>
    <th class="r t-entry">建議進場</th><th class="r t-stop">停損</th>
    <th class="r t-t1">目標1</th><th class="r t-t2">目標2</th>
    <th class="r">RSI</th><th>MA20</th><th class="r">K</th><th class="r">D</th>
    <th>量價</th><th class="r">量分</th>
    <th>分析原因</th>
  </tr></thead>
  <tbody>
  {{ range .Watchlist }}
  <tr>
    <td class="sym">{{ .Symbol }}</td>
    <td class="name-col">{{ .Name }}</td>
    <td class="r">{{ f2 .Close }}</td>
    <td class="r {{ volCSS .VolumeRatio }}">{{ f1 .VolumeRatio }}x</td>
    <td>{{ bfpDots .BFPPoints }}</td>
    <td>{{ scoreBar .Score }}</td>
    <td><span class="action-badge {{ actionCSS .Action }}">{{ .Action }}</span></td>
    <td class="r t-entry">{{ f2 .EntryPrice }}</td>
    <td class="r t-stop">{{ f2 .StopLoss }}</td>
    <td class="r t-t1">{{ f2 .Target1 }}</td>
    <td class="r t-t2">{{ f2 .Target2 }}</td>
    <td class="r {{ rsiCSS .RSI }}">{{ f1 .RSI }}</td>
    <td>{{ .MA20Trend }}</td>
    <td class="r">{{ f1 .KDJK }}</td>
    <td class="r">{{ f1 .KDJD }}</td>
    <td class="{{ pvCSS .PriceVolumeSignal }}">{{ .PriceVolumeSignal }}</td>
    <td class="r"><span class="vol-score">{{ .VolumeScore }}</span></td>
    <td><div class="reasons">{{ joinReasons .Reasons }}</div></td>
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

<footer>Stock Radar｜資料來源：TWSE / Yahoo Finance｜僅供研究參考，非投資建議</footer>
</div>
<script>
function tab(e,n){
  document.querySelectorAll('.tab-btn').forEach(b=>b.classList.remove('active'));
  document.querySelectorAll('.tab-pane').forEach(p=>p.classList.remove('active'));
  e.currentTarget.classList.add('active');
  document.getElementById('tab-'+n).classList.add('active');
}
</script>
</body>
</html>`
