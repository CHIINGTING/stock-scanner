package segbacktest

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"text/template"
	"time"
)

// engine.js and panel.js are embedded verbatim so the generated page is a single
// self-contained file: no CDN, no fetch, works from file:// and from GitHub Pages.
//
//go:embed engine.js panel.js
var assets embed.FS

// RenderOptions carries the page's default form values and header metadata.
type RenderOptions struct {
	GeneratedAt time.Time
	// Defaults pre-filled into the form. Zero values fall back to sane numbers.
	Capital   int
	Reserve   int
	AddAmount int
	AddCount  int
	AddDay    int
	// Discount is the 手續費折數 in percent (e.g. 60 for 六折); 0 → 100 (no discount).
	Discount int
	// CostsOn pre-checks 計入交易成本.
	CostsOn bool
}

func (o RenderOptions) defaulted() RenderOptions {
	if o.Capital == 0 {
		o.Capital = 100000
	}
	if o.AddAmount == 0 {
		o.AddAmount = 10000
	}
	if o.AddCount == 0 {
		o.AddCount = 6
	}
	if o.AddDay == 0 {
		o.AddDay = 5
	}
	if o.Discount == 0 {
		o.Discount = 100
	}
	if o.GeneratedAt.IsZero() {
		o.GeneratedAt = time.Now()
	}
	return o
}

type pageData struct {
	Opts        RenderOptions
	DataJSON    string
	EngineJS    string
	PanelJS     string
	Generated   string
	Fetched     string
	SymbolCount int
	Bars        int
	PriceBasis  string
	CostChecked string
}

// Render writes the interactive panel. The dataset is inlined as JSON; json.Marshal
// escapes <, > and & so a stock name can never break out of the <script> block.
func Render(w io.Writer, ds *Dataset, opts RenderOptions) error {
	if ds == nil || len(ds.Stocks) == 0 {
		return fmt.Errorf("empty dataset")
	}
	opts = opts.defaulted()

	raw, err := json.Marshal(ds)
	if err != nil {
		return fmt.Errorf("marshal dataset: %w", err)
	}
	engine, err := assets.ReadFile("engine.js")
	if err != nil {
		return err
	}
	panel, err := assets.ReadFile("panel.js")
	if err != nil {
		return err
	}

	fetched := "—"
	if !ds.FetchedAt.IsZero() {
		fetched = ds.FetchedAt.Format("2006-01-02 15:04")
	}
	basis := "原始收盤價（未還原除權息）"
	if ds.Adjusted {
		basis = "還原收盤價（已調整除權息）"
	}
	checked := ""
	if opts.CostsOn {
		checked = " checked"
	}

	tpl, err := template.New("panel").Parse(panelTemplate)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, pageData{
		Opts:        opts,
		DataJSON:    string(raw),
		EngineJS:    string(engine),
		PanelJS:     string(panel),
		Generated:   opts.GeneratedAt.Format("2006-01-02 15:04"),
		Fetched:     fetched,
		SymbolCount: len(ds.Stocks),
		Bars:        ds.Bars(),
		PriceBasis:  basis,
		CostChecked: checked,
	}); err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
}

const panelTemplate = `<!DOCTYPE html>
<html lang="zh-Hant">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>區間策略回測面板 — Stock Radar</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,"PingFang TC","Noto Sans TC",sans-serif;background:#0c1220;color:#e2e8f0;min-height:100vh;font-size:13px;line-height:1.5}
.container{max-width:1240px;margin:0 auto;padding:18px 14px 40px}
h1{font-size:1.4rem;font-weight:700;color:#f8fafc;border-bottom:2px solid #1e3a5f;padding-bottom:10px;margin-bottom:6px}
h1 small{font-size:.74rem;color:#64748b;font-weight:400;margin-left:8px}
.meta{font-size:.7rem;color:#475569;margin-bottom:16px}
.meta b{color:#64748b;font-weight:600}
h2{font-size:.98rem;color:#f1f5f9;margin-bottom:6px}
.card{background:#111827;border:1px solid #1e3a5f;border-radius:10px;padding:16px 18px;margin-bottom:16px}
.sub2{font-size:.74rem;color:#94a3b8;line-height:1.75;margin-bottom:12px}
.sub2 b{color:#cbd5e1}
.mut{color:#64748b;font-weight:400}

/* form */
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px 14px}
label.f{display:block;font-size:.68rem;color:#64748b;letter-spacing:.04em;margin-bottom:4px}
input[type=text],input[type=date],input[type=number],select{width:100%;background:#0c1220;border:1px solid #1e3a5f;border-radius:6px;color:#e2e8f0;padding:7px 9px;font-size:.82rem;font-family:inherit}
input:focus,select:focus{outline:none;border-color:#38bdf8}
.hint{font-size:.66rem;color:#475569;margin-top:3px}
.fund{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:12px;margin-top:14px}
.fund-box{background:#0c1220;border:1px solid #1a2c47;border-radius:8px;padding:12px 14px}
.fund-box .t{font-size:.76rem;color:#38bdf8;font-weight:700;margin-bottom:9px}
.row2{display:grid;grid-template-columns:1fr 1fr;gap:10px}
.bar{display:flex;flex-wrap:wrap;gap:8px;align-items:center;margin-top:14px}
button{background:#0c2340;border:1px solid #0284c7;color:#7dd3fc;border-radius:6px;padding:7px 14px;font-size:.8rem;font-family:inherit;cursor:pointer;transition:all .15s}
button:hover{background:#123457;color:#bae6fd}
button.go{background:#052e16;border-color:#16a34a;color:#86efac;font-weight:700;padding:8px 22px}
button.go:hover{background:#08421f}
.chk{display:flex;align-items:center;gap:6px;font-size:.78rem;color:#94a3b8;cursor:pointer}
.chk input{width:14px;height:14px;accent-color:#38bdf8}
#symName{font-size:.78rem;color:#38bdf8;margin-top:4px;min-height:1.2em}

/* tiles */
.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:12px}
.tile{background:#0c1220;border:1px solid #1a2c47;border-radius:8px;padding:10px 12px}
.tile label{display:block;font-size:.66rem;color:#475569;letter-spacing:.05em;margin-bottom:3px}
.tile .val{font-size:.95rem;font-weight:700;color:#f1f5f9}
.tile .sub{font-size:.7rem;font-weight:400;color:#64748b;margin-top:2px}

/* chart */
#chart{width:100%;height:340px;display:block}
#legend{display:flex;flex-wrap:wrap;gap:12px;margin:8px 0 2px;font-size:.72rem;color:#94a3b8}
.lg{display:flex;align-items:center;gap:5px;cursor:pointer;user-select:none}
.lg i{width:14px;height:3px;border-radius:2px;display:inline-block}
.lg.off{opacity:.35}
.chartbar{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin-bottom:8px}
.chartbar label{font-size:.7rem;color:#64748b}
.chartbar select{width:auto;min-width:220px}

/* tables */
table{width:100%;border-collapse:collapse;background:#0c1220;border-radius:8px;overflow:hidden;margin-bottom:4px;font-size:.78rem}
thead{background:#0a1120}
th{padding:7px 9px;text-align:left;font-weight:600;color:#475569;font-size:.66rem;letter-spacing:.04em;border-bottom:1px solid #1e3a5f;white-space:nowrap}
td{padding:6px 9px;border-bottom:1px solid #131e2e;white-space:nowrap}
tr:last-child td{border-bottom:none}
tbody tr:hover td{background:#0f1d30}
th.r,td.r{text-align:right}
tr.dim td{color:#475569}
tr.baserow td{background:#101c2f;color:#cbd5e1;font-weight:600}
.pos{color:#4ade80}.neg{color:#f87171}.neu{color:#64748b}
.scroll{overflow-x:auto;-webkit-overflow-scrolling:touch}
.scroll::after{content:"";display:block}

/* matrix */
table.matrix td{text-align:right;font-variant-numeric:tabular-nums}
table.matrix td.cell{cursor:default}
table.matrix td.cell .rd{display:block;font-size:.62rem;color:#64748b;font-weight:400}
table.matrix td.champ{outline:1px solid #fbbf24;outline-offset:-1px}
table.matrix td.dim{color:#374151;font-size:.68rem}

/* trades */
table.trades{margin-top:8px}
.side{display:inline-block;padding:1px 7px;border-radius:3px;font-size:.68rem;font-weight:700}
.side.b{background:#052e16;color:#4ade80;border:1px solid #16a34a66}
.side.s{background:#3b0a0a;color:#f87171;border:1px solid #dc262666}
.reshead{display:flex;flex-wrap:wrap;gap:6px;align-items:baseline;background:#0c1220;border:1px solid #1a2c47;border-radius:8px;padding:9px 12px;font-size:.8rem;margin-bottom:6px}
.reshead .spacer{flex:1}
.best{margin-top:14px;border-top:1px dashed #1e3a5f;padding-top:12px}

.warn{background:#1c150055;border:1px solid #ca8a0455;border-radius:6px;padding:8px 12px;font-size:.74rem;color:#fcd34d;line-height:1.7;margin-bottom:8px}
#err{display:none;background:#3b0a0a55;border:1px solid #dc262677;border-radius:6px;padding:10px 14px;font-size:.8rem;color:#fca5a5;margin-bottom:14px}
#closing p{font-size:.78rem;color:#cbd5e1;line-height:1.85;margin-bottom:7px}
#closing p.disc{color:#64748b;font-size:.72rem;margin-top:10px}
footer{margin-top:18px;font-size:.68rem;color:#374151;text-align:center}
@media(max-width:640px){.container{padding:12px 10px 30px}.reshead{font-size:.74rem}}
</style>
</head>
<body>
<div class="container">

<h1>區間策略回測面板 <small>把歷史收盤價依規則重播一次，看你的紀律在過去長什麼樣</small></h1>
<div class="meta">
  資料範圍 <b id="dataRange">—</b>　·　可查標的 <b id="symCount">—</b> 檔　·
  價格基礎 <b>{{.PriceBasis}}</b>　·　快取更新 <b>{{.Fetched}}</b>　·　產生於 <b>{{.Generated}}</b>
</div>

<div id="err"></div>

<div class="card">
  <h2>① 回測設定</h2>
  <p class="sub2">
    所有進出都以<b>當日收盤價</b>成交，沒有盤中價、沒有跳空處理。股數以 1 股為單位（零股），
    買不滿的錢留在現金池。「預留現金」即使一路沒被觸發，也<b>算在總投入</b>裡——不然把錢閒置的規則會在報酬率上佔便宜。
  </p>
  <div class="grid">
    <div>
      <label class="f">標的（代號或名稱）</label>
      <input type="text" id="sym" list="symList" placeholder="2330 或 台積電" autocomplete="off">
      <datalist id="symList"></datalist>
      <div id="symName"></div>
    </div>
    <div>
      <label class="f">買進日</label>
      <input type="date" id="from">
    </div>
    <div>
      <label class="f">賣出日</label>
      <input type="date" id="to">
    </div>
    <div>
      <label class="f">初始本金（元）</label>
      <input type="number" id="capital" min="0" step="10000" value="{{.Opts.Capital}}">
      <div class="hint">第一天單筆進場的錢</div>
    </div>
    <div>
      <label class="f">對照標的（選填）</label>
      <input type="text" id="cmp" list="symList" placeholder="0050" autocomplete="off">
      <div class="hint">疊在走勢圖上，以第一天=100 正規化</div>
    </div>
  </div>

  <div class="fund">
    <div class="fund-box">
      <div class="t">💰 定期定額加碼（驅動策略一、三、五）</div>
      <div class="row2">
        <div>
          <label class="f">每次金額（元）</label>
          <input type="number" id="addAmount" min="0" step="1000" value="{{.Opts.AddAmount}}">
        </div>
        <div>
          <label class="f">加碼次數</label>
          <input type="number" id="addCount" min="0" step="1" value="{{.Opts.AddCount}}">
        </div>
      </div>
      <div style="margin-top:10px">
        <label class="f">每月幾號扣款</label>
        <input type="number" id="addDay" min="1" max="28" step="1" value="{{.Opts.AddDay}}">
        <div class="hint">遇到假日順延到下一個交易日；設 0 次即關閉</div>
      </div>
    </div>
    <div class="fund-box">
      <div class="t">🎯 預留現金等回檔（驅動策略二、三）</div>
      <div>
        <label class="f">預留現金（元）</label>
        <input type="number" id="reserve" min="0" step="10000" value="{{.Opts.Reserve}}">
        <div class="hint">收盤價自區間最高點回檔達門檻時一次全數投入；設 0 即關閉</div>
      </div>
      <div style="margin-top:10px">
        <label class="f">手續費折數（%）</label>
        <input type="number" id="discount" min="1" max="100" step="1" value="{{.Opts.Discount}}">
        <div class="hint">28 = 券商 2.8 折；100 = 不打折</div>
      </div>
    </div>
  </div>

  <div class="bar">
    <button class="go" id="runBtn">開始回測</button>
    <span class="mut">快速區間：</span>
    <button data-months="1">近 1 月</button>
    <button data-months="3">近 3 月</button>
    <button data-months="6">近 6 月</button>
    <button data-months="12">近 1 年</button>
    <button data-months="0">全部</button>
    <label class="chk"><input type="checkbox" id="costOn"{{.CostChecked}}> 計入交易成本（手續費 0.1425%×2、最低 20 元；證交稅 0.3%）</label>
  </div>
</div>

<div id="results" style="display:none">

  <div class="card">
    <h2>② 概況</h2>
    <div class="tiles" id="summary"></div>
    <div id="warn" style="margin-top:12px"></div>
  </div>

  <div class="card">
    <h2>③ 區間走勢（收盤價）</h2>
    <div class="chartbar">
      <label for="markSel">買賣點標記</label>
      <select id="markSel"></select>
      <span class="mut">▲ 買進　▼ 賣出　·　點圖例可開關線條</span>
    </div>
    <canvas id="chart"></canvas>
    <div id="legend"></div>
  </div>

  <div class="card">
    <h2>④ 策略排名</h2>
    <p class="sub2">
      以「損益 ÷ 總投入」排序。<b>贏基準</b>是跟「第一天全押、抱到期末」的報酬率差。
      策略六（落袋為安）刻意不參賽——它期末是現金，跟全程在市場的策略比報酬率並不對等。
    </p>
    <div class="scroll"><table id="ranking"></table></div>
  </div>

  <div id="details"></div>

  <div class="card">
    <h2>⑤ 結語</h2>
    <div id="closing"></div>
  </div>

</div>

<footer>Stock Radar · 區間策略回測面板 · 資料來源：本機 .cache 價格快取（Yahoo Finance）</footer>
</div>

<script id="sbt-data" type="application/json">{{.DataJSON}}</script>
<script>var DATA = JSON.parse(document.getElementById('sbt-data').textContent);</script>
<script>
{{.EngineJS}}
</script>
<script>
{{.PanelJS}}
</script>
</body>
</html>
`
