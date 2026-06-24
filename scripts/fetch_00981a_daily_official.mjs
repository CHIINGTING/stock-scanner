#!/usr/bin/env node
// fetch_00981a_daily_official.mjs
//
// 抓取「統一投信 00981A 主動統一台股增長 ETF」每日官方持股 / PCF（投資組合明細）。
// 來源：統一投信官方 ezMoney（每日資料）。不使用 WantGoo（季度）、不以 MoneyDJ 為主來源（不完整）。
//
//   node scripts/fetch_00981a_daily_official.mjs            # 抓最新可下載資料
//   node scripts/fetch_00981a_daily_official.mjs 2026/06/12 # 指定日期
//
// 下載的 XLSX/XLS 會存到 data/official/，檔名：00981A_official_YYYYMMDD_<原始檔名>
// 找不到下載按鈕時：印出頁面所有 button/input/a 的文字/value/href，並存 debug 截圖
// 到 data/official/00981A_debug.png，然後以 exit code 2 結束。
//
// 需求：Node + Playwright（chromium）。安裝見 docs/00981A_DAILY_HOLDING.md。

import { chromium } from "playwright";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

const ETF_CODE = "00981A";
const FUND_CODE = "49YTW";
const PCF_URL = `https://www.ezmoney.com.tw/ETF/Transaction/PCF?fundCode=${FUND_CODE}`;
const OUT_DIR = path.resolve("data/official");
const DEBUG_PNG = path.join(OUT_DIR, `${ETF_CODE}_debug.png`);

// 下載觸發字樣 / 直接檔案連結的判斷。
const DOWNLOAD_TEXT_RE = /下載|匯出|下載檔案|EXCEL|XLSX?|試算表/i;
const FILE_HREF_RE = /\.xlsx?($|\?)/i;

function log(...a) {
  console.log(`[fetch_00981a ${new Date().toISOString()}]`, ...a);
}

// 把 "2026/06/12" 或 "2026-06-12" 轉成 { ymd: "20260612", slash: "2026/06/12", dash: "2026-06-12" }。
// 未給日期時用今天（Asia/Taipei）。
function resolveDate(arg) {
  let y, m, d;
  if (arg) {
    const m1 = arg.match(/^(\d{4})[/-](\d{1,2})[/-](\d{1,2})$/);
    if (!m1) throw new Error(`日期格式錯誤："${arg}"（請用 YYYY/MM/DD）`);
    [, y, m, d] = m1;
  } else {
    const now = new Date(new Date().toLocaleString("en-US", { timeZone: "Asia/Taipei" }));
    y = String(now.getFullYear());
    m = String(now.getMonth() + 1);
    d = String(now.getDate());
  }
  const mm = m.padStart(2, "0");
  const dd = d.padStart(2, "0");
  return { ymd: `${y}${mm}${dd}`, slash: `${y}/${mm}/${dd}`, dash: `${y}-${mm}-${dd}` };
}

// 嘗試在頁面上設定查詢日期（DOM 未知，盡力而為；失敗不致命）。
async function trySetDate(page, date) {
  const candidates = [
    'input[type="date"]',
    'input[id*="date" i]',
    'input[name*="date" i]',
    'input[id*="Date"]',
    'input.datepicker',
    'input[placeholder*="日期"]',
  ];
  for (const sel of candidates) {
    const el = page.locator(sel).first();
    if ((await el.count()) === 0) continue;
    for (const val of [date.dash, date.slash, date.ymd]) {
      try {
        await el.fill(val, { timeout: 2000 });
        log(`已嘗試於 ${sel} 填入日期 ${val}`);
        // 嘗試觸發查詢
        for (const q of ["查詢", "搜尋", "查 詢", "Search"]) {
          const btn = page.getByRole("button", { name: q }).first();
          if ((await btn.count()) > 0) {
            await btn.click({ timeout: 2000 }).catch(() => {});
            break;
          }
        }
        await page.waitForLoadState("networkidle", { timeout: 8000 }).catch(() => {});
        return true;
      } catch {
        /* try next */
      }
    }
  }
  log("未找到可用的日期欄位，改抓頁面預設（最新）資料");
  return false;
}

// 在頁面上找出下載觸發元素（直接檔案連結優先；其次含「下載/EXCEL」字樣的可點元素）。
async function findDownloadTrigger(page) {
  // 1) 直接的 .xlsx/.xls 連結。
  const anchors = await page.locator("a[href]").elementHandles();
  for (const a of anchors) {
    const href = (await a.getAttribute("href")) || "";
    if (FILE_HREF_RE.test(href)) return { kind: "href", handle: a, href };
  }
  // 2) 含下載字樣的 a / button / input。
  for (const sel of ["a", "button", 'input[type="button"]', 'input[type="submit"]']) {
    const els = await page.locator(sel).elementHandles();
    for (const el of els) {
      const text = ((await el.textContent()) || "").trim();
      const val = (await el.getAttribute("value")) || "";
      if (DOWNLOAD_TEXT_RE.test(text) || DOWNLOAD_TEXT_RE.test(val)) {
        return { kind: "click", handle: el, label: text || val };
      }
    }
  }
  return null;
}

// 找不到下載元素時，傾印頁面所有控制項並截圖，方便你回報給我調整選擇器。
async function dumpDebug(page) {
  const controls = await page.evaluate(() => {
    const grab = (sel) =>
      Array.from(document.querySelectorAll(sel)).map((e) => ({
        tag: e.tagName,
        text: (e.textContent || "").trim().slice(0, 80),
        value: e.getAttribute("value") || "",
        href: e.getAttribute("href") || "",
        id: e.id || "",
        name: e.getAttribute("name") || "",
      }));
    return { buttons: grab("button"), inputs: grab("input"), anchors: grab("a") };
  });
  console.log("──── DEBUG: 頁面控制項 ────");
  console.log(JSON.stringify(controls, null, 2));
  await page.screenshot({ path: DEBUG_PNG, fullPage: true }).catch((e) => log("截圖失敗:", e.message));
  log(`debug 截圖：${DEBUG_PNG}`);
}

async function main() {
  const date = resolveDate(process.argv[2]);
  await mkdir(OUT_DIR, { recursive: true });
  log(`ETF=${ETF_CODE} fundCode=${FUND_CODE} 目標日期=${date.slash}`);
  log(`開啟：${PCF_URL}`);

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ acceptDownloads: true });
  const page = await context.newPage();
  page.setDefaultTimeout(30000);

  let exitCode = 0;
  try {
    await page.goto(PCF_URL, { waitUntil: "networkidle", timeout: 45000 });
    if (process.argv[2]) await trySetDate(page, date);

    const trigger = await findDownloadTrigger(page);
    if (!trigger) {
      log("找不到下載按鈕 / 檔案連結。");
      await dumpDebug(page);
      exitCode = 2;
      return;
    }

    // 觸發下載並存檔。
    let suggested = "00981A_pcf.xlsx";
    let savedPath;
    if (trigger.kind === "href" && /^https?:/i.test(trigger.href)) {
      // 直接以瀏覽器 context 下載檔案 bytes（同 cookie/session）。
      log(`直接檔案連結：${trigger.href}`);
      const resp = await context.request.get(trigger.href);
      if (!resp.ok()) throw new Error(`下載 HTTP ${resp.status()}`);
      const buf = await resp.body();
      suggested = path.basename(new URL(trigger.href).pathname) || suggested;
      savedPath = path.join(OUT_DIR, `${ETF_CODE}_official_${date.ymd}_${suggested}`);
      await writeFile(savedPath, buf);
    } else {
      log(`點擊下載觸發：${trigger.label || trigger.href}`);
      const [download] = await Promise.all([
        page.waitForEvent("download", { timeout: 30000 }),
        trigger.handle.click(),
      ]);
      suggested = download.suggestedFilename() || suggested;
      savedPath = path.join(OUT_DIR, `${ETF_CODE}_official_${date.ymd}_${suggested}`);
      await download.saveAs(savedPath);
    }
    log(`已下載：${savedPath}`);
    console.log(savedPath); // 末行輸出存檔路徑，供 shell 取用
  } catch (err) {
    log("錯誤：", err.message);
    await dumpDebug(page).catch(() => {});
    exitCode = 1;
  } finally {
    await browser.close();
  }
  process.exit(exitCode);
}

main().catch((e) => {
  console.error("[fetch_00981a] 未預期錯誤：", e);
  process.exit(1);
});
