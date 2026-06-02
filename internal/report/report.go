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
	MarketLabel  string // "50" | "100" | "500" | "全部" | "-"
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
				return fmt.Sprintf("-%.0f", -v)
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
		// rsiCSS: green when oversold (<30), red when overbought (>70)
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
		fmt.Printf("\n[💼 Portfolio]\n")
		fmt.Printf("%-6s  %-10s  %5s  %7s  %7s  %12s  %8s  %-12s\n",
			"代號", "名稱", "股數", "成本", "現價", "市值(元)", "損益%", "建議")
		for _, a := range portfolio {
			fmt.Printf("%-6s  %-10s  %5d  %7.2f  %7.2f  %12.0f  %+8.1f%%  %-12s\n",
				a.Symbol, a.Name, a.Shares, a.CostBasis, a.Close,
				a.PortfolioValue(), a.PnLPct, a.Action)
		}
		sum := calcSummary(portfolio)
		fmt.Printf("  ▶ 總市值: %.0f  總成本: %.0f  損益: %.0f (%+.1f%%)\n",
			sum.TotalValue, sum.TotalCost, sum.TotalPnL, sum.TotalPnLPct)
	}

	if len(watchlist) > 0 {
		fmt.Printf("\n[👁 Watchlist]\n")
		fmt.Printf("%-6s  %-10s  %7s  %5s  %-12s\n", "代號", "名稱", "收盤", "分數", "建議")
		for _, a := range watchlist {
			fmt.Printf("%-6s  %-10s  %7.2f  %5d  %-12s\n",
				a.Symbol, a.Name, a.Close, a.Score, a.Action)
		}
	}

	if len(market) > 0 {
		fmt.Printf("\n[📊 市場掃描(%s)  實際 %d 支]\n", marketLabel, len(market))
		fmt.Printf("%-6s  %-10s  %7s  %5s  %-12s  %7s  %7s  %7s  %7s\n",
			"代號", "名稱", "收盤", "分數", "建議", "進場", "停損", "目標1", "目標2")
		for _, a := range market {
			fmt.Printf("%-6s  %-10s  %7.2f  %5d  %-12s  %7.2f  %7.2f  %7.2f  %7.2f\n",
				a.Symbol, a.Name, a.Close, a.Score, a.Action,
				a.EntryPrice, a.StopLoss, a.Target1, a.Target2)
		}
	}

	// Reasons for portfolio + watchlist
	fmt.Printf("\n[原因詳細]\n")
	focused := append(portfolio, watchlist...)
	for _, a := range focused {
		tag := "持倉"
		if a.Source == "watchlist" {
			tag = "觀察"
		}
		fmt.Printf("  %s %s [%s]:\n", a.Symbol, a.Name, tag)
		for _, r := range a.Reasons {
			if r != "" {
				fmt.Printf("    • %s\n", r)
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
<title>台股掃描 {{ .Date }}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,"PingFang TC","Noto Sans TC",sans-serif;background:#0f172a;color:#e2e8f0;min-height:100vh;font-size:13px}
.container{max-width:1700px;margin:0 auto;padding:20px 14px}
h1{font-size:1.5rem;font-weight:700;color:#f8fafc;border-bottom:2px solid #334155;padding-bottom:10px;margin-bottom:16px}
h1 small{font-size:.8rem;color:#64748b;font-weight:400;margin-left:8px}

.tabs{display:flex;gap:0;margin-bottom:20px;border-bottom:2px solid #334155}
.tab-btn{padding:10px 22px;background:none;border:none;color:#64748b;font-size:.88rem;cursor:pointer;border-bottom:2px solid transparent;margin-bottom:-2px;font-family:inherit;transition:all .15s}
.tab-btn:hover{color:#94a3b8}
.tab-btn.active{color:#38bdf8;border-bottom-color:#38bdf8;font-weight:600}
.tab-pane{display:none}
.tab-pane.active{display:block}

.cards{display:flex;gap:10px;flex-wrap:wrap;margin-bottom:16px}
.card{background:#1e293b;border:1px solid #334155;border-radius:9px;padding:12px 16px;min-width:120px}
.card-label{font-size:.7rem;color:#64748b;text-transform:uppercase;letter-spacing:.05em;margin-bottom:3px}
.card-value{font-size:1.5rem;font-weight:700;color:#38bdf8}
.card-value.pos{color:#4ade80}.card-value.neg{color:#f87171}
.card-sub{font-size:.72rem;color:#94a3b8;margin-top:1px}

table{width:100%;border-collapse:collapse;background:#1e293b;border-radius:10px;overflow:hidden}
thead{background:#0f172a}
th{padding:9px 11px;text-align:left;font-weight:600;color:#64748b;font-size:.7rem;text-transform:uppercase;letter-spacing:.04em;border-bottom:1px solid #334155;white-space:nowrap}
th.r,td.r{text-align:right}
td{padding:8px 11px;border-bottom:1px solid #1a2535;vertical-align:top}
tr:last-child td{border-bottom:none}
tr:hover td{background:#1a2744;cursor:default}

.action-badge{display:inline-block;padding:3px 8px;border-radius:4px;font-weight:700;font-size:.72rem;white-space:nowrap;letter-spacing:.03em}
.action-strong-buy{background:#14532d55;color:#4ade80;border:1px solid #16a34a77}
.action-buy{background:#15803d33;color:#86efac;border:1px solid #22c55e55}
.action-watch{background:#0c4a6e33;color:#7dd3fc;border:1px solid #0284c755}
.action-hold{background:#78350f33;color:#fcd34d;border:1px solid #d9770655}
.action-reduce{background:#9a3412aa;color:#fdba74;border:1px solid #ea580c55}
.action-sell{background:#7f1d1d55;color:#fca5a5;border:1px solid #dc262655}

.score-bar{display:flex;align-items:center;gap:5px}
.score-bar>div{height:5px;border-radius:3px;min-width:2px}
.bar-high{background:#4ade80}
.bar-mid{background:#fbbf24}
.bar-low{background:#f87171}
.score-bar span{font-weight:700;font-size:.8rem;color:#f1f5f9;min-width:26px}

.t-entry{color:#38bdf8!important}
.t-stop{color:#f87171!important}
.t-t1{color:#4ade80!important}
.t-t2{color:#a3e635!important}

.pos{color:#4ade80}.neg{color:#f87171}.neu{color:#94a3b8}
.sym{font-weight:700;color:#f8fafc}
.name-col{color:#cbd5e1}
.reasons{font-size:.72rem;color:#94a3b8;line-height:1.55;max-width:320px}

.pf-summary{background:#1e293b;border:1px solid #334155;border-radius:10px;padding:14px 20px;margin-bottom:16px;display:flex;gap:28px;flex-wrap:wrap;align-items:center}
.pf-item label{display:block;font-size:.7rem;color:#64748b;text-transform:uppercase;letter-spacing:.05em;margin-bottom:2px}
.pf-item .val{font-size:1.2rem;font-weight:700}

.empty{text-align:center;padding:32px;color:#475569;font-size:.9rem}
footer{margin-top:24px;font-size:.7rem;color:#475569;text-align:center;padding:12px 0}
</style>
</head>
<body>
<div class="container">
<h1>📈 台股掃描報告<small>{{ .Date }}</small></h1>

<div class="tabs">
  <button class="tab-btn active" onclick="tab(event,'market')">📊 市場掃描({{ .MarketLabel }})</button>
  <button class="tab-btn" onclick="tab(event,'portfolio')">💼 Portfolio ({{ len .Portfolio }})</button>
  <button class="tab-btn" onclick="tab(event,'watchlist')">👁 Watchlist ({{ len .Watchlist }})</button>
</div>

<!-- ══ MARKET ══════════════════════════════════════════════ -->
<div id="tab-market" class="tab-pane active">
{{ if eq (len .Market) 0 }}
<div class="empty">市場掃描無資料</div>
{{ else }}
<div class="cards">
  <div class="card"><div class="card-label">Top 股數</div><div class="card-value">{{ len .Market }}</div></div>
</div>
<table>
  <thead><tr>
    <th>#</th><th>代號</th><th>名稱</th>
    <th class="r">收盤</th><th class="r">量比</th>
    <th>分數</th><th>建議</th>
    <th class="r">進場價</th><th class="r">停損</th><th class="r">目標1</th><th class="r">目標2</th>
    <th class="r">RSI</th><th class="r">MA20</th><th class="r">K</th><th class="r">D</th>
    <th>MA20趨勢</th><th>原因</th>
  </tr></thead>
  <tbody>
  {{ range $i, $a := .Market }}
  <tr>
    <td class="neu">{{ inc $i }}</td>
    <td class="sym">{{ $a.Symbol }}</td>
    <td class="name-col">{{ $a.Name }}</td>
    <td class="r">{{ f2 $a.Close }}</td>
    <td class="r {{ volCSS $a.VolumeRatio }}">{{ f1 $a.VolumeRatio }}x</td>
    <td>{{ scoreBar $a.Score }}</td>
    <td><span class="action-badge {{ actionCSS $a.Action }}">{{ $a.Action }}</span></td>
    <td class="r t-entry">{{ f2 $a.EntryPrice }}</td>
    <td class="r t-stop">{{ f2 $a.StopLoss }}</td>
    <td class="r t-t1">{{ f2 $a.Target1 }}</td>
    <td class="r t-t2">{{ f2 $a.Target2 }}</td>
    <td class="r {{ rsiCSS $a.RSI }}">{{ f1 $a.RSI }}</td>
    <td class="r">{{ f2 $a.MA20 }}</td>
    <td class="r">{{ f1 $a.KDJK }}</td>
    <td class="r">{{ f1 $a.KDJD }}</td>
    <td>{{ $a.MA20Trend }}</td>
    <td><div class="reasons">{{ joinReasons $a.Reasons }}</div></td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<!-- ══ PORTFOLIO ══════════════════════════════════════════════ -->
<div id="tab-portfolio" class="tab-pane">
{{ if eq (len .Portfolio) 0 }}
<div class="empty">Portfolio 無資料。請在 stocks.yaml 新增持股。</div>
{{ else }}
<div class="pf-summary">
  <div class="pf-item">
    <label>總市值</label>
    <div class="val">{{ fmtMoney .PortfolioSum.TotalValue }}</div>
  </div>
  <div class="pf-item">
    <label>總成本</label>
    <div class="val">{{ fmtMoney .PortfolioSum.TotalCost }}</div>
  </div>
  <div class="pf-item">
    <label>損益%</label>
    <div class="val {{ pnlCSS .PortfolioSum.TotalPnL }}">{{ pctSign .PortfolioSum.TotalPnLPct }}</div>
  </div>
  <div class="pf-item">
    <label>損益額(元)</label>
    <div class="val {{ pnlCSS .PortfolioSum.TotalPnL }}">{{ fmtMoney .PortfolioSum.TotalPnL }}</div>
  </div>
</div>
<table>
  <thead><tr>
    <th>代號</th><th>名稱</th>
    <th class="r">股數</th><th class="r">成本</th><th class="r">現價</th>
    <th class="r">市值</th><th class="r">損益%</th><th class="r">損益額</th>
    <th>分數</th><th>建議</th>
    <th class="r">進場價</th><th class="r">停損</th><th class="r">目標1</th><th class="r">目標2</th>
    <th class="r">RSI</th><th>MA20趨勢</th><th class="r">K</th><th class="r">D</th>
    <th>原因與建議</th>
  </tr></thead>
  <tbody>
  {{ range .Portfolio }}
  <tr>
    <td class="sym">{{ .Symbol }}</td>
    <td class="name-col">{{ .Name }}</td>
    <td class="r">{{ .Shares }}</td>
    <td class="r">{{ f2 .CostBasis }}</td>
    <td class="r">{{ f2 .Close }}</td>
    <td class="r">{{ fmtMoney .PortfolioValue }}</td>
    <td class="r {{ pnlCSS .PnLPct }}">{{ pctSign .PnLPct }}</td>
    <td class="r {{ pnlCSS .PnLValue }}">{{ fmtMoney .PnLValue }}</td>
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
    <td><div class="reasons">{{ joinReasons .Reasons }}</div></td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<!-- ══ WATCHLIST ══════════════════════════════════════════════ -->
<div id="tab-watchlist" class="tab-pane">
{{ if eq (len .Watchlist) 0 }}
<div class="empty">Watchlist 無資料。請在 stocks.yaml 新增觀察股。</div>
{{ else }}
<table>
  <thead><tr>
    <th>代號</th><th>名稱</th>
    <th class="r">收盤</th><th class="r">量比</th>
    <th>分數</th><th>建議</th>
    <th class="r">進場價</th><th class="r">停損</th><th class="r">目標1</th><th class="r">目標2</th>
    <th class="r">RSI</th><th>MA20趨勢</th><th class="r">K</th><th class="r">D</th><th class="r">J</th>
    <th>原因與建議</th>
  </tr></thead>
  <tbody>
  {{ range .Watchlist }}
  <tr>
    <td class="sym">{{ .Symbol }}</td>
    <td class="name-col">{{ .Name }}</td>
    <td class="r">{{ f2 .Close }}</td>
    <td class="r {{ volCSS .VolumeRatio }}">{{ f1 .VolumeRatio }}x</td>
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
    <td class="r">{{ f1 .KDJJ }}</td>
    <td><div class="reasons">{{ joinReasons .Reasons }}</div></td>
  </tr>
  {{ end }}
  </tbody>
</table>
{{ end }}
</div>

<footer>資料來源：TWSE / Yahoo Finance｜僅供研究參考，非投資建議｜stock-scanner</footer>
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
