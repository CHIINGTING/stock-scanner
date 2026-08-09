/* 區間策略回測面板 — UI layer. Reads DATA (embedded dataset) and drives SBT (engine.js).
 * No external libraries: the chart is hand-drawn on a canvas so the page works offline
 * and from file:// with no CDN.
 */
(function () {
  'use strict';

  var $ = function (id) { return document.getElementById(id); };
  var BY_CODE = {};
  var LIST = [];      // [{code, name, market, label}]
  var LAST = null;    // last runAll() result
  var LASTCTX = null; // last chart context {bars, ma, cmp, sym}

  // ── formatting ───────────────────────────────────────────────────────────────
  function money(v) {
    var n = Math.round(v);
    var s = Math.abs(n).toLocaleString('en-US');
    return (n < 0 ? '-' : '') + s;
  }
  function signMoney(v) { return (v >= 0 ? '+' : '') + money(v); }
  function pct(v, d) { return (v >= 0 ? '+' : '') + v.toFixed(d === undefined ? 2 : d) + '%'; }
  function cls(v) { return v > 0 ? 'pos' : (v < 0 ? 'neg' : 'neu'); }
  function px(v) { return v.toFixed(2); }
  function esc(s) {
    return String(s).replace(/[&<>"]/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
    });
  }

  // ── dataset access ───────────────────────────────────────────────────────────
  function initIndex() {
    for (var i = 0; i < DATA.stocks.length; i++) {
      var s = DATA.stocks[i];
      BY_CODE[s.c] = s;
      LIST.push({ code: s.c, name: s.n || '', market: s.m || '', label: s.c + ' ' + (s.n || '') });
    }
    var dl = $('symList');
    // A datalist over ~2000 rows is fine; the browser filters it natively.
    dl.innerHTML = LIST.map(function (x) {
      return '<option value="' + esc(x.code) + '">' + esc(x.label) + '</option>';
    }).join('');
  }

  // Resolve free text ("2330", "台積電", "2330 台積電") to a series.
  function resolve(q) {
    q = (q || '').trim();
    if (!q) return null;
    if (BY_CODE[q]) return BY_CODE[q];
    var head = q.split(/\s+/)[0];
    if (BY_CODE[head]) return BY_CODE[head];
    var exact = null, partial = null;
    for (var i = 0; i < LIST.length; i++) {
      if (LIST[i].name === q) { exact = LIST[i]; break; }
      if (!partial && LIST[i].name && LIST[i].name.indexOf(q) >= 0) partial = LIST[i];
    }
    var hit = exact || partial;
    return hit ? BY_CODE[hit.code] : null;
  }

  // Full history of a series as parallel arrays over its own extent, gaps kept as 0
  // so moving averages refuse to average across a suspension.
  function fullSeries(s) {
    var dates = [], closes = [];
    for (var i = 0; i < s.p.length; i++) {
      dates.push(DATA.axis[s.o + i]);
      closes.push(s.p[i]);
    }
    return { dates: dates, closes: closes };
  }

  // Indexes of tradable bars inside [from, to].
  function windowIdx(fs, from, to) {
    var out = [];
    for (var i = 0; i < fs.dates.length; i++) {
      var d = fs.dates[i];
      if (d >= from && d <= to && fs.closes[i] > 0) out.push(i);
    }
    return out;
  }

  // ── inputs ───────────────────────────────────────────────────────────────────
  function readParams() {
    return {
      capital: +$('capital').value || 0,
      reserve: +$('reserve').value || 0,
      addAmount: +$('addAmount').value || 0,
      addCount: +$('addCount').value || 0,
      addDay: +$('addDay').value || 5,
      cost: {
        enabled: $('costOn').checked,
        discount: (+$('discount').value || 100) / 100
      }
    };
  }

  function setRange(months) {
    var axis = DATA.axis, to = axis[axis.length - 1];
    var from;
    if (months <= 0) {
      from = axis[0];
    } else {
      var d = new Date(to + 'T00:00:00Z');
      d.setUTCMonth(d.getUTCMonth() - months);
      from = d.toISOString().slice(0, 10);
      if (from < axis[0]) from = axis[0];
    }
    $('from').value = from;
    $('to').value = to;
    run();
  }

  // ── run ──────────────────────────────────────────────────────────────────────
  function run() {
    var s = resolve($('sym').value);
    if (!s) { fail('找不到這個代號或名稱：' + esc($('sym').value) + '。資料只包含 .cache 裡已抓過的標的。'); return; }
    $('sym').value = s.c;
    $('symName').textContent = (s.n || '—') + '（' + (s.m === 'TWO' ? '上櫃' : '上市') + '）';

    var fs = fullSeries(s);
    var from = $('from').value, to = $('to').value;
    if (!from || !to || from >= to) { fail('請確認買進日早於賣出日。'); return; }

    var idx = windowIdx(fs, from, to);
    if (idx.length < 2) {
      fail('這個區間內 ' + esc(s.c) + ' 沒有足夠的交易日資料（可用範圍 ' +
        fs.dates[0] + ' ~ ' + fs.dates[fs.dates.length - 1] + '）。');
      return;
    }

    var p = readParams();
    if (p.capital <= 0 && !(p.addAmount > 0 && p.addCount > 0)) {
      fail('請至少填「初始本金」或「定期定額（金額 + 次數）」其中一種資金來源。');
      return;
    }

    var bars = idx.map(function (i) { return { d: fs.dates[i], c: fs.closes[i] }; });
    var r = SBT.runAll(bars, p);
    if (!r) { fail('資料不足，無法回測。'); return; }
    LAST = r;

    // Moving averages come from the full series so the first plotted point is a real
    // 60-day average rather than a ramp-up artifact.
    var ma = {
      m5: SBT.sma(fs.closes, 5), m20: SBT.sma(fs.closes, 20), m60: SBT.sma(fs.closes, 60)
    };
    var cmp = buildCompare(from, to, bars);
    LASTCTX = { bars: bars, idx: idx, fs: fs, ma: ma, cmp: cmp, sym: s };

    $('err').style.display = 'none';
    $('results').style.display = 'block';
    renderSummary(r, s, cmp);
    renderMarkerPicker(r);
    drawChart();
    renderRanking(r);
    renderDetails(r);
    renderClosing(r);
  }

  function fail(msg) {
    $('err').innerHTML = '⚠️ ' + msg;
    $('err').style.display = 'block';
    $('results').style.display = 'none';
  }

  // 對照標的: normalised to 100 at the first shared bar so two different price levels
  // are comparable on one axis.
  function buildCompare(from, to, bars) {
    var q = $('cmp').value.trim();
    if (!q) return null;
    var s = resolve(q);
    if (!s) return null;
    var fs = fullSeries(s), map = {};
    for (var i = 0; i < fs.dates.length; i++) if (fs.closes[i] > 0) map[fs.dates[i]] = fs.closes[i];
    var basePx = 0, vals = [];
    for (var j = 0; j < bars.length; j++) {
      var c = map[bars[j].d] || 0;
      if (c > 0 && !basePx) basePx = c;
      vals.push(c > 0 && basePx ? (c / basePx) * 100 : 0);
    }
    if (!basePx) return null;
    var lastVal = 0;
    for (var k = vals.length - 1; k >= 0; k--) if (vals[k] > 0) { lastVal = vals[k]; break; }
    return { code: s.c, name: s.n, vals: vals, ret: lastVal ? lastVal - 100 : 0 };
  }

  // ── summary ──────────────────────────────────────────────────────────────────
  function renderSummary(r, s, cmp) {
    var champ = r.ranking.length ? r.ranking[0] : null;
    var tiles = [
      ['標的', esc(s.c) + ' ' + esc(s.n || '')],
      ['區間', r.meta.from + ' → ' + r.meta.to + '　<span class="mut">' + r.meta.bars + ' 個交易日</span>'],
      ['區間價格報酬', '<span class="' + cls(r.meta.priceRet) + '">' + pct(r.meta.priceRet) + '</span>' +
        '<div class="sub">' + px(r.meta.firstClose) + ' → ' + px(r.meta.lastClose) + '</div>'],
      ['基準：單筆抱到底', '<span class="' + cls(r.base.ret) + '">' + pct(r.base.ret) + '</span>' +
        '<div class="sub">' + signMoney(r.base.profit) + ' 元</div>'],
      ['冠軍策略', champ ? esc(champ.label) + '<div class="sub ' + cls(champ.ret) + '">' + pct(champ.ret) +
        '　' + signMoney(champ.profit) + ' 元</div>' : '—']
    ];
    if (cmp) {
      tiles.push(['對照 ' + esc(cmp.code) + ' ' + esc(cmp.name || ''),
        '<span class="' + cls(cmp.ret) + '">' + pct(cmp.ret) + '</span><div class="sub">同期間</div>']);
    }
    $('summary').innerHTML = tiles.map(function (t) {
      return '<div class="tile"><label>' + t[0] + '</label><div class="val">' + t[1] + '</div></div>';
    }).join('');

    var warn = [];
    if (r.meta.bars < 60) {
      warn.push('這個區間只有 <b>' + r.meta.bars + ' 個交易日</b>（約 ' +
        (r.meta.bars / 21).toFixed(1) + ' 個月）。區間越短，「哪個策略贏」的運氣成分越高——同一檔拉長到一年以上再跑一次，結論常常翻轉。');
    }
    if (!r.meta.costOn) {
      warn.push('目前<b>未計入交易成本</b>。頻繁進出的策略（四、五）在關掉成本時會被高估。');
    }
    if (DATA.adjusted === false) {
      warn.push('價格為<b>原始收盤價</b>，未做除權息還原；長區間、高股息標的的報酬會被低估。');
    }
    $('warn').innerHTML = warn.length ? warn.map(function (w) { return '<div class="warn">' + w + '</div>'; }).join('') : '';
  }

  // ── chart ────────────────────────────────────────────────────────────────────
  var SHOW = { price: true, m5: false, m20: true, m60: true, cmp: true, marks: true };

  function renderMarkerPicker(r) {
    var opts = [['none', '不標記']];
    opts.push(['s1', '策略一 定期定額']);
    if (r.s2 && r.s2.best) opts.push(['s2', '策略二 逢跌狙擊（最佳 ' + r.s2.best.threshold + '%）']);
    if (r.s3 && r.s3.best) opts.push(['s3', '策略三 定額+狙擊（最佳 ' + r.s3.best.threshold + '%）']);
    if (r.s4 && r.s4.best) opts.push(['s4', '策略四 停利接回（最佳 +' + r.s4.best.tp + '%/-' + r.s4.best.rb + '%）']);
    if (r.s5 && r.s5.best) opts.push(['s5', '策略五 定額+停利接回（最佳 +' + r.s5.best.tp + '%/-' + r.s5.best.rb + '%）']);
    if (r.s6 && r.s6.best) opts.push(['s6', '策略六 落袋為安（最佳 +' + r.s6.best.tp + '%）']);
    var sel = $('markSel'), prev = sel.value;
    sel.innerHTML = opts.map(function (o) { return '<option value="' + o[0] + '">' + esc(o[1]) + '</option>'; }).join('');
    sel.value = opts.some(function (o) { return o[0] === prev; }) ? prev : 's4';
    if (!sel.value) sel.value = 's1';
  }

  function markerTrades() {
    if (!LAST || !SHOW.marks) return [];
    var k = $('markSel').value, r = LAST;
    if (k === 'none') return [];
    if (k === 's1') return r.s1.trades;
    if (k === 's2' && r.s2 && r.s2.best) return r.s2.best.trades;
    if (k === 's3' && r.s3 && r.s3.best) return r.s3.best.trades;
    if (k === 's4' && r.s4 && r.s4.best) return r.s4.best.trades;
    if (k === 's5' && r.s5 && r.s5.best) return r.s5.best.trades;
    if (k === 's6' && r.s6 && r.s6.best) return r.s6.best.trades;
    return [];
  }

  function drawChart() {
    var cv = $('chart'), ctx = LASTCTX;
    if (!ctx) return;
    var dpr = window.devicePixelRatio || 1;
    var w = cv.clientWidth, h = 340;
    cv.width = w * dpr; cv.height = h * dpr;
    var g = cv.getContext('2d');
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.clearRect(0, 0, w, h);

    var padL = 52, padR = ctx.cmp && SHOW.cmp ? 46 : 14, padT = 12, padB = 26;
    var plotW = w - padL - padR, plotH = h - padT - padB;
    var bars = ctx.bars, n = bars.length;

    // Price scale spans everything that is actually drawn on the left axis.
    var lo = Infinity, hi = -Infinity;
    function span(v) { if (v > 0) { if (v < lo) lo = v; if (v > hi) hi = v; } }
    for (var i = 0; i < n; i++) {
      if (SHOW.price) span(bars[i].c);
      var fi = ctx.idx[i];
      if (SHOW.m5) span(ctx.ma.m5[fi]);
      if (SHOW.m20) span(ctx.ma.m20[fi]);
      if (SHOW.m60) span(ctx.ma.m60[fi]);
    }
    if (!isFinite(lo) || !isFinite(hi)) { for (var j = 0; j < n; j++) span(bars[j].c); }
    if (hi === lo) { hi = lo * 1.01; lo = lo * 0.99; }
    var pad = (hi - lo) * 0.08; lo -= pad; hi += pad;

    var X = function (i) { return padL + (n === 1 ? plotW / 2 : (plotW * i) / (n - 1)); };
    var Y = function (v) { return padT + plotH - ((v - lo) / (hi - lo)) * plotH; };

    // grid + price ticks
    g.font = '10px -apple-system,"PingFang TC",sans-serif';
    g.textBaseline = 'middle';
    for (var t = 0; t <= 4; t++) {
      var v = lo + ((hi - lo) * t) / 4, y = Y(v);
      g.strokeStyle = '#16233a'; g.lineWidth = 1;
      g.beginPath(); g.moveTo(padL, y); g.lineTo(padL + plotW, y); g.stroke();
      g.fillStyle = '#64748b'; g.textAlign = 'right';
      g.fillText(v.toFixed(v >= 500 ? 0 : 1), padL - 6, y);
    }
    // date ticks
    g.textAlign = 'center'; g.textBaseline = 'top';
    var ticks = Math.min(6, n);
    for (var k = 0; k < ticks; k++) {
      var bi = Math.round(((n - 1) * k) / Math.max(1, ticks - 1));
      g.fillStyle = '#64748b';
      g.fillText(bars[bi].d.slice(2).replace(/-/g, '/'), X(bi), padT + plotH + 6);
    }

    function line(getter, color, width) {
      g.strokeStyle = color; g.lineWidth = width; g.beginPath();
      var pen = false;
      for (var i2 = 0; i2 < n; i2++) {
        var v2 = getter(i2);
        if (!(v2 > 0)) { pen = false; continue; }
        var x = X(i2), y2 = Y(v2);
        if (pen) g.lineTo(x, y2); else { g.moveTo(x, y2); pen = true; }
      }
      g.stroke();
    }

    if (SHOW.m60) line(function (i) { return ctx.ma.m60[ctx.idx[i]]; }, '#a855f7', 1);
    if (SHOW.m20) line(function (i) { return ctx.ma.m20[ctx.idx[i]]; }, '#fbbf24', 1);
    if (SHOW.m5) line(function (i) { return ctx.ma.m5[ctx.idx[i]]; }, '#38bdf8', 1);

    // comparison on its own right-hand scale (index = 100 at the first shared bar)
    if (ctx.cmp && SHOW.cmp) {
      var clo = Infinity, chi = -Infinity;
      ctx.cmp.vals.forEach(function (v) { if (v > 0) { clo = Math.min(clo, v); chi = Math.max(chi, v); } });
      if (chi === clo) { chi = clo + 1; }
      var CY = function (v) { return padT + plotH - ((v - clo) / (chi - clo)) * plotH; };
      g.strokeStyle = '#64748b'; g.lineWidth = 1; g.setLineDash([4, 3]);
      g.beginPath();
      var pen2 = false;
      for (var i3 = 0; i3 < n; i3++) {
        var cv2 = ctx.cmp.vals[i3];
        if (!(cv2 > 0)) { pen2 = false; continue; }
        if (pen2) g.lineTo(X(i3), CY(cv2)); else { g.moveTo(X(i3), CY(cv2)); pen2 = true; }
      }
      g.stroke(); g.setLineDash([]);
      g.fillStyle = '#64748b'; g.textAlign = 'left'; g.textBaseline = 'middle';
      g.fillText('對照', padL + plotW + 6, CY(chi) + 8);
    }

    if (SHOW.price) line(function (i) { return bars[i].c; }, '#e2e8f0', 1.6);

    // buy/sell markers
    var trades = markerTrades();
    if (trades.length) {
      var pos = {};
      for (var m = 0; m < n; m++) pos[bars[m].d] = m;
      trades.forEach(function (tr) {
        var i4 = pos[tr.date];
        if (i4 === undefined) return;
        var x = X(i4), y = Y(bars[i4].c), buy = tr.side === 'B';
        var settle = tr.kind === '期末結算';
        g.fillStyle = settle ? '#64748b' : (buy ? '#4ade80' : '#f87171');
        g.beginPath();
        if (buy) { g.moveTo(x, y + 9); g.lineTo(x - 4.5, y + 16); g.lineTo(x + 4.5, y + 16); }
        else { g.moveTo(x, y - 9); g.lineTo(x - 4.5, y - 16); g.lineTo(x + 4.5, y - 16); }
        g.closePath(); g.fill();
      });
    }
    drawLegend();
  }

  function drawLegend() {
    var ctx = LASTCTX;
    var items = [
      ['price', '收盤價', '#e2e8f0'], ['m5', 'M5', '#38bdf8'],
      ['m20', 'M20', '#fbbf24'], ['m60', 'M60', '#a855f7']
    ];
    if (ctx && ctx.cmp) items.push(['cmp', '對照 ' + ctx.cmp.code, '#64748b']);
    items.push(['marks', '買賣點', '#4ade80']);
    $('legend').innerHTML = items.map(function (it) {
      return '<span class="lg' + (SHOW[it[0]] ? '' : ' off') + '" data-k="' + it[0] + '">' +
        '<i style="background:' + it[2] + '"></i>' + esc(it[1]) + '</span>';
    }).join('');
  }

  // ── ranking ──────────────────────────────────────────────────────────────────
  function renderRanking(r) {
    var base = r.base;
    var rows = r.ranking.map(function (x, i) {
      var medal = ['🥇', '🥈', '🥉'][i] || '　';
      var vs = x.ret - base.ret;
      return '<tr><td>' + medal + ' ' + esc(x.label) + '</td>' +
        '<td class="r">' + money(x.invested) + '</td>' +
        '<td class="r">' + money(x.finalValue) + '</td>' +
        '<td class="r ' + cls(x.profit) + '">' + signMoney(x.profit) + '</td>' +
        '<td class="r ' + cls(x.ret) + '"><b>' + pct(x.ret) + '</b></td>' +
        '<td class="r ' + cls(vs) + '">' + pct(vs) + '</td>' +
        '<td class="r neg">' + x.maxDD.toFixed(1) + '%</td>' +
        '<td class="r">' + (x.rounds || '—') + '</td></tr>';
    }).join('');
    var baseRow = '<tr class="baserow"><td>📌 基準：第一天全押、抱到期末</td>' +
      '<td class="r">' + money(base.invested) + '</td><td class="r">' + money(base.finalValue) + '</td>' +
      '<td class="r ' + cls(base.profit) + '">' + signMoney(base.profit) + '</td>' +
      '<td class="r ' + cls(base.ret) + '"><b>' + pct(base.ret) + '</b></td>' +
      '<td class="r neu">—</td><td class="r neg">' + base.maxDD.toFixed(1) + '%</td><td class="r">—</td></tr>';
    $('ranking').innerHTML =
      '<thead><tr><th>策略</th><th class="r">總投入</th><th class="r">期末總值</th><th class="r">損益</th>' +
      '<th class="r">報酬率</th><th class="r">贏基準</th><th class="r">最大回檔</th><th class="r">進出趟數</th></tr></thead>' +
      '<tbody>' + baseRow + rows + '</tbody>';
  }

  // ── strategy detail tables ───────────────────────────────────────────────────
  function gridRows(list, kind) {
    return list.map(function (x) {
      var label = kind === 'dip' ? '回檔 ' + x.threshold + '%' : '停利 +' + x.tp + '%';
      var fired = x.fired;
      return '<tr class="' + (fired ? '' : 'dim') + '">' +
        '<td>' + label + '</td>' +
        '<td>' + (fired ? (kind === 'dip' ? x.fireDate + ' @ ' + px(x.firePrice) : x.exitDate + ' @ ' + px(x.exitPrice)) : '未觸發') + '</td>' +
        '<td class="r">' + money(x.invested) + '</td>' +
        '<td class="r">' + money(x.finalValue) + '</td>' +
        '<td class="r ' + cls(x.profit) + '">' + signMoney(x.profit) + '</td>' +
        '<td class="r ' + cls(x.ret) + '"><b>' + pct(x.ret) + '</b></td>' +
        '<td class="r neg">' + x.maxDD.toFixed(1) + '%</td></tr>';
    }).join('');
  }

  function gridTable(list, kind, firstCol) {
    return '<table><thead><tr><th>' + firstCol + '</th><th>觸發</th><th class="r">總投入</th>' +
      '<th class="r">期末總值</th><th class="r">損益</th><th class="r">報酬率</th><th class="r">最大回檔</th>' +
      '</tr></thead><tbody>' + gridRows(list, kind) + '</tbody></table>';
  }

  // tp × rb matrix. Cell colour is relative to the baseline, so "green = beat holding".
  function matrixTable(grid, baseRet) {
    var byTP = {};
    grid.forEach(function (x) { (byTP[x.tp] = byTP[x.tp] || {})[x.rb] = x; });
    var best = null;
    grid.forEach(function (x) { if (x.rounds > 0 && (!best || x.ret > best.ret)) best = x; });
    var head = '<tr><th>停利 \\ 回檔接回</th>' + SBT.RB_GRID.map(function (rb) {
      return '<th class="r">-' + rb + '%</th>';
    }).join('') + '</tr>';
    var body = SBT.TP_GRID.map(function (tp) {
      var cells = SBT.RB_GRID.map(function (rb) {
        var x = byTP[tp] && byTP[tp][rb];
        if (!x) return '<td class="r">—</td>';
        if (x.rounds === 0) return '<td class="r dim" title="停利門檻未觸發">未觸發</td>';
        var edge = x.ret - baseRet;
        var a = Math.min(0.42, Math.abs(edge) / 45);
        var bg = edge >= 0 ? 'rgba(74,222,128,' + a.toFixed(3) + ')' : 'rgba(248,113,113,' + a.toFixed(3) + ')';
        var champ = best && x === best ? ' champ' : '';
        return '<td class="r cell' + champ + '" style="background:' + bg + '" title="總投入 ' + money(x.invested) +
          ' → 期末 ' + money(x.finalValue) + '／' + x.rounds + ' 趟">' +
          '<b class="' + cls(x.ret) + '">' + pct(x.ret, 1) + '</b><span class="rd">' + x.rounds + '趟</span></td>';
      }).join('');
      return '<tr><td>+' + tp + '%</td>' + cells + '</tr>';
    }).join('');
    return '<table class="matrix"><thead>' + head + '</thead><tbody>' + body + '</tbody></table>';
  }

  function tradeTable(res) {
    if (!res || !res.trades.length) return '';
    var rows = res.trades.map(function (t) {
      return '<tr><td>' + t.date + '</td>' +
        '<td><span class="side ' + (t.side === 'B' ? 'b' : 's') + '">' + (t.side === 'B' ? '買' : '賣') + '</span></td>' +
        '<td>' + esc(t.kind) + '</td>' +
        '<td class="r">' + px(t.price) + '</td>' +
        '<td class="r">' + money(t.shares) + '</td>' +
        '<td class="r">' + money(t.amount) + '</td></tr>';
    }).join('');
    return '<table class="trades"><thead><tr><th>日期</th><th>買賣</th><th>原因</th>' +
      '<th class="r">成交價</th><th class="r">股數</th><th class="r">金額</th></tr></thead><tbody>' +
      rows + '</tbody></table>';
  }

  function head(res, extra) {
    var idle = res.idleCash > 0 ? '　<span class="mut">閒置現金 ' + money(res.idleCash) + ' 元</span>' : '';
    return '<div class="reshead"><b>' + esc(res.name) + '</b>' + (extra ? '<span class="mut">　' + extra + '</span>' : '') +
      '<span class="spacer"></span>' +
      '總投入 ' + money(res.invested) + '　期末 ' + money(res.finalValue) +
      '　<span class="' + cls(res.profit) + '">' + signMoney(res.profit) + ' 元（' + pct(res.ret) + '）</span>' + idle +
      '</div>';
  }

  function section(title, subtitle, body) {
    return '<div class="card"><h2>' + title + '</h2>' +
      (subtitle ? '<p class="sub2">' + subtitle + '</p>' : '') + body + '</div>';
  }

  function renderDetails(r) {
    var out = [];

    out.push(section('💰 策略一：定期定額加碼',
      '初始本金（含未指定用途的預留現金）第一天全數進場，之後每月加碼日再固定投入一筆，全程不賣出。這是「不研究、按表操課」的底線打法。',
      head(r.s1) + tradeTable(r.s1)));

    if (r.s2) {
      out.push(section('📉 策略二：預留現金等回檔狙擊',
        '初始本金第一天進場，另外留一筆現金等待——收盤價自「區間內最高收盤」回檔達門檻時，把預留現金一次全數投入，之後抱到期末。沒觸發的門檻，預留現金就一直是現金（仍計入總投入）。',
        gridTable(r.s2.grid, 'dip', '回檔門檻') +
        (r.s2.best ? '<div class="best">最佳：' + head(r.s2.best) + tradeTable(r.s2.best) + '</div>'
          : '<div class="warn">所有回檔門檻都沒被觸發——這段區間沒有給你加碼的機會，預留現金純粹拖累報酬率。</div>')));
    }

    if (r.s3) {
      out.push(section('🧩 策略三：定期定額 + 回檔狙擊（雙資金流）',
        '資金拆三份：初始本金第一天進場、每月加碼日照買、再留一筆現金等回檔狙擊。預留現金只由回檔門檻動用，定期定額不會偷用它。',
        gridTable(r.s3.grid, 'dip', '回檔門檻') +
        (r.s3.best ? '<div class="best">最佳：' + head(r.s3.best) + tradeTable(r.s3.best) + '</div>' : '')));
    }

    if (r.s4) {
      out.push(section('🔁 策略四：停利 + 回檔接回（單筆資金）',
        '初始本金一次進場；未實現獲利（以含手續費的平均成本計）達停利門檻就全數賣出，之後股價自「賣出後最高收盤」回檔達接回門檻時，本利和全數買回，可反覆循環。下表每格是一組（停利／接回）參數的成績：<b>數字顏色＝這組是賺還是賠</b>，<b>底色＝跟基準（第一天全押抱到底）比是贏還是輸</b>，滑過去可看總投入與期末總值。',
        matrixTable(r.s4.grid, r.base.ret) +
        (r.s4.best ? '<div class="best">最佳：' + head(r.s4.best, r.s4.best.rounds + ' 趟進出') + tradeTable(r.s4.best) + '</div>'
          : '<div class="warn">沒有任何停利門檻被觸發——這段區間單筆抱到底就是答案，來回操作沒有發揮空間。</div>')));
    }

    if (r.s5) {
      out.push(section('🔁 策略五：定期定額 + 停利接回（最忙的打法）',
        '資金流同策略一（初始本金 + 每月加碼）：持股時每月照買；停利出場後，新進來的加碼先進現金池，等回檔門檻觸發時本利和連同現金池一起接回。',
        matrixTable(r.s5.grid, r.base.ret) +
        (r.s5.best ? '<div class="best">最佳：' + head(r.s5.best, r.s5.best.rounds + ' 趟進出') + tradeTable(r.s5.best) + '</div>'
          : '<div class="warn">停利門檻都沒被觸發——等於退化成策略一。</div>')));
    }

    if (r.s6) {
      out.push(section('🔒 策略六：停利落袋，出場後不再進場',
        '單筆進場，獲利達門檻就全數賣出、然後就抱著現金到期末。這一列<b>不參加排名</b>——它期末是現金，跟全程在市場的策略比報酬率並不對等。它回答的是另一個問題：「我在哪個獲利點下車，事後看會不會後悔？」',
        gridTable(r.s6.grid, 'lock', '停利門檻')));
    }

    $('details').innerHTML = out.join('');
  }

  // ── closing note ─────────────────────────────────────────────────────────────
  function renderClosing(r) {
    var champ = r.ranking.length ? r.ranking[0] : null;
    var lines = [];
    if (champ) {
      var edge = champ.ret - r.base.ret;
      var combos = (r.s4 ? r.s4.grid.length : 0) + (r.s5 ? r.s5.grid.length : 0) +
        (r.s2 ? r.s2.grid.length : 0) + (r.s3 ? r.s3.grid.length : 0) + 1;
      if (edge > 0) {
        lines.push('這個區間的冠軍是 <b>' + esc(champ.label) + '</b>，比「第一天全押抱到底」多 <b class="pos">' +
          pct(edge) + '</b>（' + signMoney(champ.profit - r.base.profit) + ' 元）。');
      } else {
        lines.push('這個區間<b>沒有任何策略贏過「第一天全押抱到底」</b>：冠軍 ' + esc(champ.label) +
          ' 仍落後基準 <b class="neg">' + pct(edge) + '</b>。加碼與停利的紀律在這段行情裡只是徒增成本。');
      }
      if (r.base.profit < 0 && champ.profit < 0) {
        lines.push('注意：這段區間<b>連冠軍都是虧的</b>，差別只在傷得深淺——這種標的與時點，控制部位比挑策略重要得多。');
      }
      lines.push('冠軍是<b>事後</b>從 ' + combos + ' 組參數裡挑出來的最佳解，實戰中你不可能每次都剛好選中它。' +
        '當冠軍只小勝基準時，選一個你<b>執行得下去</b>的規則，長期勝率反而更高。');
    }
    lines.push('報酬率一律以「損益 ÷ 總投入」計算。有定期定額時這不是時間加權報酬——後面才投入的錢在市場裡待得比較短，' +
      '所以定期定額的報酬率天生會比單筆保守，兩者不能直接當成「策略優劣」來讀。');
    $('closing').innerHTML = lines.map(function (l) { return '<p>' + l + '</p>'; }).join('') +
      '<p class="disc">※ 本面板只是把歷史收盤價依規則重播一次，用來檢查自己的紀律在過去行情下的樣子。' +
      '過去績效不代表未來，不構成投資建議，也不會幫你下單。</p>';
  }

  // ── wiring ───────────────────────────────────────────────────────────────────
  function boot() {
    initIndex();
    var axis = DATA.axis;
    ['from', 'to'].forEach(function (id) {
      $(id).min = axis[0]; $(id).max = axis[axis.length - 1];
    });
    $('dataRange').textContent = axis[0] + ' ~ ' + axis[axis.length - 1];
    $('symCount').textContent = DATA.stocks.length;

    $('runBtn').addEventListener('click', run);
    $('markSel').addEventListener('change', drawChart);
    ['sym', 'cmp', 'from', 'to', 'capital', 'reserve', 'addAmount', 'addCount', 'addDay', 'discount'].forEach(function (id) {
      $(id).addEventListener('keydown', function (e) { if (e.key === 'Enter') run(); });
    });
    $('costOn').addEventListener('change', run);
    document.querySelectorAll('[data-months]').forEach(function (b) {
      b.addEventListener('click', function () { setRange(+b.getAttribute('data-months')); });
    });
    $('legend').addEventListener('click', function (e) {
      var el = e.target.closest('.lg');
      if (!el) return;
      var k = el.getAttribute('data-k');
      SHOW[k] = !SHOW[k];
      drawChart();
    });
    var rt;
    window.addEventListener('resize', function () { clearTimeout(rt); rt = setTimeout(drawChart, 120); });

    // Default view: the most-traded large cap over the last six months, so the page
    // shows something meaningful before the user touches anything.
    $('sym').value = BY_CODE['2330'] ? '2330' : DATA.stocks[0].c;
    $('cmp').value = BY_CODE['0050'] ? '0050' : '';
    setRange(6);
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();
})();
