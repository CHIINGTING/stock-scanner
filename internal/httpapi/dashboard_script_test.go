package httpapi

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The dashboard's rendering rules are the §21 contract — a value with no number behind it
// must not render as one — and grepping the source for the branch that implements them only
// proves the branch is present, not that it fires.
//
// So these tests EXECUTE the page's script in a real JS engine against a DOM shim and assert
// on the tree it produces. The runtime discovery follows internal/segbacktest, which
// established this pattern in this repo: jsc (shipped with macOS) or node, and a skip rather
// than a failure when neither exists, so a bare Linux CI image does not go red over a missing
// interpreter.

var jscPaths = []string{
	"/System/Library/Frameworks/JavaScriptCore.framework/Versions/A/Helpers/jsc",
}

func jsRuntime(t *testing.T) string {
	t.Helper()
	for _, p := range jscPaths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	for _, name := range []string{"jsc", "node"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("no JS runtime (jsc/node) available — skipping dashboard rendering tests")
	return ""
}

// pageScript extracts the page's own <script> body and unwraps its IIFE.
//
// The page wraps everything in (function () { ... })() so it leaks nothing into the global
// scope — which is right for the page and inconvenient here, because the functions under test
// are then unreachable. The wrapper is removed by the HARNESS rather than by adding an export
// hook to the page: a test seam in production code is a thing that can be wrong in production,
// and this way the file that ships is exactly the file that was tested.
//
// If the wrapper ever changes shape this fails loudly rather than silently testing nothing.
func pageScript(t *testing.T) string {
	t.Helper()
	page := dashboardBody(t)
	start := strings.Index(page, "<script>")
	end := strings.LastIndex(page, "</script>")
	if start < 0 || end < 0 {
		t.Fatal("no <script> block in the page")
	}
	body := page[start+len("<script>") : end]

	openIIFE := "(function () {"
	i := strings.Index(body, openIIFE)
	if i < 0 {
		t.Fatalf("the page script is no longer wrapped in the expected IIFE; " +
			"update pageScript rather than deleting these tests")
	}
	j := strings.LastIndex(body, "})();")
	if j < 0 || j <= i {
		t.Fatal("the page script's IIFE is not closed as expected")
	}
	return body[i+len(openIIFE) : j]
}

// domShim is the smallest DOM the page actually uses. It is deliberately minimal: anything
// the page reaches for that is not here throws, which is itself the assertion that the page
// uses nothing but these primitives.
const domShim = `
if (typeof print === 'undefined') { var print = console.log; }

function Node(tag) {
  this.tagName = (tag || '').toUpperCase();
  this.children = []; this.className = ''; this._text = '';
  this.style = {}; this.attrs = {}; this.title = '';
  this.classList = {
    _n: this,
    add: function (c) { this._n.className = (this._n.className + ' ' + c).trim(); },
    remove: function (c) {
      this._n.className = this._n.className.split(/\s+/).filter(function (x) {
        return x && x !== c; }).join(' ');
    },
    contains: function (c) { return this._n.className.split(/\s+/).indexOf(c) >= 0; }
  };
}
Object.defineProperty(Node.prototype, 'textContent', {
  // In a real DOM, setting textContent then appending an element leaves BOTH a text node and
  // the element, so the getter returns their concatenation. A shim that returned only the
  // children would hide exactly the case these tests care about: a figure with a marker
  // appended after it.
  get: function () {
    return this._text + this.children.map(function (c) { return c.textContent; }).join('');
  },
  set: function (v) { this._text = String(v); this.children = []; }
});
Object.defineProperty(Node.prototype, 'firstChild', {
  get: function () { return this.children[0] || null; }
});
Node.prototype.appendChild = function (c) { this.children.push(c); return c; };
Node.prototype.setAttribute = function (k, v) { this.attrs[k] = String(v); };
Node.prototype.getAttribute = function (k) { return this.attrs[k]; };
Node.prototype.addEventListener = function () {};
Node.prototype.focus = function () {};
Node.prototype.scrollIntoView = function () {};
Node.prototype.closest = function () { return null; };
Object.defineProperty(Node.prototype, 'value', {
  get: function () { return this._value || ''; },
  set: function (v) { this._value = v; }
});

var IDS = {};
['q','results','run','asof','cost','skipai','picked','banner','out'].forEach(function (id) {
  IDS[id] = new Node('div');
});
IDS.skipai.checked = false;

var document = {
  getElementById: function (id) { return IDS[id]; },
  createElement: function (t) { return new Node(t); },
  createTextNode: function (t) { var n = new Node('#text'); n._text = String(t); return n; },
  addEventListener: function () {}
};
var window = {};
function fetch() { throw new Error('the page must not fetch during render'); }
function setTimeout(f) { return 0; }
function clearTimeout() {}
function encodeURIComponent(s) { return String(s); }
var URLSearchParams = function () {
  this.set = function () {}; this.toString = function () { return ''; };
};

// walk collects every node so a test can assert on the whole rendered tree.
function walk(n, out) {
  out = out || [];
  out.push(n);
  for (var i = 0; i < n.children.length; i++) walk(n.children[i], out);
  return out;
}
function byClass(root, cls) {
  return walk(root).filter(function (n) { return n.classList.contains(cls); });
}
`

// runPage executes the page script plus a test body, returning stdout.
func runPage(t *testing.T, body string) string {
	t.Helper()
	rt := jsRuntime(t)
	script := domShim + "\n" + pageScript(t) + "\n" + body
	f := filepath.Join(t.TempDir(), "page_test.js")
	if err := os.WriteFile(f, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(rt, f).CombinedOutput()
	if err != nil {
		t.Fatalf("run page script: %v\n%s", err, out)
	}
	return string(out)
}

// health builds a response body for the page to render.
func health(t *testing.T, mutate func(m map[string]any)) string {
	t.Helper()
	missing := func(status string) map[string]any {
		return map[string]any{"status": status, "display": status, "reason": "測試"}
	}
	num := func(v float64, disp string) map[string]any {
		return map[string]any{"status": "AVAILABLE", "value": v, "display": disp}
	}
	m := map[string]any{
		"identity": map[string]any{"symbol": "2330", "name": "台積電", "market": "TWSE",
			"industry": "半導體"},
		"as_of":        "2026-09-04",
		"data_quality": map[string]any{"level": "PARTIAL", "blocks": []any{}},
		"price": map[string]any{"status": "AVAILABLE", "bar_date": "2026-09-04", "bar_count": 486,
			"close": num(2410, "2,410.00 TWD"), "change_pct": num(-1.5, "-1.50%"),
			"volume_ratio": num(1.2, "1.20x")},
		"technical": map[string]any{"status": "AVAILABLE", "ma20": num(2400, "2,400.00 TWD"),
			"ma20_distance_pct": num(0.45, "0.45%"), "rsi": num(52.3, "52.3"),
			"atr": num(30, "30.00 TWD"), "scanner_action": "WATCH"},
		"market": map[string]any{"status": "UNAVAILABLE", "regime": ""},
		"fundamental": map[string]any{"status": "AVAILABLE",
			"revenue": num(1000, "1,000 TWD"), "revenue_yoy": num(25, "25.00%"),
			"revenue_mom": num(-3, "-3.00%"), "gross_margin": num(50, "50.00%"),
			"operating_margin": num(40, "40.00%"), "net_margin": num(30, "30.00%"),
			"eps": map[string]any{"period": "2026 H1（累計 Q1–Q2）", "note": "累計不是單季",
				"cumulative": num(49.33, "49.33 TWD"),
				"quarter":    missing("INSUFFICIENT_DATA"),
				"ttm":        missing("INSUFFICIENT_DATA"),
				"forward":    missing("NOT_IMPLEMENTED")}},
		"valuation": map[string]any{"status": "PARTIAL",
			"trailing_pe": num(27.88, "27.88x"), "pb_ratio": missing("UNAVAILABLE"),
			"dividend_yield": missing("UNAVAILABLE"), "forward_pe": missing("NOT_IMPLEMENTED"),
			"historical_pe": map[string]any{"status": "INSUFFICIENT_DATA",
				"sample_count": 1, "required_samples": 20,
				"summary":            "INSUFFICIENT_DATA（1 筆，需 20）",
				"median":             missing("INSUFFICIENT_DATA"),
				"current_percentile": missing("INSUFFICIENT_DATA")}},
		"target_price": map[string]any{"status": "INSUFFICIENT_DATA", "rule": "NONE",
			"sample_count": 1, "required_samples": 20,
			"current_price": num(2410, "2,410.00 TWD"), "eps": missing("INSUFFICIENT_DATA"),
			"margin_of_safety": missing("INSUFFICIENT_DATA"),
			"scenarios": []any{
				map[string]any{"name": "BEAR", "status": "INSUFFICIENT_DATA",
					"pe_multiple":  missing("INSUFFICIENT_DATA"),
					"target_price": missing("INSUFFICIENT_DATA"),
					"upside_pct":   missing("INSUFFICIENT_DATA")},
				map[string]any{"name": "BASE", "status": "INSUFFICIENT_DATA",
					"pe_multiple":  missing("INSUFFICIENT_DATA"),
					"target_price": missing("INSUFFICIENT_DATA"),
					"upside_pct":   missing("INSUFFICIENT_DATA")},
				map[string]any{"name": "BULL", "status": "INSUFFICIENT_DATA",
					"pe_multiple":  missing("INSUFFICIENT_DATA"),
					"target_price": missing("INSUFFICIENT_DATA"),
					"upside_pct":   missing("INSUFFICIENT_DATA")}}},
		"institution": map[string]any{"status": "AVAILABLE", "today_complete": false,
			"expected_days": 20, "observed_days": 0, "missing_dates": []any{"2026-09-04"},
			"legs": []any{map[string]any{"name": "外資",
				"today_net": missing("UNAVAILABLE"),
				"sum_5d":    map[string]any{"status": "PARTIAL", "value": 100, "display": "100 股"},
				"sum_10d":   num(200, "200 股"), "sum_20d": num(300, "300 股"),
				"consecutive_buy_days": 0, "consecutive_sell_days": 0,
				"streak_complete": false}}},
		"news": map[string]any{"status": "AVAILABLE", "items": []any{}},
		"ai":   map[string]any{"status": "UNAVAILABLE", "reason": "AI 判讀失敗"},
	}
	if mutate != nil {
		mutate(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ── §21, executed rather than grepped ─────────────────────────────────────────────────

// EVERY value with a non-AVAILABLE status must render with the .missing treatment, and no
// rendered value may look like a figure unless a real number is behind it.
//
// This walks the whole tree the page actually built. A field that forgot to go through
// valueNode shows up here even though it would pass any amount of source grepping.
func TestRenderedMissingValuesAreNeverFigures(t *testing.T) {
	out := runPage(t, `
var h = `+health(t, nil)+`;
render(h);
var vals = byClass(IDS.out, 'v');
print('COUNT ' + vals.length);
var bad = 0;
vals.forEach(function (n) {
  var txt = n.textContent;
  var isMissing = n.classList.contains('missing');
  // A node holding a status word MUST carry the missing treatment.
  if (/INSUFFICIENT_DATA|UNAVAILABLE|NOT_IMPLEMENTED|DISABLED/.test(txt) && !isMissing) {
    bad++; print('BARE-STATUS ' + txt);
  }
  // A node carrying the missing treatment must NOT contain a digit or read as a dash.
  if (isMissing) {
    if (/[0-9]/.test(txt)) { bad++; print('MISSING-WITH-DIGIT ' + txt); }
    if (txt === '-' || txt === '—' || txt.trim() === '') {
      bad++; print('MISSING-AS-DASH [' + txt + ']');
    }
  }
});
print('BAD ' + bad);
`)
	if !strings.Contains(out, "BAD 0") {
		t.Fatalf("the rendered page misrepresents a missing value:\n%s", out)
	}
	// A tree with only a handful of nodes would make "BAD 0" meaningless.
	m := regexp.MustCompile(`COUNT (\d+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no node count:\n%s", out)
	}
	if m[1] == "0" {
		t.Fatalf("nothing was rendered, so this proves nothing:\n%s", out)
	}
	t.Logf("checked %s rendered values", m[1])
}

// The specific §34 case, end to end through the real renderer: an insufficient historical
// P/E and every target scenario must reach the page as a status, never as 0 or 0x.
func TestInsufficientHistoricalPERendersAsStatus(t *testing.T) {
	out := runPage(t, `
render(`+health(t, nil)+`);
var txt = IDS.out.textContent;
print('HAS_STATUS ' + /INSUFFICIENT_DATA/.test(txt));
print('HAS_ZEROX ' + /\b0(\.00)?x\b/.test(txt));
print('HAS_ZEROPCT ' + /\b0(\.0+)?%/.test(txt));
print('SAMPLES ' + /1/.test(txt));
`)
	for _, want := range []string{"HAS_STATUS true", "HAS_ZEROX false", "HAS_ZEROPCT false"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// A hostile payload must not become markup. The page builds nodes and sets textContent; a
// name containing a tag has to come back out as text.
func TestHostileStringsStayText(t *testing.T) {
	evil := `<img src=x onerror=alert(1)>`
	out := runPage(t, `
var h = `+health(t, func(m map[string]any) {
		m["identity"].(map[string]any)["name"] = evil
		m["ai"] = map[string]any{"status": "AVAILABLE", "summary": evil,
			"bull_points": []any{evil}, "confidence": "LOW", "model": "m",
			"disclaimer": "d"}
	})+`;
render(h);
var nodes = walk(IDS.out);
var made = 0;
nodes.forEach(function (n) {
  if (n.tagName === 'IMG' || n.tagName === 'SCRIPT') made++;
});
print('ELEMENTS_FROM_STRING ' + made);
print('TEXT_PRESERVED ' + (IDS.out.textContent.indexOf('onerror') >= 0));
`)
	if !strings.Contains(out, "ELEMENTS_FROM_STRING 0") {
		t.Fatalf("server data became an element:\n%s", out)
	}
	if !strings.Contains(out, "TEXT_PRESERVED true") {
		t.Fatalf("the payload was not rendered as text at all:\n%s", out)
	}
}

// A degraded AI section must say the figures are unaffected, and must not print prose.
func TestDegradedAISectionTellsTheReaderTheFiguresStand(t *testing.T) {
	out := runPage(t, `
render(`+health(t, nil)+`);
var txt = IDS.out.textContent;
print('SAYS_UNAFFECTED ' + (txt.indexOf('不受 AI 是否可用影響') >= 0));
print('SHOWS_REASON ' + (txt.indexOf('AI 判讀失敗') >= 0));
print('STILL_HAS_PRICE ' + (txt.indexOf('2,410.00 TWD') >= 0));
`)
	for _, want := range []string{"SAYS_UNAFFECTED true", "SHOWS_REASON true", "STILL_HAS_PRICE true"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// Target-price provenance has to reach the page. Grepping for "t.rule" would pass even if
// the whole block were wrapped in if(false).
func TestTargetProvenanceReachesThePage(t *testing.T) {
	out := runPage(t, `
render(`+health(t, func(m map[string]any) {
		tp := m["target_price"].(map[string]any)
		tp["status"] = "AVAILABLE"
		tp["rule"] = "HISTORICAL_PERCENTILE"
		tp["rule_detail"] = "本股自身 25 個交易 session 的本益比分布"
		tp["eps_basis"] = "TRAILING_IMPLIED"
		tp["eps_source"] = "2026-09-01 收盤價 ÷ 同日交易所公告本益比"
		tp["eps_base_date"] = "2026-09-01"
		tp["current_date"] = "2026-09-04"
		tp["margin_of_safety_basis"] = "BASE 情境上檔空間"
	})+`);
var txt = IDS.out.textContent;
['HISTORICAL_PERCENTILE','本股自身','TRAILING_IMPLIED','2026-09-01','2026-09-04','BASE 情境上檔空間']
  .forEach(function (s) { print('HAS ' + s + ' ' + (txt.indexOf(s) >= 0)); });
`)
	if strings.Contains(out, "false") {
		t.Fatalf("provenance missing from the rendered page:\n%s", out)
	}
}

// An absent observed_days must not print "undefined".
func TestNoUndefinedReachesThePage(t *testing.T) {
	out := runPage(t, `
render(`+health(t, func(m map[string]any) {
		inst := m["institution"].(map[string]any)
		delete(inst, "observed_days") // omitempty drops it when zero
	})+`);
print('UNDEFINED ' + (IDS.out.textContent.indexOf('undefined') >= 0));
print('NAN ' + (IDS.out.textContent.indexOf('NaN') >= 0));
print('NULL ' + (IDS.out.textContent.indexOf('null') >= 0));
`)
	for _, bad := range []string{"UNDEFINED true", "NAN true", "NULL true"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s — a missing field leaked to the page:\n%s", bad, out)
		}
	}
}

// A PARTIAL value carries a real number AND a caveat, and must be visually distinct from
// both a clean figure and an absent one.
//
// The server's Qualified() is what produces this state: a 5-day institutional cumulative
// spanning a missing session is a genuine sum of what was observed. Showing it as an
// absent-value label discards a real figure; showing it clean claims a completeness the
// record does not have.
func TestPartialValuesShowTheNumberAndTheCaveat(t *testing.T) {
	out := runPage(t, `
render(`+health(t, nil)+`);
var caveats = byClass(IDS.out, 'caveat');
print('CAVEAT_COUNT ' + caveats.length);
caveats.forEach(function (n) {
  print('CAVEAT_TEXT ' + n.textContent);
  print('CAVEAT_NOT_MISSING ' + !n.classList.contains('missing'));
  print('CAVEAT_HAS_MARK ' + (byClass(n, 'caveat-mark').length > 0));
});
`)
	if strings.Contains(out, "CAVEAT_COUNT 0") {
		t.Fatalf("the PARTIAL 5-day cumulative was not rendered as a caveated figure:\n%s", out)
	}
	if !strings.Contains(out, "CAVEAT_TEXT 100 股") {
		t.Errorf("the real number was lost:\n%s", out)
	}
	if strings.Contains(out, "CAVEAT_NOT_MISSING false") {
		t.Errorf("a real number was styled as an absent value:\n%s", out)
	}
	if strings.Contains(out, "CAVEAT_HAS_MARK false") {
		t.Errorf("a tainted figure was shown with no marker:\n%s", out)
	}
}

// The layout must not depend on JS-set inline styles, so tightening style-src cannot
// silently break the tables.
func TestNoInlineStyleIsSetFromScript(t *testing.T) {
	script := pageScript(t)
	if strings.Contains(script, ".style.") {
		t.Errorf("the script sets an inline style; put it in CSS so a stricter CSP cannot break it")
	}
}

// The sample count has to be visible beside the distribution, so a median over one
// observation cannot read like a median.
func TestSampleCountIsVisibleOnThePage(t *testing.T) {
	out := runPage(t, `
render(`+health(t, nil)+`);
var txt = IDS.out.textContent;
print('SHOWS_COUNT ' + (txt.indexOf('1 筆') >= 0));
print('SHOWS_REQUIRED ' + (txt.indexOf('需 20') >= 0));
// The count used to be printed twice, once by the summary and once appended after it.
var dup = (txt.match(/需 20/g) || []).length;
print('TIMES ' + dup);
`)
	for _, want := range []string{"SHOWS_COUNT true", "SHOWS_REQUIRED true", "TIMES 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// The no-risk sentinel must not reach the reader as a risk.
//
// internal/scanner writes "—" for "no risk". It is truthy in JS, so a plain truthiness test
// printed 「風險：—」 on the price card of every unflagged stock — a line whose prefix ASSERTS
// a risk the stock does not have. The server now omits the field, and the page refuses a
// punctuation-only label; this test covers both, because a regression in either one is a
// wrong statement shown to a person about to risk money.
func TestNoRiskSentinelIsNotRenderedAsARisk(t *testing.T) {
	// 1. The field absent, which is what the server now sends.
	out := runPage(t, `
render(`+health(t, func(m map[string]any) {
		delete(m["technical"].(map[string]any), "risk_label")
	})+`);
print('HAS_RISK_LINE ' + (IDS.out.textContent.indexOf('風險：') >= 0));
`)
	if !strings.Contains(out, "HAS_RISK_LINE false") {
		t.Errorf("a risk line was drawn with no risk_label at all:\n%s", out)
	}

	// 2. The sentinel arriving anyway — a server-side regression must not reach the page.
	out = runPage(t, `
render(`+health(t, func(m map[string]any) {
		m["technical"].(map[string]any)["risk_label"] = "—"
	})+`);
var txt = IDS.out.textContent;
print('SENTINEL_SHOWN ' + (txt.indexOf('風險：—') >= 0));
print('ANY_RISK_LINE ' + (txt.indexOf('風險：') >= 0));
`)
	if strings.Contains(out, "SENTINEL_SHOWN true") || strings.Contains(out, "ANY_RISK_LINE true") {
		t.Errorf("the no-risk sentinel was rendered as a risk:\n%s", out)
	}

	// 3. A REAL label must still show, or the guard is just hiding the field.
	out = runPage(t, `
render(`+health(t, func(m map[string]any) {
		m["technical"].(map[string]any)["risk_label"] = "跌破支撐"
	})+`);
print('REAL_LABEL ' + (IDS.out.textContent.indexOf('風險：跌破支撐') >= 0));
`)
	if !strings.Contains(out, "REAL_LABEL true") {
		t.Errorf("a genuine risk label was suppressed:\n%s", out)
	}
}

// The verdict must actually reach the page, first and in plain words.
//
// It did not: the server computed the assessment, the API returned it, and the renderer never
// touched it — so the one card that answers "should I buy this" was missing while eight cards
// of evidence were shown. A reader asking for the conclusion got the raw material instead.
func TestAssessmentIsRenderedFirstAndInPlainWords(t *testing.T) {
	out := runPage(t, `
render(`+health(t, func(m map[string]any) {
		m["assessment"] = map[string]any{
			"verdict": "OVERVALUED", "decided_by": "EXPENSIVE",
			"reason": "以自身歷史評價來看已偏貴，且 BASE 情境沒有上檔空間",
			"rules": []any{
				map[string]any{"id": "NO_PRICE", "description": "沒有可用的收盤價 → INSUFFICIENT_DATA", "fired": false},
				map[string]any{"id": "HAZARD", "description": "嚴重風險 → HIGH_RISK", "fired": false},
				map[string]any{"id": "EXPENSIVE", "description": "百分位 ≥ 80 且上檔 < 0 → OVERVALUED",
					"fired": true, "detail": "本益比位於自身歷史 92 百分位"},
			},
			"inputs": map[string]any{"scanner_action": "WATCH", "margin_of_safety": "-12.0%"},
			"note":   "此判定使用獨立詞彙，與掃描器的 BUY / WATCH / SELL 不同",
		}
	})+`);
var cards = IDS.out.children;
print('FIRST_CARD ' + (cards.length ? cards[0].textContent.slice(0, 24) : '(none)'));
print('SECOND_CARD ' + (cards.length > 1 ? cards[1].textContent.slice(0, 20) : '(none)'));
var txt = IDS.out.textContent;
print('HAS_PLAIN_WORDS ' + (txt.indexOf('太貴') >= 0));
print('HAS_REASON ' + (txt.indexOf('沒有上檔空間') >= 0));
print('HAS_RULE ' + (txt.indexOf('EXPENSIVE') >= 0));
print('HAS_TRACE_DETAIL ' + (txt.indexOf('92 百分位') >= 0));
print('HAS_BOUNDARY_NOTE ' + (txt.indexOf('與掃描器的 BUY') >= 0));
print('BADGES ' + byClass(IDS.out, 'v-overvalued').length);
`)
	// The conclusion comes before the evidence: identity header, then the verdict.
	if !strings.Contains(out, "SECOND_CARD 結論") {
		t.Errorf("the verdict is not the first card after the header:\n%s", out)
	}
	for _, want := range []string{
		"HAS_PLAIN_WORDS true",  // a verdict a person can act on, not only the enum
		"HAS_REASON true",       // why
		"HAS_RULE true",         // which rule decided it
		"HAS_TRACE_DETAIL true", // the evidence that satisfied it
		"HAS_BOUNDARY_NOTE true",
		"BADGES 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// Every verdict in the vocabulary must render as words, not as a bare enum.
func TestEveryVerdictHasPlainWords(t *testing.T) {
	for _, v := range []string{"ATTRACTIVE", "ACCEPTABLE", "WAIT", "OVERVALUED",
		"HIGH_RISK", "INSUFFICIENT_DATA"} {
		out := runPage(t, `
render(`+health(t, func(m map[string]any) {
			m["assessment"] = map[string]any{"verdict": v, "reason": "r", "rules": []any{}}
		})+`);
print('TEXT ' + IDS.out.textContent.indexOf('`+v+`'));
print('BADGE ' + (byClass(IDS.out, 'badge').length));
`)
		if !strings.Contains(out, "BADGE 1") {
			t.Errorf("%s: no verdict badge rendered:\n%s", v, out)
		}
	}
}

// ── the holder's view ─────────────────────────────────────────────────────────────────

// With no cost basis the page answers only the entry question. An empty or zero input must
// not conjure a position card full of divisions by zero.
func TestNoCostBasisShowsNoPositionCard(t *testing.T) {
	for _, v := range []string{"", "0", "0.00", "abc", "-5"} {
		out := runPage(t, `
IDS.cost.value = `+jsStr(v)+`;
render(`+health(t, withTarget)+`);
print('HAS_POSITION ' + (IDS.out.textContent.indexOf('你的部位') >= 0));
print('NAN ' + (IDS.out.textContent.indexOf('NaN') >= 0));
print('INF ' + (IDS.out.textContent.indexOf('Infinity') >= 0));
`)
		if !strings.Contains(out, "HAS_POSITION false") {
			t.Errorf("cost %q produced a position card:\n%s", v, out)
		}
		for _, bad := range []string{"NAN true", "INF true"} {
			if strings.Contains(out, bad) {
				t.Errorf("cost %q: %s\n%s", v, bad, out)
			}
		}
	}
}

// With a cost basis the holder's facts appear, computed from that cost — not from the
// current price.
func TestCostBasisProducesTheHolderView(t *testing.T) {
	// EPS 20, current price 2410, BASE target 440 in the fixture below.
	out := runPage(t, `
IDS.cost.value = "1000";
render(`+health(t, withTarget)+`);
var txt = IDS.out.textContent;
print('HAS_POSITION ' + (txt.indexOf('你的部位') >= 0));
print('HAS_COST ' + (txt.indexOf('1,000.00 TWD') >= 0));
// (2410 − 1000) / 1000 = +141.00%
print('HAS_PL ' + (txt.indexOf('141.00%') >= 0));
// 1000 / 20 = 50.00x paid multiple
print('HAS_PAID_PE ' + (txt.indexOf('50.00x') >= 0));
print('SAYS_NOT_SENT ' + (txt.indexOf('不會送到伺服器') >= 0));
print('VERDICT_STILL_ENTRY ' + (txt.indexOf('該不該進場') >= 0));
`)
	for _, want := range []string{"HAS_POSITION true", "HAS_COST true", "HAS_PL true",
		"HAS_PAID_PE true", "SAYS_NOT_SENT true", "VERDICT_STILL_ENTRY true"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// The cost basis must never be sent to the server. It is the reader's own position, not
// evidence, and it has no business in a request, an archive or a database.
func TestCostBasisIsNeverSentToTheServer(t *testing.T) {
	page := dashboardBody(t)
	script := pageScript(t)
	// The only query parameters the health request may carry.
	for _, m := range regexp.MustCompile(`params\.set\(\s*"([^"]+)"`).FindAllStringSubmatch(script, -1) {
		switch m[1] {
		case "as_of", "skip_ai":
		default:
			t.Errorf("the health request carries an unexpected parameter %q", m[1])
		}
	}
	if strings.Contains(script, `params.set("cost`) || strings.Contains(script, "cost_basis") {
		t.Error("the cost basis is put into the request")
	}
	if !strings.Contains(page, "不會送到伺服器") {
		t.Error("the page does not tell the reader the cost stays local")
	}
}

// A missing EPS must not produce a fabricated paid multiple.
func TestPaidMultipleNeedsAnEPS(t *testing.T) {
	out := runPage(t, `
IDS.cost.value = "1000";
render(`+health(t, nil)+`);
var txt = IDS.out.textContent;
print('HAS_POSITION ' + (txt.indexOf('你的部位') >= 0));
print('SAYS_WHY ' + (txt.indexOf('無法回推你買在幾倍本益比') >= 0));
print('HAS_FAKE_PE ' + /你買在的本益比/.test(txt));
`)
	if !strings.Contains(out, "HAS_POSITION true") {
		t.Fatalf("no position card:\n%s", out)
	}
	if !strings.Contains(out, "SAYS_WHY true") {
		t.Errorf("the missing paid multiple is not explained:\n%s", out)
	}
	if strings.Contains(out, "HAS_FAKE_PE true") {
		t.Errorf("a paid multiple was shown with no EPS behind it:\n%s", out)
	}
}

// withTarget turns the fixture's refused target into an available one, so the holder view
// has scenarios and an EPS to work from.
func withTarget(m map[string]any) {
	num := func(v float64, disp string) map[string]any {
		return map[string]any{"status": "AVAILABLE", "value": v, "display": disp}
	}
	t := m["target_price"].(map[string]any)
	t["status"] = "AVAILABLE"
	t["rule"] = "HISTORICAL_PERCENTILE"
	t["eps"] = num(20, "20.00 TWD")
	t["current_price"] = num(2410, "2,410.00 TWD")
	t["margin_of_safety"] = num(-81.7, "-81.7%")
	t["scenarios"] = []any{
		map[string]any{"name": "BEAR", "status": "AVAILABLE",
			"pe_multiple": num(16, "16.00x"), "target_price": num(320, "320.00 TWD"),
			"upside_pct": num(-86.7, "-86.7%")},
		map[string]any{"name": "BASE", "status": "AVAILABLE",
			"pe_multiple": num(22, "22.00x"), "target_price": num(440, "440.00 TWD"),
			"upside_pct": num(-81.7, "-81.7%")},
		map[string]any{"name": "BULL", "status": "AVAILABLE",
			"pe_multiple": num(28, "28.00x"), "target_price": num(560, "560.00 TWD"),
			"upside_pct": num(-76.8, "-76.8%")},
	}
	v := m["valuation"].(map[string]any)
	v["historical_pe"].(map[string]any)["median"] = num(22, "22.00x")
	v["historical_pe"].(map[string]any)["status"] = "AVAILABLE"
}

func jsStr(s string) string { return `"` + s + `"` }

// ── FU-9: evidence quality on the page ────────────────────────────────────────────────

// qualityHealth builds a health body whose historical P/E carries a quality block.
func qualityHealth(t *testing.T, q map[string]any) string {
	t.Helper()
	num := func(v float64, disp string) map[string]any {
		return map[string]any{"status": "AVAILABLE", "value": v, "display": disp}
	}
	return health(t, func(m map[string]any) {
		hp := map[string]any{"status": "AVAILABLE", "sample_count": 26, "required_samples": 20,
			"summary":            "26 筆樣本，中位數 28.79x",
			"median":             num(28.79, "28.79x"),
			"p25":                num(28.00, "28.00x"),
			"p75":                num(30.48, "30.48x"),
			"current_percentile": num(31, "31%"),
			"quality":            q}
		m["valuation"].(map[string]any)["historical_pe"] = hp
		m["valuation"].(map[string]any)["status"] = "AVAILABLE"
	})
}

func lowQuality() map[string]any {
	return map[string]any{
		"quality": "LOW", "rule_version": "FU9-v1",
		"valid_samples": 26, "window_sessions": 225,
		"coverage":             map[string]any{"status": "AVAILABLE", "value": 11.6, "display": "11.6%"},
		"oldest_valid_session": "2026-07-31", "newest_valid_session": "2026-09-04",
		"sample_span_sessions": 26, "sample_span_days": 36,
		"iqr":          map[string]any{"status": "AVAILABLE", "value": 2.48, "display": "2.48x"},
		"relative_iqr": map[string]any{"status": "AVAILABLE", "value": 9, "display": "9%"},
		"caveats": []any{
			"歷史本益比有效樣本 26 筆，涵蓋期間內已封存的 225 個交易 session",
			"樣本 coverage 11.6%（其餘 session 交易所未公告本益比）",
			"有效樣本集中在 2026-07-31 ～ 2026-09-04，共 36 天"},
		"summary": "LOW（有效樣本 26／已封存 session 225，coverage 11.6%；規則 FU9-v1）",
	}
}

// A LOW grade must arrive with its evidence. The failure this blocks is a page that prints
// the word and nothing else: a reader told to distrust a number, with no way to judge whether
// the reason matters to the decision they are making.
func TestLowQualityRendersItsEvidenceNotJustTheGrade(t *testing.T) {
	out := runPage(t, `
render(`+qualityHealth(t, lowQuality())+`);
var txt = IDS.out.textContent;
print('GRADE ' + /LOW/.test(txt));
print('RULE ' + /FU9-v1/.test(txt));
print('VALID ' + /26 筆/.test(txt));
print('WINDOW ' + /225 個/.test(txt));
print('COVERAGE ' + /11\.6%/.test(txt));
print('OLDEST ' + /2026-07-31/.test(txt));
print('NEWEST ' + /2026-09-04/.test(txt));
print('SPAN ' + /36 天/.test(txt));
print('CAVEATS ' + byClass(IDS.out, 'qual-caveats').length);
print('PILL ' + byClass(IDS.out, 'qual').length);
`)
	for _, want := range []string{"GRADE true", "RULE true", "VALID true", "WINDOW true",
		"COVERAGE true", "OLDEST true", "NEWEST true", "SPAN true"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing from the page: %s\n%s", want, out)
		}
	}
	if strings.Contains(out, "CAVEATS 0") {
		t.Errorf("the caveats were not rendered:\n%s", out)
	}
	if strings.Contains(out, "PILL 0") {
		t.Errorf("the grade pill was not rendered:\n%s", out)
	}
}

// The target price must still be shown in full beside a LOW grade. Quality is a caveat next
// to a number, not the removal of the number.
func TestALowGradeDoesNotSuppressTheTargetPriceOnThePage(t *testing.T) {
	num := func(v float64, disp string) map[string]any {
		return map[string]any{"status": "AVAILABLE", "value": v, "display": disp}
	}
	body := health(t, func(m map[string]any) {
		hp := map[string]any{"status": "AVAILABLE", "sample_count": 26, "required_samples": 20,
			"summary": "26 筆樣本，中位數 28.79x", "median": num(28.79, "28.79x"),
			"current_percentile": num(31, "31%"), "quality": lowQuality()}
		m["valuation"].(map[string]any)["historical_pe"] = hp
		m["target_price"] = map[string]any{"status": "AVAILABLE",
			"rule": "HISTORICAL_PERCENTILE", "sample_count": 26, "required_samples": 20,
			"current_price": num(50, "50.00 TWD"), "eps": num(1.72, "1.72 TWD"),
			"margin_of_safety": num(0.1, "0.10%"),
			"scenarios": []any{map[string]any{"name": "BASE", "status": "AVAILABLE",
				"pe_multiple":  num(29.09, "29.09x"),
				"target_price": num(50.05, "50.05 TWD"),
				"upside_pct":   num(0.1, "0.10%")}}}
	})
	out := runPage(t, `
render(`+body+`);
var txt = IDS.out.textContent;
print('TARGET ' + /50\.05 TWD/.test(txt));
print('MULTIPLE ' + /29\.09x/.test(txt));
print('GRADE ' + /LOW/.test(txt));
`)
	for _, want := range []string{"TARGET true", "MULTIPLE true", "GRADE true"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s — a LOW grade suppressed the target price:\n%s", want, out)
		}
	}
}

// INSUFFICIENT has no distribution to describe. Printing a coverage of 0% and a span of
// nothing would dress an absence up as a measurement.
func TestInsufficientQualityDoesNotRenderFabricatedMetrics(t *testing.T) {
	q := map[string]any{
		"quality": "INSUFFICIENT", "rule_version": "FU9-v1",
		"valid_samples": 0, "window_sessions": 225,
		"coverage":             map[string]any{"status": "AVAILABLE", "value": 0, "display": "0.0%"},
		"sample_span_sessions": 0, "sample_span_days": 0,
		"summary": "INSUFFICIENT（沒有可用的歷史本益比樣本；規則 FU9-v1）",
	}
	out := runPage(t, `
render(`+qualityHealth(t, q)+`);
var txt = IDS.out.textContent;
print('GRADE ' + /INSUFFICIENT/.test(txt));
print('SPANZERO ' + /0 天/.test(txt));
print('CAVEATS ' + byClass(IDS.out, 'qual-caveats').length);
`)
	if !strings.Contains(out, "GRADE true") {
		t.Errorf("the grade was not rendered:\n%s", out)
	}
	if !strings.Contains(out, "SPANZERO false") {
		t.Errorf("a span of 0 days was rendered as a measurement:\n%s", out)
	}
	if !strings.Contains(out, "CAVEATS 0") {
		t.Errorf("INSUFFICIENT rendered caveats; it has no distribution to caveat:\n%s", out)
	}
}

// ── FU-11: model suitability on the page ──────────────────────────────────────────────

func weakSuitability() map[string]any {
	return map[string]any{
		"suitability": "WEAK", "rule_version": "FU11-v1",
		"reasons":         []any{"PE_AVAILABLE_ONLY_RECENTLY", "EARNINGS_SIGN_CHANGE"},
		"notes":           []any{"本益比只在近期出現：較早的 session 全部沒有，之後才全部有"},
		"earnings_sign":   "POSITIVE",
		"earnings_period": "2026 H1（累計 Q1–Q2）",
		"pe_persistence":  "RECENT_ONLY",
		"window_sessions": 225,
		"summary":         "WEAK：本益比算得出來，但作為長期主要估值錨的經濟意義偏弱（規則 FU11-v1）",
	}
}

// Data quality and model suitability are DIFFERENT axes, and the page must show both. The
// failure this blocks is one being rendered as the other — "資料品質 LOW" quietly turned into
// "P/E 不適用", which is a claim about the company that the data never made.
func TestQualityAndSuitabilityAreBothRenderedAndDistinct(t *testing.T) {
	num := func(v float64, disp string) map[string]any {
		return map[string]any{"status": "AVAILABLE", "value": v, "display": disp}
	}
	body := health(t, func(m map[string]any) {
		v := m["valuation"].(map[string]any)
		v["status"] = "AVAILABLE"
		v["historical_pe"] = map[string]any{"status": "AVAILABLE", "sample_count": 26,
			"required_samples": 20, "summary": "26 筆樣本，中位數 28.79x",
			"median": num(28.79, "28.79x"), "current_percentile": num(31, "31%"),
			"quality": lowQuality()}
		v["model_suitability"] = weakSuitability()
	})
	out := runPage(t, `
render(`+body+`);
var txt = IDS.out.textContent;
print('QUALITY_PILL ' + byClass(IDS.out, 'qual').length);
print('SUIT_PILL ' + byClass(IDS.out, 'suit').length);
print('SUIT_GRADE ' + /WEAK/.test(txt));
print('SUIT_RULE ' + /FU11-v1/.test(txt));
print('CODES ' + /PE_AVAILABLE_ONLY_RECENTLY/.test(txt));
print('PERSIST ' + /RECENT_ONLY/.test(txt));
print('EARNINGS ' + /POSITIVE/.test(txt));
print('QUALITY_GRADE ' + /LOW/.test(txt));
`)
	for _, want := range []string{"SUIT_GRADE true", "SUIT_RULE true", "CODES true",
		"PERSIST true", "EARNINGS true", "QUALITY_GRADE true"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing from the page: %s\n%s", want, out)
		}
	}
	// Two separate badges, so neither axis can be read as the other.
	if strings.Contains(out, "QUALITY_PILL 0") || strings.Contains(out, "SUIT_PILL 0") {
		t.Errorf("the two axes are not both badged:\n%s", out)
	}
}

// A WEAK verdict must not delete the target price from the page.
func TestAWeakVerdictDoesNotSuppressTheTargetPriceOnThePage(t *testing.T) {
	num := func(v float64, disp string) map[string]any {
		return map[string]any{"status": "AVAILABLE", "value": v, "display": disp}
	}
	body := health(t, func(m map[string]any) {
		v := m["valuation"].(map[string]any)
		v["status"] = "AVAILABLE"
		v["historical_pe"] = map[string]any{"status": "AVAILABLE", "sample_count": 26,
			"required_samples": 20, "summary": "26 筆樣本", "median": num(28.79, "28.79x"),
			"current_percentile": num(31, "31%"), "quality": lowQuality()}
		v["model_suitability"] = weakSuitability()
		m["target_price"] = map[string]any{"status": "AVAILABLE",
			"rule": "HISTORICAL_PERCENTILE", "sample_count": 26, "required_samples": 20,
			"current_price": num(50, "50.00 TWD"), "eps": num(1.72, "1.72 TWD"),
			"margin_of_safety": num(0.1, "0.10%"),
			"scenarios": []any{map[string]any{"name": "BASE", "status": "AVAILABLE",
				"pe_multiple":  num(29.09, "29.09x"),
				"target_price": num(50.05, "50.05 TWD"),
				"upside_pct":   num(0.1, "0.10%")}}}
	})
	out := runPage(t, `
render(`+body+`);
var txt = IDS.out.textContent;
print('TARGET ' + /50\.05 TWD/.test(txt));
print('SUIT ' + /WEAK/.test(txt));
`)
	for _, want := range []string{"TARGET true", "SUIT true"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s — a WEAK verdict suppressed the target price:\n%s", want, out)
		}
	}
}

// UNSUITABLE must not be styled as danger. It means "use a different anchor", not bad,
// overpriced, risky or sell — and the verdict palette would say all four.
func TestUnsuitableIsNotStyledAsRisk(t *testing.T) {
	body := health(t, func(m map[string]any) {
		v := m["valuation"].(map[string]any)
		v["model_suitability"] = map[string]any{
			"suitability": "UNSUITABLE", "rule_version": "FU11-v1",
			"reasons":       []any{"NO_POSITIVE_TRAILING_EARNINGS"},
			"notes":         []any{"最近一期財報顯示盈餘基準不為正"},
			"earnings_sign": "NON_POSITIVE", "pe_persistence": "NEVER",
			"window_sessions": 225, "summary": "UNSUITABLE：目前證據顯示本益比不適合作為主要估值模型",
		}
	})
	out := runPage(t, `
render(`+body+`);
var pills = byClass(IDS.out, 'suit');
print('PILLS ' + pills.length);
var bad = 0;
pills.forEach(function (p) {
  // The verdict palette's classes must never appear on a suitability badge.
  ['danger', 'risk', 'bad', 'down'].forEach(function (c) {
    if (p.classList.contains(c)) { bad++; print('RISK-STYLED ' + c); }
  });
});
print('BAD ' + bad);
print('GRADE ' + /UNSUITABLE/.test(IDS.out.textContent));
`)
	if !strings.Contains(out, "BAD 0") || !strings.Contains(out, "GRADE true") {
		t.Errorf("UNSUITABLE is rendered as a risk verdict:\n%s", out)
	}
}
