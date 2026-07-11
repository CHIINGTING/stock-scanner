package validator

// htmlTemplate is the Signal Validation Report. It intentionally leads with a
// summary and interpretation, then the BUY / REDUCE tables, best/worst cases and
// reason-tag accuracy — so the reader sees at a glance whether the scanner's
// recent judgments held up, not just a raw table.
const htmlTemplate = `<!doctype html>
<html lang="zh-Hant">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>訊號驗證報告 Signal Validation</title>
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
body{margin:0;background:#0b0f17;color:#e2e8f0;font-family:-apple-system,"Segoe UI",Roboto,"Noto Sans TC",sans-serif;line-height:1.5}
.wrap{max-width:1280px;margin:0 auto;padding:24px 18px 80px}
h1{font-size:1.5rem;margin:0 0 4px}
h2{font-size:1.15rem;margin:34px 0 10px;padding-bottom:6px;border-bottom:1px solid #1e293b}
.sub{color:#94a3b8;font-size:.85rem;margin:0 0 6px}
.note{color:#cbd5e1;font-size:.82rem;background:#111827;border:1px solid #1e293b;border-radius:8px;padding:10px 12px;margin:10px 0}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px;margin-top:14px}
.card{background:#111827;border:1px solid #1e293b;border-radius:10px;padding:12px 14px}
.card .k{color:#94a3b8;font-size:.72rem;text-transform:uppercase;letter-spacing:.04em}
.card .v{font-size:1.35rem;font-weight:700;margin-top:4px}
.card .v small{font-size:.8rem;color:#94a3b8;font-weight:400}
.hit{background:#0d1b12;border-color:#14532d}
.tablewrap{overflow-x:auto;border:1px solid #1e293b;border-radius:10px;margin-top:10px}
table{border-collapse:collapse;width:100%;font-size:.82rem;white-space:nowrap}
th,td{padding:7px 10px;text-align:right;border-bottom:1px solid #16202e}
th{background:#0f1725;color:#9fb0c3;position:sticky;top:0;font-weight:600}
td.l,th.l{text-align:left}
tbody tr:hover{background:#0f1725}
.pos{color:#4ade80}.neg{color:#f87171}.neu{color:#94a3b8}.na{color:#475569}
.pill{display:inline-block;padding:2px 8px;border-radius:5px;font-weight:700;font-size:.72rem}
.r-correct{background:#052e16;color:#4ade80;border:1px solid #16a34a}
.r-wrong{background:#3b0a0a;color:#fca5a5;border:1px solid #dc2626}
.r-neutral{background:#1c2433;color:#cbd5e1;border:1px solid #475569}
.r-pending{background:#2a220a;color:#fde047;border:1px solid #a16207}
.r-nodata{background:#161e2e;color:#64748b;border:1px solid #334155}
.reason{color:#94a3b8;font-size:.78rem;white-space:normal;max-width:340px}
.empty{color:#64748b;padding:10px 2px;font-size:.85rem}
.legend{font-size:.75rem;color:#94a3b8;margin-top:6px}
.legend .pill{margin-right:6px}
footer{margin-top:50px;color:#475569;font-size:.75rem}
</style>
</head>
<body>
<div class="wrap">
  <h1>訊號驗證報告 · Signal Validation</h1>
  <p class="sub">驗證期間 {{.Summary.Period}}　·　產生時間 {{.GeneratedAt}}</p>
  <div class="note">{{.FilterDesc}}<br>{{.Summary.BenchmarkLine}}</div>

  <h2>① 總覽 Summary</h2>
  <div class="cards">
    <div class="card"><div class="k">總訊號數</div><div class="v">{{.Summary.Total}}</div></div>
    <div class="card"><div class="k">BUY_GROUP</div><div class="v">{{.Summary.Buy}}</div></div>
    <div class="card"><div class="k">REDUCE_GROUP</div><div class="v">{{.Summary.Reduce}}</div></div>
    <div class="card"><div class="k">ENTRY_CAUTION</div><div class="v">{{.Summary.EntryCaution}}</div></div>
    <div class="card"><div class="k">WATCH_GROUP</div><div class="v">{{.Summary.Watch}}</div></div>
    <div class="card"><div class="k">可驗證訊號</div><div class="v">{{.Summary.Validatable}}</div></div>
    <div class="card"><div class="k">資料不足</div><div class="v">{{.Summary.Insufficient}}</div></div>
    <div class="card hit"><div class="k">整體命中率</div><div class="v">{{.Summary.OverallHit}}</div></div>
    <div class="card"><div class="k">BUY T+5 上漲率</div><div class="v"><small>{{.Summary.BuyT5}}</small></div></div>
    <div class="card"><div class="k">BUY T+10 上漲率</div><div class="v"><small>{{.Summary.BuyT10}}</small></div></div>
    <div class="card"><div class="k">REDUCE T+5 走弱率</div><div class="v"><small>{{.Summary.ReduceT5}}</small></div></div>
    <div class="card"><div class="k">REDUCE T+10 走弱率</div><div class="v"><small>{{.Summary.ReduceT10}}</small></div></div>
    {{if .HasEntryCaution}}<div class="card"><div class="k">過熱警告命中率</div><div class="v"><small>{{.Summary.EntryCautionHit}}</small></div></div>{{end}}
  </div>
  <div class="legend">
    <span class="pill r-correct">CORRECT</span><span class="pill r-wrong">WRONG</span>
    <span class="pill r-neutral">NEUTRAL</span><span class="pill r-pending">PENDING</span>
    <span class="pill r-nodata">NO_PRICE_DATA</span>
    整體命中率 = CORRECT / (CORRECT+WRONG)，不含 NEUTRAL / PENDING / 觀察類。
  </div>

  <h2>② 加買 / 買進訊號驗證 BUY_GROUP</h2>
  {{template "vtable" dict "Rows" .BuyRows "H" .HorizonLabels}}

  <h2>③ 減碼 / 賣出 / 避開訊號驗證 REDUCE_GROUP</h2>
  {{template "vtable" dict "Rows" .ReduceRows "H" .HorizonLabels}}

  {{if .HasEntryCaution}}
  <h2>③′ 過熱 / 追高警告驗證 ENTRY_CAUTION_GROUP</h2>
  <div class="note">watchlist 的 <b>OVERHEATED；追高</b> 是「太熱、別追、等回檔」的<strong>進場風險警告</strong>，不是看空出場。
  故不以「後續是否下跌」評分，改判：<b>CORRECT</b>＝隨後出現回檔（最大回檔≤−5%）提供更好進場點、或短線走弱避開追高；
  <b>WRONG</b>＝沒回檔就直接噴出（T+10≥12% 或 T+5≥15%），代表警告太嚴、錯失主升段。此組不計入整體命中率。</div>
  {{template "vtable" dict "Rows" .EntryCautionRows "H" .HorizonLabels}}

  {{if .WrongEntryCaution}}
  <p class="sub">下列為「喊過熱卻直接噴出」的樣本——這正是 scanner OVERHEATED 門檻該放寬 / 改等回檔的證據。</p>
  {{template "vtable" dict "Rows" .WrongEntryCaution "H" .HorizonLabels}}
  {{end}}
  {{end}}

  <h2>④ 最佳正確案例 Best Correct Cases</h2>
  <p class="sub">BUY：後續漲幅最大且判斷正確　·　REDUCE：後續跌幅最大且判斷正確</p>
  {{template "vtable" dict "Rows" .BestBuy "H" .HorizonLabels}}
  {{template "vtable" dict "Rows" .BestReduce "H" .HorizonLabels}}

  <h2>⑤ 錯誤案例 Wrong Cases</h2>
  <p class="sub">加買後下跌、或減碼後大漲，這些是最需要檢討與回饋權重的樣本。</p>
  {{template "vtable" dict "Rows" .WrongBuy "H" .HorizonLabels}}
  {{template "vtable" dict "Rows" .WrongReduce "H" .HorizonLabels}}

  {{if .HasReason}}
  <h2>⑥ 理由標籤命中率 Reason Tag Accuracy</h2>
  <div class="tablewrap"><table>
    <thead><tr>
      <th class="l">Reason Tag</th><th>出現次數</th><th>BUY 命中率</th><th>REDUCE 命中率</th>
      <th>平均 T+5</th><th>平均 T+10</th>
    </tr></thead>
    <tbody>
    {{range .ReasonRows}}
      <tr><td class="l">{{.Tag}}</td><td>{{.Count}}</td><td>{{.BuyHit}}</td><td>{{.ReduceHit}}</td>
      <td>{{.AvgT5}}</td><td>{{.AvgT10}}</td></tr>
    {{end}}
    </tbody>
  </table></div>
  {{end}}

  {{if .HasWatch}}
  <h2>⑦ 觀察類訊號追蹤 WATCH_GROUP（不計入命中率）</h2>
  {{template "vtable" dict "Rows" .WatchRows "H" .HorizonLabels}}
  {{end}}

  <footer>Signal Validation Report · 由 cmd/validate-signals 產生 · 尊重報告當日原始判斷，不使用今日邏輯回溯過去。</footer>
</div>

{{define "vtable"}}
{{if .Rows}}
<div class="tablewrap"><table>
  <thead><tr>
    <th class="l">日期</th><th class="l">代號</th><th class="l">名稱</th><th class="l">Action</th><th class="l">Stage</th>
    <th>Signal</th><th>Entry</th>
    {{range .H}}<th>{{.}}</th>{{end}}
    <th>MaxDD</th><th>Result</th><th class="l">Reason</th><th class="l">來源</th>
  </tr></thead>
  <tbody>
  {{range .Rows}}
    <tr>
      <td class="l">{{.Date}}</td><td class="l">{{.Code}}</td><td class="l">{{.Name}}</td>
      <td class="l">{{.Action}}</td><td class="l">{{.Stage}}</td>
      <td>{{.SignalPrice}}</td><td>{{.EntryPrice}}</td>
      {{range .Rets}}<td class="{{.Cls}}">{{.Text}}</td>{{end}}
      <td class="{{.MaxDD.Cls}}">{{.MaxDD.Text}}</td>
      <td><span class="pill {{.ResultCls}}">{{.Result}}</span></td>
      <td class="l reason">{{.Reason}}</td>
      <td class="l">{{.Source}}</td>
    </tr>
  {{end}}
  </tbody>
</table></div>
{{else}}
<p class="empty">（無符合資料）</p>
{{end}}
{{end}}
</body>
</html>`
