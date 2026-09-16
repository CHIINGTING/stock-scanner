# EP-10 — 舊版 ④ 價位計畫 與 ⑲ 進場計畫的收斂決定

本文件記錄 EP-10 的**量測**與**決定**。它不是 review 結論，也不宣稱任何策略成立。

EntryPlan 的定位在 EP-10 之後**沒有改變**：`Shadow Only`、`HEURISTIC`、`NOT BACKTEST-FITTED`，
`RuleVersion` 仍為 `EP6G-v1`。EP-10 沒有調整任何參數、沒有改任何決策語意、沒有改任何狀態定義。

---

## 1. 量測（Workstream B）

工具：`scripts/ep10_convergence`。

它**不自行重算 ⑲ 的顯示條件**：它用 `internal/report` 真的產生一份報告，再問「這一檔自己的展開列
裡有沒有 `wl-ep` 區塊」。所以顯示與否就是產品本身的判斷，不會和 renderer 漂移。
④ 的欄位則直接讀 `WatchlistEntry` 上那四列 ④ 會印出來的欄位。

母體：`internal/entryplanbacktest/recon` 重建的整個快取宇宙，arm 為 `REPLAYED_PIT`。

> **Research Only．REPLAYED_PIT．不是 production 歷史績效．未經回測擬合。**
> `production_historical_execution = NOT_EVALUABLE`。本量測**不含任何報酬、成交或績效數字**，
> 它比較的是同一個交易日裡兩個「呈現方式」，不是兩者的結果。

| | 2026-08-19（SIDEWAYS） | 2026-08-31（BULL_PULLBACK） |
|---|---|---|
| 觀察清單檔數 | 1976 | 1975 |
| A 只有 ④ | 1962 | 1949 |
| B 只有 ⑲ | 0 | 0 |
| C 兩者都有且實質一致 | 0 | 0 |
| D 兩者都有但實質矛盾 | 14 | 26 |
| E 兩者都沒有 | 0 | 0 |

D 的矛盾面向（同一列可同時具備多項）：

| 面向 | 08-19 | 08-31 |
|---|---|---|
| `ENTRY_NOT_NUMERICALLY_COMPARABLE`（④ 的進場欄是文字敘述，無法與 ⑲ 的區間比較） | 14 | 26 |
| `STOP_LEVEL_DIFFERS`（④ 停損 ≠ ⑲ 想法失效價） | 10 | 24 |
| `EXECUTION_AUTHORIZATION`（⑲ 不發布任何可執行價位，④ 仍列出完整價位計畫） | 4 | 2 |
| `STATUS_THESIS`（⑲ 狀態為 `INSUFFICIENT_DATA` / `NO_VALID_ENTRY` / `TOO_EXTENDED`） | 4 | 17 |
| `TARGET_BAND_DISJOINT`（④ 停利區與 ⑲ 目標區完全不重疊） | 4 | 3 |
| 其中 ④ 停損**高於** ⑲ 想法失效價 | 9 | 20 |
| 其中 ④ 停損**低於** ⑲ 想法失效價 | 1 | 4 |
| 兩者顯示位數四捨五入後相同 | 0 | 0 |

數字的意思：

1. **B = 0、E = 0。** ④ 是無條件的：`rocket.go:316-331` 每一檔都會寫出突破價／支撐價／停損價／
   停利區／進場區，而 `scorer.go:730-742` 在 ATR 不可用時以 `close×0.025` 代替、在停損算不出來時
   以 `close×0.93` 代替。不存在「⑲ 有話說而 ④ 沒有」的狀態。
2. **C = 0。** 兩區同時出現的每一檔，至少有一個實質面向互相矛盾。兩者從來沒有一致過。
3. **④ 與 ⑲ 的「進場」根本不是同一種東西。** ④ 的進場欄在六個 rocket 階段中有三個是文字
   （「拉回 5/10 日線量縮承接」「等回檔測 10 日線再評估」「等待型態成形」），所以在全部 40 列 D 中
   沒有一列有可與 ⑲ 進場區比較的數值區間。
4. **停損幾乎不會重合**（四捨五入後相同：34 列中 0 列），而且 34 列中有 29 列是 ④ 的停損**高於**
   ⑲ 的想法失效價——照 ④ 操作會在 ⑲ 認為想法還沒破的位置出場。
5. **最危險的是 `EXECUTION_AUTHORIZATION`（6 列）**：⑲ 一格價位都不填（`NO_VALID_ENTRY` /
   `INSUFFICIENT_DATA`），④ 卻列出完整的買進價位計畫。

## 2. 決定

**⑲ 不升格。** EntryPlan 仍是啟發式、未經回測擬合，且其 production 歷史執行為
`NOT_EVALUABLE`，因此不會成為報告的權威價位來源。

**④ 的欄位一個都不移除。** ④ 是報告既有的產品契約；把它的欄位藏起來會改變沒有打開 `show_entry_plan`
的讀者看到的東西。

EP-10 只做**呈現層的澄清**，而且只在**兩區同時出現**（`show_entry_plan=true` 且該檔真的有 ⑲ 區塊）時：

- ④ 的標題改為「④ 價位計畫（舊版掃描器價位指引）」；
- ④ 底下加一行說明它是什麼（每檔都有、必要時以固定比例替代）；
- ⑲ 底部原本那句「EP-10 之前不收斂」換成上面量測到的關係，並明寫 **④ 仍是既有價位欄位、⑲ 不取代它、
  ⑲ 不是經回測驗證的策略**。

沒有改變任何 scanner 決策語意（`Score` / `Decision` / `WatchAction` / `RocketScore` / 排序）。
行為證明：`internal/report/report_entryplan_test.go` 的
`TestEntryPlanDisplayChangesOnlyTheEntryPlanBlockAndMutatesNothing`（OFF vs ON，唯一允許的差異就是
⑲ 區塊、它的樣式、以及這兩處 ④ 標示）與
`TestLegacyPriceBlockIsLabelledLegacyOnlyBesideARenderedEntryPlan`。

## 3. 瀏覽器實測（Workstream A）修掉的呈現缺陷

fixture 在 `internal/report/report_entryplan_visual_test.go`（13 個狀態，含對抗性輸入）。

| | 缺陷 | 修法 |
|---|---|---|
| D1 | ⑲ 理由或注意事項裡一個無法斷行的長 token，會把**整份報告**撐寬，同一頁其他個股的卡片被推出畫面外（`.wl-grid` 的軌道是 `1fr` = `minmax(auto,1fr)`） | `.wl-ep` 加 `min-width:0` 與 `overflow-wrap:anywhere`，只影響 ⑲ 自身 |
| D2 | ⑲ 有 11 個欄位加兩份清單，卻擠在三分之一欄寬，每個 `ep-code` 標籤都換行 | `.wl-ep{grid-column:1/-1}` |
| D3 | 進場區不存在時仍印出它的基準（`理想進場區 — MA20・RAW`） | `epZoneBasis` 改為在 `epZone` 判定不可用時一併不印 |
| D4 | ④ 與 ⑲ 的關係沒有任何說明 | 見第 2 節 |
| D5 | ⑲ 底部寫著「EP-10 之前不收斂」，EP-10 之後就是假的 | 換成實測到的關係 |

`NaN` / `±Inf` 在任何一個 fixture 都沒有出現；缺值一律 `—`，沒有偽造的 `0.00`。

## 4. Reason 呈現

EP-8 對 60 個 reason code 中的 28 個給了中文；其餘 32 個以原始代碼呈現。EP-10 把對照表**補齊到剛好
等於 `entryplan.AllReasons`**，並加上雙向精確守衛
`TestEntryPlanReasonLabelsCoverTheRegistryExactly`（登記了卻沒有標籤 → 失敗；有標籤卻沒登記 → 失敗）。

保留的三個性質：原始代碼永遠印在文字旁邊、未登記的代碼仍以原始代碼呈現（fallback 沒有拿掉）、
順序仍是 plan 自己的順序（輸出是決定性的）。沒有任何一句話改寫了 entryplan 的條件；特別是
`STATUS_REQUIREMENT_NOT_EVALUATED` 寫成「本系統尚無法評估（未評估，不等於通過）」，
由 `TestEntryPlanReasonLabelsAreDistinctAndKeepTheirCode` 直接斷言。

## 5. CANDIDATE_FOLLOWUP（只記錄，EP-10 一律不實作）

EP-9 的九項全數保留在該次 run 的 `SUMMARY.md` 內，由 `cmd/ep9-study/followups.go` 由當次數字產生。
EP-10 另外把下列三項寫成明確條目：

**A. 缺少的 Entry Requirement 評估器（`NEAR_SUPPORT` / `ELEVATED_EVIDENCE`）**
未來的工作必須定義：正典的評估器、它的 point-in-time 重建、PASS/FAIL 行為，以及它造成的實際狀態轉換。
**`UNKNOWN` 不等於 `PASS`。** 帶著 `STATUS_REQUIREMENT_NOT_EVALUATED` 的那批計畫是
**EVALUATOR-BLOCKED ENTRY CANDIDATES**，不是「差一點的 BUY_NOW」、不是「接近的 BUY_NOW」、
也不是「被否決的 BUY_NOW」；它們的事後結果是 `POST_HOC_DISCOVERY`。EP-10 已把
原本使用 "would-be `BUY_NOW`" 用語的**兩個**地方都改成這個說法（只改用語，數字未動）：
`cmd/ep9-study/followups.go`（產生 SUMMARY 條目的地方）與
`internal/entryplanbacktest/study/observe.go` 的 `ExecutableStatuses` 註解（該母體的正典定義處）。
`ExecutableStatuses` 本身未變動，數字也沒有動。這件事**沒有任何測試守著**：它是一次全庫
grep 的結果 —— EP-10 收尾時對 `would-be` / `near BUY_NOW` / `rejected BUY_NOW` /
「差一點」/「接近的 BUY_NOW」/「被否決的 BUY_NOW」/「準 BUY_NOW」等寫法逐一搜尋，
13 個命中全部是**否定句或歷史引用**（另有一處 README 的漲停鎖量規則與本議題無關），
沒有任何一處是斷言。日後新增文字時請重跑這個 grep。

**B. 歷史 regime 證據的整合**
需要：production 端安全的歷史 regime 介面、歷史 posture、breadth 存活者偏誤的處理方式，以及
replay 對 archived 的驗證。**不可以只是把現在的 replay 接到 production 上。**
現況：`archived_regime_n = 0`、`posture = UNKNOWN`、`posture_dependent_rules = NOT_EVALUABLE`、
`replay_not_production_wired = true`。

**C. 回檔選擇效應的驗證**
EP-9 觀察到「等到回檔成交」與「沒等到」兩群的後續表現差異很大。要把「成交價變好」與「等待本身選到
了哪些股票」分開，需要配對／cohort 分析。**EP-9 與 EP-10 都沒有宣稱因果。**

**D.（EP-10 新增）`run.json` 的六個 run-level 旗標尚未是具名 JSON 欄位**
`production_historical_execution` / `replay_not_production_wired` / `archived_regime_n` /
`posture` / `posture_dependent_rules` / `breadth_survivorship_bias` 目前在 `run.json` 內是
`conclusions_hold_under` 與 `caveats` 的散文。EP-10 已把它們補成 `metrics.csv` 的具名鍵；把
`internal/entryplanbacktest/metadata.go` 的契約一併加上具名欄位是後續工作，不在 EP-10 範圍。

**E.（EP-10 新增）④ 的 fallback 價位沒有任何標示**
`scorer.go:730-742` 在 ATR 或停損不可用時以固定比例替代，而 ④ 印出來的數字看起來和實測值一樣。
若要讓 ④ 自己說出「這格是替代值」，那是改變既有 production 呈現契約，需要另一個工作項。

以上全部是**候選**。EP-10 沒有依據 EP-9 的任何結果做參數調整或語意變更。
