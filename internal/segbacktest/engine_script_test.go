package segbacktest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The strategy engine lives in JS because the panel has to recompute on every input
// change in the browser. To keep it from being untested, these tests run engine.js in
// a real JS engine — jsc (shipped with macOS) or node — and assert on printed output.
//
// If neither runtime exists the tests skip rather than fail; CI on a bare Linux image
// should not go red over a missing interpreter.

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
	t.Skip("no JS runtime (jsc/node) available — skipping engine.js tests")
	return ""
}

// jsPrelude makes the same script work under jsc (print) and node (console.log), and
// gives tests a tiny assert plus a trading-day series builder.
const jsPrelude = `
if (typeof print === 'undefined') { var print = console.log; }
var FAILED = 0;
function eq(name, got, want) {
  var g = String(got), w = String(want);
  if (g !== w) { FAILED++; print('FAIL ' + name + ': got ' + g + ' want ' + w); }
}
function near(name, got, want, tol) {
  if (Math.abs(got - want) > tol) { FAILED++; print('FAIL ' + name + ': got ' + got + ' want ~' + want); }
}
// mk builds bars on consecutive calendar days from 'start'.
function mk(closes, start) {
  var d = new Date(start + 'T00:00:00Z'), out = [];
  for (var i = 0; i < closes.length; i++) {
    out.push({ d: d.toISOString().slice(0, 10), c: closes[i] });
    d = new Date(d.getTime() + 86400000);
  }
  return out;
}
function ramp(from, step, n) {
  var a = [];
  for (var i = 0; i < n; i++) a.push(from + step * i);
  return a;
}
var NOCOST = { enabled: false };
`

const jsEpilogue = `
print(FAILED === 0 ? 'ALL OK' : 'FAILURES ' + FAILED);
`

// runJS concatenates engine.js + prelude + body and executes it, returning stdout.
func runJS(t *testing.T, body string) string {
	t.Helper()
	rt := jsRuntime(t)
	engine, err := os.ReadFile("engine.js")
	if err != nil {
		t.Fatalf("read engine.js: %v", err)
	}
	path := filepath.Join(t.TempDir(), "combined.js")
	src := string(engine) + "\n" + jsPrelude + "\n" + body + "\n" + jsEpilogue
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	out, err := exec.Command(rt, path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", filepath.Base(rt), err, out)
	}
	return string(out)
}

// assertJS runs a JS body that uses eq/near and fails the Go test on any JS assertion
// failure, surfacing the offending lines.
func assertJS(t *testing.T, name, body string) {
	t.Helper()
	out := runJS(t, body)
	if !strings.Contains(out, "ALL OK") {
		t.Errorf("%s: JS assertions failed\n%s", name, out)
	}
}

func TestJSCostModel(t *testing.T) {
	assertJS(t, "cost", `
var c = SBT.makeCost({});
eq('fee 100k', SBT.buyFee(c, 100000), 143);              // round(142.5)
eq('fee min', SBT.buyFee(c, 1000), 20);                  // 1.425 -> floor at 20
eq('sell 100k', SBT.sellCharges(c, 100000), 143 + 300);  // fee + 0.3% tax
eq('discount', SBT.buyFee(SBT.makeCost({discount:0.6}), 100000), 86); // round(85.5)
var off = SBT.makeCost({enabled:false});
eq('off buy', SBT.buyFee(off, 100000), 0);
eq('off sell', SBT.sellCharges(off, 100000), 0);
// Budget must cover fees: 10000 at 100 buys 99 shares, not 100.
eq('afford with fee', SBT.affordableShares(c, 100, 10000), 99);
eq('afford no fee', SBT.affordableShares(off, 100, 10000), 100);
eq('afford broke', SBT.affordableShares(c, 100, 50), 0);
`)
}

func TestJSBuyHoldMatchesPriceReturn(t *testing.T) {
	// With costs off and a price that divides the capital evenly, buy & hold must
	// reproduce the raw price return exactly — the sanity anchor for everything else.
	assertJS(t, "buyhold", `
var bars = mk([100, 110, 90, 120], '2026-01-01');
var r = SBT.buyHold(bars, {capital:100000, reserve:0, addAmount:0, addCount:0, addDay:5, cost:SBT.makeCost(NOCOST)});
eq('invested', r.invested, 100000);
eq('final', r.finalValue, 120000);
eq('profit', r.profit, 20000);
near('ret', r.ret, 20, 1e-9);
near('maxDD', r.maxDD, -18.181818, 1e-4);   // 110 -> 90
eq('trades', r.trades.length, 2);            // 買進 + 期末結算
eq('settled flat', r.trades[1].kind, '期末結算');
// Reserve is committed on day 0 for buy & hold, so it is invested, not idle.
var r2 = SBT.buyHold(bars, {capital:60000, reserve:40000, addAmount:0, addCount:0, addDay:5, cost:SBT.makeCost(NOCOST)});
eq('reserve all-in', r2.invested, 100000);
eq('reserve final', r2.finalValue, 120000);
`)
}

func TestJSMonthlyAddSchedule(t *testing.T) {
	assertJS(t, "schedule", `
// Day 0 is 2026-01-01; the January add would land on 01-05 (index 4).
var bars = mk(ramp(100, 0, 100), '2026-01-01');
eq('jan+feb+mar', JSON.stringify(SBT.monthlyAddIndexes(bars, 5, 3)), '[4,35,63]');
eq('capped', JSON.stringify(SBT.monthlyAddIndexes(bars, 5, 1)), '[4]');
eq('zero count', JSON.stringify(SBT.monthlyAddIndexes(bars, 5, 0)), '[]');
// Buying ON the add day must not double-invest: start 2026-01-05, first add is Feb.
var b2 = mk(ramp(100, 0, 100), '2026-01-05');
eq('skip bar0', JSON.stringify(SBT.monthlyAddIndexes(b2, 5, 2)), '[31,59]');
// A later add day still resolves to the first bar on/after it.
eq('day 20', JSON.stringify(SBT.monthlyAddIndexes(bars, 20, 2)), '[19,50]');
`)
}

func TestJSDCAInvestedAndFills(t *testing.T) {
	assertJS(t, "dca", `
var bars = mk(ramp(100, 0, 100), '2026-01-01');
var p = {capital:100000, reserve:0, addAmount:10000, addCount:3, addDay:5, cost:SBT.makeCost(NOCOST)};
var r = SBT.dca(bars, p);
eq('invested', r.invested, 130000);          // 100k + 3 x 10k
eq('final flat price', r.finalValue, 130000); // price never moved
var buys = r.trades.filter(function(t){return t.side==='B';});
eq('buy count', buys.length, 4);
eq('first kind', buys[0].kind, '初始資金');
eq('add kind', buys[1].kind, '定期定額');
eq('add date', buys[1].date, '2026-01-05');
// addCount beyond the window length simply runs out of months.
var r2 = SBT.dca(mk(ramp(100, 0, 40), '2026-01-01'), {capital:100000, reserve:0, addAmount:10000, addCount:12, addDay:5, cost:SBT.makeCost(NOCOST)});
eq('capped invested', r2.invested, 120000);  // only Jan + Feb bars exist
`)
}

func TestJSDipSnipe(t *testing.T) {
	assertJS(t, "dip", `
var p = {capital:100000, reserve:50000, addAmount:0, addCount:0, addDay:5, cost:SBT.makeCost(NOCOST)};
// Peak 110 then 99 = -10.0% from peak. A 10% rule fires; a 15% rule does not.
var bars = mk([100, 110, 99, 105], '2026-01-01');
var fired = SBT.dipSnipe(bars, p, 10);
eq('fired', fired.fired, true);
eq('fire date', fired.fireDate, '2026-01-03');
eq('fire price', fired.firePrice, 99);
eq('no idle cash', fired.idleCash, 0);
eq('invested', fired.invested, 150000);
var missed = SBT.dipSnipe(bars, p, 15);
eq('not fired', missed.fired, false);
eq('idle cash', missed.idleCash, 50000);
eq('invested anyway', missed.invested, 150000);   // committed cash still counts
// Idle reserve is preserved in the final value, it is not lost.
eq('final keeps cash', missed.finalValue, 105000 + 50000);
// Monotonic rise never triggers.
eq('up only', SBT.dipSnipe(mk(ramp(100, 1, 30), '2026-01-01'), p, 3).fired, false);
// Trigger is measured from the running peak, not from the entry price.
var b2 = mk([100, 200, 185, 180], '2026-01-01');
eq('peak based', SBT.dipSnipe(b2, p, 10).fireDate, '2026-01-04');  // 180 = -10% of 200
`)
}

func TestJSComboKeepsReserveSeparate(t *testing.T) {
	// 策略三's failure mode is the monthly add accidentally spending the sniper reserve.
	assertJS(t, "combo", `
var p = {capital:100000, reserve:50000, addAmount:10000, addCount:2, addDay:5, cost:SBT.makeCost(NOCOST)};
var bars = mk(ramp(100, 0, 70), '2026-01-01');   // flat: dip never triggers
var r = SBT.comboDCADip(bars, p, 10);
eq('not fired', r.fired, false);
eq('reserve untouched', r.idleCash, 50000);
eq('invested', r.invested, 170000);
// Every buy must be initial capital or a monthly add — never the reserve.
var spent = 0;
r.trades.forEach(function(t){ if (t.side === 'B') spent += t.amount; });
eq('spent excludes reserve', spent, 120000);
`)
}

func TestJSTakeProfitReenterCycles(t *testing.T) {
	assertJS(t, "tpre", `
var p = {capital:100000, reserve:0, addAmount:0, addCount:0, addDay:5, cost:SBT.makeCost(NOCOST)};
// 100 -> 120 (+20% stops out) -> 108 (-10% off 120 buys back) -> 130 (+20% again)
var bars = mk([100, 120, 108, 130, 117, 140], '2026-01-01');
var r = SBT.takeProfitReenter(bars, p, 20, 10, false);
eq('rounds', r.rounds, 2);
var kinds = r.trades.map(function(t){return t.kind;}).join('|');
eq('sequence', kinds, '初始資金|停利 +20%|回檔 10% 接回|停利 +20%|回檔 10% 接回|期末結算');
// A threshold nothing reaches degenerates to buy & hold.
var none = SBT.takeProfitReenter(bars, p, 500, 10, false);
eq('no rounds', none.rounds, 0);
eq('equals hold', none.finalValue, SBT.buyHold(bars, p).finalValue);
// Ending flat is reported so the panel can say "期末是現金".
var flat = SBT.takeProfitReenter(mk([100, 130], '2026-01-01'), p, 20, 90, false);
eq('ended flat', flat.endedFlat, true);
`)
}

func TestJSTakeProfitWithDCAPoolsCashWhileFlat(t *testing.T) {
	assertJS(t, "tpre-dca", `
var p = {capital:100000, reserve:0, addAmount:10000, addCount:2, addDay:5, cost:SBT.makeCost(NOCOST)};
// Stops out on day 2 (+50%), stays flat through the Jan 5 add, buys back on the dip.
var bars = mk([100, 150, 150, 150, 150, 90, 90, 90], '2026-01-01');
var r = SBT.takeProfitReenter(bars, p, 20, 20, true);
eq('rounds', r.rounds, 1);
eq('invested', r.invested, 110000);   // only Jan 5 falls inside this 8-day window
// The Jan-5 contribution arrives while flat, so there is no 定期定額 buy trade for it;
// it is deployed by the 接回 buy instead.
var dcaBuys = r.trades.filter(function(t){ return t.kind === '定期定額'; }).length;
eq('no dca buy while flat', dcaBuys, 0);
var reentry = r.trades.filter(function(t){ return t.kind.indexOf('接回') >= 0; });
eq('one reentry', reentry.length, 1);
near('reentry deploys pool', reentry[0].amount, 150000 + 10000, 90);
`)
}

func TestJSLockInStopsForever(t *testing.T) {
	assertJS(t, "lockin", `
var p = {capital:100000, reserve:0, addAmount:0, addCount:0, addDay:5, cost:SBT.makeCost(NOCOST)};
var bars  = mk([100, 130, 500, 900], '2026-01-01');
var bars2 = mk([100, 130,  10,   5], '2026-01-01');
var a = SBT.lockIn(bars, p, 20), b = SBT.lockIn(bars2, p, 20);
eq('fired', a.fired, true);
eq('exit date', a.exitDate, '2026-01-02');
// Once out, later prices are irrelevant — that is the whole point of 落袋為安.
eq('immune to rally', a.finalValue, 130000);
eq('immune to crash', b.finalValue, 130000);
eq('one round', a.rounds, 1);
var never = SBT.lockIn(bars, p, 2000);
eq('never fired', never.fired, false);
eq('degenerates to hold', never.finalValue, SBT.buyHold(bars, p).finalValue);
`)
}

func TestJSSMALeadIn(t *testing.T) {
	assertJS(t, "sma", `
var s = SBT.sma([1,2,3,4,5,6], 3);
eq('ramp-up blank', s[0] + ',' + s[1], '0,0');
eq('first value', s[2], 2);
eq('last value', s[5], 5);
// A gap (0 = no bar) must not silently average as zero.
var g = SBT.sma([1,2,0,4,5,6,7], 3);
eq('gap blanks', g[2] + ',' + g[3] + ',' + g[4], '0,0,0');
eq('recovers', g[5], 5);
`)
}

func TestJSRunAllShape(t *testing.T) {
	assertJS(t, "runall", `
var bars = mk([100, 110, 95, 120, 108, 130, 118, 140], '2026-01-01');
var r = SBT.runAll(bars, {capital:100000, reserve:50000, addAmount:10000, addCount:2, cost:NOCOST});
eq('has base', !!r.base, true);
eq('has s1', !!r.s1, true);
eq('has s2', !!r.s2, true);
eq('has s3', !!r.s3, true);
eq('has s4', !!r.s4, true);
eq('has s5', !!r.s5, true);
eq('has s6', !!r.s6, true);
eq('dip grid', r.s2.grid.length, SBT.DIP_GRID.length);
eq('tp grid', r.s4.grid.length, SBT.TP_GRID.length * SBT.RB_GRID.length);
eq('meta bars', r.meta.bars, 8);
near('price ret', r.meta.priceRet, 40, 1e-9);
// Ranking is sorted best-first and excludes 落袋為安 by design.
eq('ranked', r.ranking.length >= 2, true);
for (var i = 1; i < r.ranking.length; i++) {
  if (r.ranking[i-1].ret < r.ranking[i].ret) { FAILED++; print('FAIL ranking not sorted'); }
}
eq('no lockin in ranking', r.ranking.filter(function(x){return x.label.indexOf('落袋')>=0;}).length, 0);

// Strategies that need money the user did not allocate must be omitted, not faked.
var lean = SBT.runAll(bars, {capital:100000, reserve:0, addAmount:0, addCount:0, cost:NOCOST});
eq('no s2 without reserve', lean.s2, null);
eq('no s3 without both', lean.s3, null);
eq('no s5 without dca', lean.s5, null);
eq('s4 still there', !!lean.s4, true);
eq('too few bars', SBT.runAll([{d:'2026-01-01',c:100}], {capital:1000, cost:NOCOST}), null);
`)
}

func TestJSCostsDragReturns(t *testing.T) {
	// Fees must always make the same plan worse, and more round trips must cost more.
	assertJS(t, "costdrag", `
// Moves are comfortably past the threshold so both runs take the same 2 round trips —
// otherwise the fee-inflated cost basis alone would change the trade count.
var bars = mk([100, 125, 110, 135, 120, 145], '2026-01-01');
var on  = {capital:100000, reserve:0, addAmount:0, addCount:0, addDay:5, cost:SBT.makeCost({})};
var off = {capital:100000, reserve:0, addAmount:0, addCount:0, addDay:5, cost:SBT.makeCost(NOCOST)};
eq('hold cheaper without fees', SBT.buyHold(bars, off).profit > SBT.buyHold(bars, on).profit, true);
var churnOn  = SBT.takeProfitReenter(bars, on, 20, 10, false);
var churnOff = SBT.takeProfitReenter(bars, off, 20, 10, false);
eq('churn costs more', churnOff.profit - churnOn.profit > SBT.buyHold(bars, off).profit - SBT.buyHold(bars, on).profit, true);
eq('rounds charged', churnOn.rounds, 3);
eq('same trade count', churnOn.rounds, churnOff.rounds);
// 停利 measures against the FEE-INCLUSIVE cost basis, so a bare +20% close does not
// clear a 20% target once fees are on. That is intended, not a rounding slip.
var thin = mk([100, 120, 90], '2026-01-01');
eq('thin with fees', SBT.takeProfitReenter(thin, on, 20, 10, false).rounds, 0);
eq('thin without fees', SBT.takeProfitReenter(thin, off, 20, 10, false).rounds, 1);
`)
}
