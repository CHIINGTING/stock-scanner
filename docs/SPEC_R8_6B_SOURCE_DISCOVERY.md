# SPEC R8-6b — ETF Holdings Source Discovery & Classification

狀態：Discovery 完成（docs only）。本文件**只做來源分類與 guardrail**，不接任何來源進 R8 flow。

相關：[SPEC_R8_1_ETF_FLOW.md](SPEC_R8_1_ETF_FLOW.md)（snapshot schema / flow 定義）、
[SPEC_R8_5_MANUAL_INGEST.md](SPEC_R8_5_MANUAL_INGEST.md)（手動 ingest CLI）、
R8-6a MoneyDJ auto fetcher（**尚未 commit**，待本 registry 落地後再決定改造方向，見 §6）。

---

## 0. 為什麼需要這份文件

R8 ETF flow（`CalculateFlows` / `delta_shares` / sell-pressure / 連續賣出天數）只有在
**逐日、完整、含持有股數**的持股資料下才成立。若把不符合的來源（前 N 大、季度比例）
誤接進 flow，會產生**假的賣壓訊號**（例如某股「跌出前 10 名」被誤判成「被賣光」，或拿
季度比例當成當日股數變化）。因此先把每個來源的涵蓋型別釘死，再決定用途。

---

## 1. MoneyDJ — `partial_top_n`

| 項目 | 值 |
|------|----|
| 來源頁 | `https://www.moneydj.com/etf/x/basic/basic0007.xdjhtm?etfid=<code>.TW` |
| coverage_type | **`partial_top_n`** |
| top_n | **10**（持股明細表只揭露前 10 大成分） |
| 每日資料日期 (as_of_date) | **有**（每日；探測 00981A 取得 2026-06-12，揭露延遲約 3 天） |
| holding_shares（持有股數） | **有**（前 10 大，單位 股；例：2330 = 11,960,000 股） |
| holding_weight（比例） | 有（投資比例 %） |
| 是否完整持股 | **否**（僅前 10 大；第 11 名以後不揭露） |

判定與限制：

- ✅ 可作 **top holdings context**（前 10 大每日權重 / 股數）。
- ❌ **不可直接進 R8 flow**：top-10 的 `delta_shares` 會漏掉第 11 名後的部位；個股
  「跌出前 10」與「實際賣光」在此資料下**無法區分**。
- ❌ **不可把「某股缺席前 10」解讀為賣出**。
- ⚠️ 抓取方式：單次公開頁 GET（已實作於 R8-6a fetcher，加 User-Agent + timeout，
  無 crawler / 無 browser automation）。但因 markup 可能變動，需 fixture 測試保護。

> R8-6a 的 MoneyDJ fetcher 目前**不得**以 `full_holdings` 名義 commit / 寫入 production
> snapshot。改造方向見 §6。

---

## 2. WantGoo — `quarterly_composition`

| 項目 | 值 |
|------|----|
| 來源頁 | `https://www.wantgoo.com/stock/etf/<code>/constituent`（SPA，資料由站內 API 載入） |
| 站內資料 API | `/stock/etf/<code>/shareholding-data`（成分比例）、`/stock/etf/<code>/top-five-net-asset-value-ratio-data`（每月前五大 NAV 比例） |
| coverage_type | **`quarterly_composition`** |
| 成分涵蓋 | 全成分**比例**（比 MoneyDJ top-10 完整），但本質是季度成分 |
| holding_shares（持有股數） | **無**（成分物件欄位為 `shareholdingRate` 比例、`close`、`change`、`changePercent`；無 shares 欄位） |
| holding_weight（比例） | 有（季度比例） |
| 每日資料日期 | **無**。**季度更新**（1、4、7、10 月，資料為「上一季」持股；頁面明文標示） |
| 背後 API 是否提供每日完整 | **否**。同一份季度比例資料、無股數；且 `shareholding-data` 對純 HTTP 回 400 BadRequest（需站內 JS 的請求機制），抓取**不穩定** |

判定與限制：

- ✅ 可作 **季度持股結構 / 權重 context**（標示「上一季」與季別）。
- ❌ **不可做 `delta_shares`**（無股數）。
- ❌ **不可做 sell pressure**（季度比例 ≠ 當日部位變化）。
- ❌ **不可拿季度資料判斷當日換股**。
- ❌ **不可把缺席某股解讀為賣出**。
- ❌ **不可標記為 `full_holdings` 或 `full_daily_holdings`**。

---

## 3. 資料源分類表（coverage_type）

| coverage_type | 定義 | 可進 R8 flow？ | 允許用途 |
|---------------|------|----------------|----------|
| `full_daily_holdings` | 逐日、完整成分、含持有股數 | ✅ 是 | `CalculateFlows` / `delta_shares` / sell-pressure / 連續賣出天數 |
| `partial_top_n` | 僅前 N 大（含股數、可每日） | ❌ 否 | 只做 top holdings context |
| `quarterly_composition` | 季度成分比例（無股數、非每日） | ❌ 否 | 只做季度持股結構 context |
| `unusable` | 不可解析 / 不穩定 / 無用欄位 | ❌ 否 | 不使用 |

目前歸類：

| 來源 | coverage_type |
|------|---------------|
| MoneyDJ basic0007 | `partial_top_n`（top_n = 10） |
| WantGoo constituent | `quarterly_composition` |
| 官方 / issuer / PCF | 待 R8-6c 探查（目標 `full_daily_holdings`） |

> 目前**沒有任何來源**達到 `full_daily_holdings`，因此 R8 flow 目前**不應**接任何自動來源。

---

## 4. Guardrails（強制規則）

1. **只有 `full_daily_holdings` 可進 `CalculateFlows`。**
2. `partial_top_n` / `quarterly_composition` / `unusable` **一律不可進 flow**。
3. **缺席某股不可解讀成賣出**（前 N 大缺席 ≠ 賣光；季度名單缺席 ≠ 當日出清）。
4. **比例資料不可解讀成股數變化**（weight delta ≠ share delta）。
5. **不可拿季度資料判斷當日換股 / 當日流向。**
6. snapshot 必須帶 `coverage_type`；flow 計算前必須檢查 `coverage_type == full_daily_holdings`，
   否則拒絕計算（fail closed）。
7. 抓取一律**公開頁面**：不繞過登入 / captcha / 私有簽章 API，不對站台加壓（單次請求、
   加 User-Agent 與 timeout，無 crawler、無 browser automation）。
8. **不得 commit** `data/etf_holdings/*.json`（真實 snapshot 一律 gitignore）。

---

## 5. 下一步 — R8-6c：官方 / issuer / PCF full holdings 探查

目標：找到 `full_daily_holdings`（逐日、完整、含持有股數）來源。

- 優先找**統一投信官方**公開資料（00981A 為統一投信主動式 ETF）。
- 候選：投信官網每日持股 / PCF（申購買回清單，通常含逐檔股數）、TWSE 公開揭露。
- 仍受 §4.7 約束：**不繞過登入 / captcha / 私有 API**，只取公開資料。
- 產出：比照本文件，對每個候選標 `coverage_type`，確認是否真為 `full_daily_holdings`
  （逐日 + 完整 + 股數三者皆備）後，才允許進 flow。

---

## 6. R8-6a MoneyDJ fetcher 後續（暫停，待決策）

R8-6a MoneyDJ auto fetcher 程式**先不 commit**。待本 registry 落地後，再決定是否改造為
**top-n-only probe**：

- 標記 `coverage_type = top_n_only`（對齊本文件的 `partial_top_n`）。
- `LoadHistory` **不讀** partial snapshot（避免被 flow 誤用）。
- **預設不寫入 production snapshot**。
- 僅在 `--allow-partial` 時輸出，且輸出與紀錄中**明確警告：不可進 flow**。
