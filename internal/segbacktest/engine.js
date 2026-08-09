/* 區間策略回測引擎 — pure functions, no DOM, no globals beyond SBT.
 *
 * Everything here answers one question: given a stock's closes between a buy date
 * and a sell date, what would each capital-deployment rule have ended up with?
 *
 * Conventions used throughout:
 *   - All decisions are made on the DAILY CLOSE. There is no intraday fill, no gap
 *     handling, and no slippage beyond the fee/tax model. A rule that "triggers at
 *     -5%" therefore fills at that day's close, not at the -5% price.
 *   - 總投入 (invested) counts every dollar the plan commits, including 預留現金 that
 *     never got deployed — otherwise a rule that keeps cash idle looks artificially
 *     good on a percentage basis.
 *   - 報酬率 = 損益 / 總投入. With staggered contributions this is NOT time-weighted;
 *     money added late has less time to work. The panel says so next to the number.
 *   - Shares are whole 零股 (1 share granularity). Leftover cash stays in the pool.
 *
 * This is a replay of historical closes, not a forecast and not an order router.
 */
var SBT = (function () {
  'use strict';

  // ── threshold comparison ─────────────────────────────────────────────────────
  // Binary floating point makes exact thresholds miss: 120/100 - 1 is
  // 0.19999999999999996, so a +20% take-profit would NOT fire on a clean +20% move.
  // Every threshold test goes through these so "20%" means 20%.
  var EPS = 1e-9;
  function gte(a, b) { return a >= b - Math.abs(b) * EPS - EPS; }
  function lte(a, b) { return a <= b + Math.abs(b) * EPS + EPS; }

  // ── cost model ────────────────────────────────────────────────────────────────
  // 台股: 手續費 0.1425%(可打折, 買賣各收, 最低 20 元) + 證交稅 0.3%(賣出才收).
  function makeCost(o) {
    o = o || {};
    if (o.enabled === false) return { fee: 0, tax: 0, minFee: 0, on: false };
    var disc = o.discount === undefined ? 1 : o.discount;
    return {
      fee: (o.feeRate === undefined ? 0.001425 : o.feeRate) * disc,
      tax: o.taxRate === undefined ? 0.003 : o.taxRate,
      minFee: o.minFee === undefined ? 20 : o.minFee,
      on: true
    };
  }

  function buyFee(cost, gross) {
    if (!cost.on || gross <= 0) return 0;
    return Math.max(cost.minFee, Math.round(gross * cost.fee));
  }

  function sellCharges(cost, gross) {
    if (!cost.on || gross <= 0) return 0;
    return Math.max(cost.minFee, Math.round(gross * cost.fee)) + Math.round(gross * cost.tax);
  }

  // Largest whole-share order a budget can pay for, fees included.
  function affordableShares(cost, price, budget) {
    if (price <= 0 || budget <= 0) return 0;
    var n = Math.floor(budget / price);
    while (n > 0 && n * price + buyFee(cost, n * price) > budget) n--;
    return n;
  }

  // ── account ──────────────────────────────────────────────────────────────────
  // costTotal includes buy fees, so avgCost is a true break-even-ish per-share cost.
  function Account(cost) {
    this.cost = cost;
    this.shares = 0;
    this.costTotal = 0;
    this.cash = 0;
    this.invested = 0;
    this.realized = 0;
    this.rounds = 0;
    this.trades = [];
  }

  Account.prototype.contribute = function (amount) {
    if (amount <= 0) return;
    this.cash += amount;
    this.invested += amount;
  };

  Account.prototype.avgCost = function () {
    return this.shares > 0 ? this.costTotal / this.shares : 0;
  };

  // Unrealized return against average cost, on the gross close (sell costs excluded
  // from the trigger so the threshold means what the user typed).
  Account.prototype.unrealized = function (price) {
    var a = this.avgCost();
    return a > 0 ? price / a - 1 : 0;
  };

  Account.prototype.buy = function (date, price, budget, kind) {
    var n = affordableShares(this.cost, price, Math.min(budget, this.cash));
    if (n <= 0) return 0;
    var gross = n * price, fee = buyFee(this.cost, gross);
    this.cash -= gross + fee;
    this.shares += n;
    this.costTotal += gross + fee;
    this.trades.push({ date: date, side: 'B', price: price, shares: n, amount: gross + fee, kind: kind });
    return n;
  };

  Account.prototype.buyAll = function (date, price, kind) {
    return this.buy(date, price, this.cash, kind);
  };

  Account.prototype.sellAll = function (date, price, kind) {
    if (this.shares <= 0) return 0;
    var n = this.shares, gross = n * price, ch = sellCharges(this.cost, gross);
    var net = gross - ch;
    this.cash += net;
    this.realized += net - this.costTotal;
    this.shares = 0;
    this.costTotal = 0;
    this.trades.push({ date: date, side: 'S', price: price, shares: n, amount: net, kind: kind });
    return n;
  };

  // Mark-to-market equity used for the drawdown curve (gross, no exit costs).
  Account.prototype.equity = function (price) {
    return this.cash + this.shares * price;
  };

  // ── schedules ────────────────────────────────────────────────────────────────
  // 定期定額 dates: the first in-window bar of each month on/after `day`, skipping
  // bar 0 (that money is the 初始資金, already deployed). Capped at `count` dates.
  //
  // If the entry bar itself is already on/after the add day, that whole month is
  // consumed — otherwise buying on the 6th would trigger "this month's" add on the
  // 7th, deploying two tranches back to back.
  function monthlyAddIndexes(bars, day, count) {
    if (!(count > 0) || !bars.length) return [];
    var out = [], usedMonth = {};
    if (+bars[0].d.slice(8, 10) >= day) usedMonth[bars[0].d.slice(0, 7)] = true;
    for (var i = 1; i < bars.length && out.length < count; i++) {
      var d = bars[i].d, month = d.slice(0, 7), dom = +d.slice(8, 10);
      if (usedMonth[month] || dom < day) continue;
      usedMonth[month] = true;
      out.push(i);
    }
    return out;
  }

  // ── result packaging ─────────────────────────────────────────────────────────
  function settle(acc, bars, name, extra) {
    var last = bars[bars.length - 1];
    if (acc.shares > 0) acc.sellAll(last.d, last.c, '期末結算');
    var finalValue = acc.cash;
    var profit = finalValue - acc.invested;
    var r = {
      name: name,
      invested: acc.invested,
      finalValue: finalValue,
      profit: profit,
      ret: acc.invested > 0 ? (profit / acc.invested) * 100 : 0,
      rounds: acc.rounds,
      trades: acc.trades,
      maxDD: acc.maxDD || 0,
      idleCash: acc.idleCash || 0
    };
    if (extra) for (var k in extra) if (extra.hasOwnProperty(k)) r[k] = extra[k];
    return r;
  }

  // Tracks the worst peak-to-trough drop of the equity curve, in percent.
  function ddTracker() {
    var peak = 0, worst = 0;
    return {
      step: function (eq) {
        if (eq > peak) peak = eq;
        if (peak > 0) {
          var dd = (eq / peak - 1) * 100;
          if (dd < worst) worst = dd;
        }
      },
      worst: function () { return worst; }
    };
  }

  // ── strategies ───────────────────────────────────────────────────────────────
  // Shared params: {capital, reserve, addAmount, addCount, addDay, cost}

  // 基準 — 第一天把所有資金(初始+預留)一次投入, 抱到期末.
  function buyHold(bars, p) {
    var acc = new Account(p.cost), dd = ddTracker();
    acc.contribute(p.capital + p.reserve);
    acc.buyAll(bars[0].d, bars[0].c, '單筆全押');
    for (var i = 0; i < bars.length; i++) dd.step(acc.equity(bars[i].c));
    acc.maxDD = dd.worst();
    return settle(acc, bars, '基準：單筆全押抱到底');
  }

  // 策略一 定期定額 — 初始資金(含預留現金)第一天進場, 之後每月加碼日固定投入.
  function dca(bars, p) {
    var acc = new Account(p.cost), dd = ddTracker();
    var adds = indexSet(monthlyAddIndexes(bars, p.addDay, p.addCount));
    acc.contribute(p.capital + p.reserve);
    acc.buyAll(bars[0].d, bars[0].c, '初始資金');
    for (var i = 0; i < bars.length; i++) {
      if (i > 0 && adds[i]) {
        acc.contribute(p.addAmount);
        acc.buyAll(bars[i].d, bars[i].c, '定期定額');
      }
      dd.step(acc.equity(bars[i].c));
    }
    acc.maxDD = dd.worst();
    return settle(acc, bars, '策略一：定期定額加碼');
  }

  // 策略二 逢跌狙擊 — 初始資金第一天進場, 預留現金等「自區間最高收盤回檔 t%」時一次
  // 全數投入. 只觸發一次; 沒觸發就一直是現金(仍計入總投入).
  function dipSnipe(bars, p, t) {
    var acc = new Account(p.cost), dd = ddTracker();
    acc.contribute(p.capital);
    acc.buyAll(bars[0].d, bars[0].c, '初始資金');
    acc.contribute(p.reserve);
    var peak = bars[0].c, fired = false, fireDate = '', firePrice = 0;
    for (var i = 0; i < bars.length; i++) {
      var c = bars[i].c;
      if (c > peak) peak = c;
      if (!fired && i > 0 && p.reserve > 0 && lte(c, peak * (1 - t / 100))) {
        acc.buyAll(bars[i].d, c, '回檔 ' + t + '% 狙擊');
        fired = true; fireDate = bars[i].d; firePrice = c;
      }
      dd.step(acc.equity(c));
    }
    acc.maxDD = dd.worst();
    acc.idleCash = fired ? 0 : p.reserve;
    return settle(acc, bars, '回檔 ' + t + '% 狙擊',
      { threshold: t, fired: fired, fireDate: fireDate, firePrice: firePrice });
  }

  // 策略三 混合 — 策略一的定期定額 + 策略二的預留現金狙擊(雙資金流).
  function comboDCADip(bars, p, t) {
    var acc = new Account(p.cost), dd = ddTracker();
    var adds = indexSet(monthlyAddIndexes(bars, p.addDay, p.addCount));
    acc.contribute(p.capital);
    acc.buyAll(bars[0].d, bars[0].c, '初始資金');
    acc.contribute(p.reserve);
    var reserveLeft = p.reserve, peak = bars[0].c, fired = false, fireDate = '', firePrice = 0;
    for (var i = 0; i < bars.length; i++) {
      var c = bars[i].c;
      if (c > peak) peak = c;
      if (i > 0 && adds[i]) {
        acc.contribute(p.addAmount);
        // Only the fresh contribution goes in here; the reserve waits for its trigger.
        acc.buy(bars[i].d, c, acc.cash - reserveLeft, '定期定額');
      }
      if (!fired && i > 0 && reserveLeft > 0 && lte(c, peak * (1 - t / 100))) {
        acc.buyAll(bars[i].d, c, '回檔 ' + t + '% 狙擊');
        reserveLeft = 0; fired = true; fireDate = bars[i].d; firePrice = c;
      }
      dd.step(acc.equity(c));
    }
    acc.maxDD = dd.worst();
    acc.idleCash = reserveLeft;
    return settle(acc, bars, '定期定額 + 回檔 ' + t + '% 狙擊',
      { threshold: t, fired: fired, fireDate: fireDate, firePrice: firePrice });
  }

  // 策略四 停利接回(單筆) — 初始資金(含預留)一次進場; 未實現獲利達 tp% 全數停利,
  // 之後自「賣出後最高收盤」回檔 rb% 時本利和全數接回. 可反覆循環.
  // 策略五 = 同一台引擎, 加上每月定期定額(持股時直接買進, 空手時先進現金池).
  function takeProfitReenter(bars, p, tp, rb, withDCA) {
    var acc = new Account(p.cost), dd = ddTracker();
    var adds = withDCA ? indexSet(monthlyAddIndexes(bars, p.addDay, p.addCount)) : {};
    acc.contribute(p.capital + p.reserve);
    acc.buyAll(bars[0].d, bars[0].c, '初始資金');
    var peakSinceSell = 0, holding = true;
    for (var i = 0; i < bars.length; i++) {
      var c = bars[i].c;
      if (i > 0 && adds[i]) {
        acc.contribute(p.addAmount);
        if (holding) acc.buyAll(bars[i].d, c, '定期定額');
      }
      if (i > 0) {
        if (holding) {
          if (acc.shares > 0 && gte(acc.unrealized(c), tp / 100)) {
            acc.sellAll(bars[i].d, c, '停利 +' + tp + '%');
            acc.rounds++;
            holding = false;
            peakSinceSell = c;
          }
        } else {
          if (c > peakSinceSell) peakSinceSell = c;
          if (lte(c, peakSinceSell * (1 - rb / 100))) {
            acc.buyAll(bars[i].d, c, '回檔 ' + rb + '% 接回');
            holding = true;
          }
        }
      }
      dd.step(acc.equity(c));
    }
    acc.maxDD = dd.worst();
    acc.idleCash = holding ? 0 : acc.cash;
    return settle(acc, bars,
      (withDCA ? '定期定額 + ' : '') + '停利 +' + tp + '% / 回檔 ' + rb + '% 接回',
      { tp: tp, rb: rb, endedFlat: !holding });
  }

  // 策略六 落袋為安 — 單筆進場, 獲利達 tp% 全數賣出後就不再進場.
  function lockIn(bars, p, tp) {
    var acc = new Account(p.cost), dd = ddTracker();
    acc.contribute(p.capital + p.reserve);
    acc.buyAll(bars[0].d, bars[0].c, '初始資金');
    var done = false, exitDate = '', exitPrice = 0;
    for (var i = 0; i < bars.length; i++) {
      var c = bars[i].c;
      if (!done && i > 0 && acc.shares > 0 && gte(acc.unrealized(c), tp / 100)) {
        acc.sellAll(bars[i].d, c, '停利 +' + tp + '% 落袋');
        acc.rounds++;
        done = true; exitDate = bars[i].d; exitPrice = c;
      }
      dd.step(acc.equity(c));
    }
    acc.maxDD = dd.worst();
    return settle(acc, bars, '停利 +' + tp + '% 落袋不再進場',
      { tp: tp, fired: done, exitDate: exitDate, exitPrice: exitPrice });
  }

  function indexSet(list) {
    var m = {};
    for (var i = 0; i < list.length; i++) m[list[i]] = true;
    return m;
  }

  // ── grids ────────────────────────────────────────────────────────────────────
  var DIP_GRID = [3, 5, 8, 10, 12, 15, 20, 25, 30];
  var TP_GRID = [5, 10, 15, 20, 25, 30, 40, 50, 75, 100, 150, 200];
  var RB_GRID = [3, 5, 8, 10, 15, 20];

  // The champion is the highest 報酬率 among runs that actually did something. A grid
  // where nothing triggered has no champion — the honest answer there is "the rule
  // never fired, so the baseline IS the result", and the panel says exactly that.
  function bestOf(list, firedOnly) {
    var best = null;
    for (var i = 0; i < list.length; i++) {
      var r = list[i];
      if (firedOnly && !r.fired) continue;
      if (!best || r.ret > best.ret) best = r;
    }
    return best;
  }

  // ── moving averages ──────────────────────────────────────────────────────────
  // Computed over the FULL series (including bars before the window) so the first
  // in-window value is a real average, not a ramp-up artifact.
  function sma(closes, n) {
    var out = new Array(closes.length), sum = 0, cnt = 0;
    for (var i = 0; i < closes.length; i++) {
      var c = closes[i];
      if (c > 0) { sum += c; cnt++; }
      if (i >= n) {
        var old = closes[i - n];
        if (old > 0) { sum -= old; cnt--; }
      }
      out[i] = (i >= n - 1 && cnt === n) ? sum / n : 0;
    }
    return out;
  }

  // ── top level ────────────────────────────────────────────────────────────────
  /* runAll(bars, params) where
   *   bars   = [{d:'2026-01-02', c:940}, ...] ascending, already sliced to the window
   *   params = {capital, reserve, addAmount, addCount, addDay, cost:{...}}
   */
  function runAll(bars, params) {
    if (!bars || bars.length < 2) return null;
    var p = {
      capital: num(params.capital),
      reserve: num(params.reserve),
      addAmount: num(params.addAmount),
      addCount: num(params.addCount),
      addDay: params.addDay > 0 ? params.addDay : 5,
      cost: makeCost(params.cost)
    };
    var hasDCA = p.addAmount > 0 && p.addCount > 0;
    var hasReserve = p.reserve > 0;
    var hasLump = p.capital > 0;

    var base = buyHold(bars, p);
    var s1 = dca(bars, p);

    var s2 = null;
    if (hasReserve) {
      s2 = { grid: DIP_GRID.map(function (t) { return dipSnipe(bars, p, t); }) };
      s2.best = bestOf(s2.grid, true);
    }

    var s3 = null;
    if (hasReserve && hasDCA) {
      s3 = { grid: DIP_GRID.map(function (t) { return comboDCADip(bars, p, t); }) };
      s3.best = bestOf(s3.grid, true);
    }

    var s4 = null;
    if (hasLump || hasReserve) {
      s4 = { grid: [] };
      TP_GRID.forEach(function (tp) {
        RB_GRID.forEach(function (rb) { s4.grid.push(takeProfitReenter(bars, p, tp, rb, false)); });
      });
      s4.best = bestOf(s4.grid.filter(function (r) { return r.rounds > 0; }), false);
    }

    var s5 = null;
    if (hasDCA) {
      s5 = { grid: [] };
      TP_GRID.forEach(function (tp) {
        RB_GRID.forEach(function (rb) { s5.grid.push(takeProfitReenter(bars, p, tp, rb, true)); });
      });
      s5.best = bestOf(s5.grid.filter(function (r) { return r.rounds > 0; }), false);
    }

    var s6 = null;
    if (hasLump || hasReserve) {
      s6 = { grid: TP_GRID.map(function (tp) { return lockIn(bars, p, tp); }) };
      s6.best = bestOf(s6.grid, true);
    }

    // 排名: 落袋為安(策略六)刻意不參賽 — 它結束時是現金, 跟其他全程在市場的策略
    // 比報酬率並不對等; 它的用途是「我想抱多久」的參考表.
    var ranking = [];
    push(ranking, '策略一 定期定額', s1);
    if (s2 && s2.best) push(ranking, '策略二 逢跌狙擊（最佳 ' + s2.best.threshold + '%）', s2.best);
    if (s3 && s3.best) push(ranking, '策略三 定額+狙擊（最佳 ' + s3.best.threshold + '%）', s3.best);
    if (s4 && s4.best) push(ranking, '策略四 停利接回（最佳 +' + s4.best.tp + '%/-' + s4.best.rb + '%）', s4.best);
    if (s5 && s5.best) push(ranking, '策略五 定額+停利接回（最佳 +' + s5.best.tp + '%/-' + s5.best.rb + '%）', s5.best);
    ranking.sort(function (a, b) { return b.ret - a.ret; });

    return {
      meta: {
        from: bars[0].d, to: bars[bars.length - 1].d, bars: bars.length,
        firstClose: bars[0].c, lastClose: bars[bars.length - 1].c,
        priceRet: (bars[bars.length - 1].c / bars[0].c - 1) * 100,
        hasDCA: hasDCA, hasReserve: hasReserve, hasLump: hasLump,
        costOn: p.cost.on
      },
      params: p, base: base, s1: s1, s2: s2, s3: s3, s4: s4, s5: s5, s6: s6,
      ranking: ranking
    };
  }

  function push(arr, label, r) {
    if (r) arr.push({ label: label, ret: r.ret, profit: r.profit, invested: r.invested,
                      finalValue: r.finalValue, rounds: r.rounds, maxDD: r.maxDD, res: r });
  }

  function num(v) { var x = +v; return isFinite(x) && x > 0 ? x : 0; }

  return {
    makeCost: makeCost, buyFee: buyFee, sellCharges: sellCharges,
    affordableShares: affordableShares, Account: Account,
    monthlyAddIndexes: monthlyAddIndexes, sma: sma,
    buyHold: buyHold, dca: dca, dipSnipe: dipSnipe, comboDCADip: comboDCADip,
    takeProfitReenter: takeProfitReenter, lockIn: lockIn,
    runAll: runAll, DIP_GRID: DIP_GRID, TP_GRID: TP_GRID, RB_GRID: RB_GRID
  };
})();

if (typeof module !== 'undefined' && module.exports) module.exports = SBT;
