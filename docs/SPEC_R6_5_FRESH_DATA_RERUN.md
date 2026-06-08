# R6-5 — Fresh Data Rerun（獨立 fresh-cache 可重現性驗證）

> **Docs only — no code, no config default, no scanner / report / scoring change.**
> 本文件僅整理 R6-5 的對照結果與穩定性判定，不修改任何程式碼、不調整 `configs/config.yaml` 預設值、
> 不改 live scanner / report / RocketScore / WatchAction / ExplosionProb / stop profile 預設。
> 上游：`internal/r6backtest`、`docs/SPEC_R6_BACKTEST_INTERPRETATION.md`、
> `docs/SPEC_R6_3_STOP_BENCHMARK_INTERPRETATION.md`、`docs/SPEC_R6_4_STOP_PROFILE_PROPOSAL.md`。

---

## 0. 文件定位（務必先讀）

```text
本次 R6-5 是同日 fresh rerun / reproducibility check。
不是跨 regime 驗證。
不可把結果包裝成 fresh market regime validation。

R6 是離線回測與策略研究，不是自動交易系統。
所有結論僅作為候選策略與風險評估依據。
不自動下單、不接 broker、不替使用者執行交易。
```

為何是 reproducibility check 而非跨 regime 驗證：開始前已確認現有 `.cache/` 本身就是 **2026-06-08 當日最新資料**
（2y range，樣本末日 2026-06-08）。因此本次「用最新資料重跑」實際比較的是：

```text
OLD   = 現有 .cache 重跑（current cache rerun）
FRESH = 獨立重新抓取的 .cache_fresh_r6_5 重跑（independently re-fetched, same trading day）
```

兩者落在同一交易日、同一 2y 視窗，**diff 預期接近零**；本文件回答的是「R6 結論能否在獨立重抓的資料上重現」，
**不是**「在新的市場 regime（更多空頭 / 殺盤期）下是否仍成立」。後者屬 R6-5 之後的工作。

---

## 1. 方法（fresh data 如何取得、cache 與對照如何保存）

| 項目 | 做法 |
|---|---|
| Fresh 抓取 | 用**現有 scanner binary（程式未改）** + 一份 `/tmp` 臨時研究 config（僅覆寫 `cache_dir`），走既有 `internal/fetcher` → Yahoo chart API，`history_range: 2y`。臨時 config 放 `/tmp`，**不進 repo、不成為 tracked file**。 |
| Fresh cache 位置 | 寫入獨立目錄 **`.cache_fresh_r6_5/`（gitignored）**，**完全不覆蓋 `.cache/`**（`.cache/` 同時是 baseline 與 live scanner 既有快取）。 |
| Proxy 補抓 | 全市場掃描母體為普通股，**不含 ETF proxy `0050`**（Setup D regime 需要）。為在 fresh 資料上完成 Setup D，且不把 OLD 的 0050 混入破壞獨立性，**獨立重抓今天的 `0050`** 進 fresh cache（`fetched_at 2026-06-08`）。 |
| OLD baseline | **以現有 `.cache/` 重新跑一次**輸出到 `reports/r6_5_baseline_old/`，不只依賴未版控的舊 `reports/backtest_*` 當基準。 |
| FRESH | 以 `.cache_fresh_r6_5/` 跑輸出到 `reports/r6_5_fresh/`。 |
| 報表保存 | `reports/` 為 gitignored；OLD / FRESH 分開目錄，互不覆蓋。 |

執行指令：

```bash
# OLD baseline from current .cache
go run ./cmd/r6backtest -cache .cache -out reports/r6_5_baseline_old
go run ./cmd/r6backtest -cache .cache -out reports/r6_5_baseline_old -stopbench

# FRESH from independently re-fetched .cache_fresh_r6_5
go run ./cmd/r6backtest -cache .cache_fresh_r6_5 -out reports/r6_5_fresh
go run ./cmd/r6backtest -cache .cache_fresh_r6_5 -out reports/r6_5_fresh -stopbench
```

---

## 2. Coverage diff

| 指標 | OLD（current `.cache`） | FRESH（`.cache_fresh_r6_5`） | 說明 |
|---|---|---|---|
| universe（backtest 載入） | 1974 | 1967 | min-bars 130 過濾後 |
| cache 檔數 | ~1981 | 1971 | — |
| coverage 起 | 2024-06-03 | 2024-06-11 | OLD 含少量較早抓取檔（6/4）→ 視窗略長 |
| coverage 迄 | 2026-06-08 | 2026-06-08 | 同一交易日 |
| trading days | 488 | 483 | 差 5 日（OLD 起點較早） |
| fetched_at | 混合（少量 6/4 + 多數 6/8） | 全部 6/8（~23:16；0050 ~23:23） | FRESH 為均勻同日抓取 |
| fetch 失敗 / skipped | — | 無實質失敗（log 無 EOF/error/timeout 批量；普通股清單 market 1969 + pos 11 + wl 59 全數抓回） | — |

差異成因：FRESH 全部於同日重抓 → 2y 視窗起點一致（2024-06-11）；OLD `.cache` 殘留少量 6/4 抓取檔與多檔 ETF，
使母體略大、視窗略長。屬資料新鮮度造成的**邊際結構差異**，非策略差異。

---

## 3. CSV-level 可重現性（最硬證據）

對 OLD / FRESH 的 per-trade 與 benchmark CSV 直接逐列比對：

| 輸出 | 列數 | 移除非數值欄後的差異 | 結論 |
|---|---|---|---|
| pullback（A/B/C，26334 trades） | 相同 | **移除 `stock_name` 欄後 0 差異** | 全部交易數值逐格相同；唯一差異為 **20 個代號的公司名稱字串**（如 1710 東聯/東華，資料來源命名不同） |
| stop benchmark（10 setup × 9 policy = 90 列） | 相同 | **逐 byte 0 差異** | R6-3 stop benchmark 完整重現 |
| crash survivors（Setup D，537 trades） | 相同（538 含表頭） | **移除 `stock_name` + `breadth_below_ma20_pct` 兩欄後 0 差異** | 唯一數值差異為 `breadth_below_ma20_pct`（殺盤日全市場低於 MA20 比例，header 明載 **context-only、非硬門檻**）因母體 1974→1967 位移 ≤0.06pp；交易報酬 / RS / 相對報酬 / 停損全相同 |

> 即：所有**交易結果與風險指標**在 OLD / FRESH 完全一致；可觀察到的差異全屬資料來源命名與非門檻 context 欄位的微小位移。

---

## 4. Setup diff（逐點驗證）

各 setup 的 summary 指標 OLD / FRESH **逐格相同**（見第 3 節）；下列數值 OLD = FRESH。

### 4.1 Setup A — A_MA20 是否仍優於 A_MA60 → ✅ STABLE

| | sample | win_20d | avg_20d | avg_60d | stop_hit |
|---|---|---|---|---|---|
| A_MA20_PULLBACK | 2361 | 35.9% | 3.0% | 5.0% | 81.1% |
| A_MA60_PULLBACK | 791 | 26.7% | 1.4% | 1.6% | 92.9% |

A_MA20 在每個 horizon 的 win_rate 與 avg_return 皆高於 A_MA60，60d 報酬 5.0% vs 1.6%。**排序保留。**

### 4.2 Setup B — 15–20% deep pullback 是否仍較強 → ✅ STABLE

hold-to-horizon 60d 報酬隨回檔深度單調上升：

| bucket | B_5 | B_8 | B_10 | B_15 | B_20 |
|---|---|---|---|---|---|
| hold_avg_60d | 21.1% | 22.6% | 23.0% | 23.5% | 24.5% |

深回檔（15–20%）的 hold return 仍最強；同時 stop_delta 最負（最依賴「不被洗出」），與 R6 原結論一致。**保留。**

### 4.3 Setup C — C_VCP_MA20 是否仍有足夠樣本與優勢 → ✅ STABLE

| | sample | win_20d | avg_20d | avg_60d |
|---|---|---|---|---|
| C_VCP_MA20_RETEST | 2592 | 34.0% | 2.4% | 3.2% |
| C_VCP_MA60_RETEST | 1401 | 29.1% | 1.6% | 2.0% |
| C_VCP_BASE_LOW_RETEST | 288 | 34.7% | 1.0% | 1.0% |

C_VCP_MA20 樣本 2592（充足），win/avg 在各 horizon 優於 MA60 / BASE_LOW，且 baseline stop_hit 85.7% 偏高（停損偏嚴）仍成立。**樣本與優勢保留。**

### 4.4 Setup D — HIGH_RS 在 crash regime 是否仍相對抗跌 → ✅ STABLE（但 confidence 永遠 LOW）

- event_count: **3**，range 2025-03-13 ~ 2026-03-31，proxy 0050，avg market_proxy_return_20d −9.4%。

| cohort | n | win_20d | avg_20d | hold_20d | rdd_avg | avg_rel_ret_20d |
|---|---|---|---|---|---|---|
| HIGH_RS | 1624 | 41.6% | 0.7% | 2.3% | −4.2% | **+6.9pp** |
| LOW_RS | 3519（OLD 3530） | 49.1% | 0.1% | 1.1% | −2.1% | **−0.4pp** |

HIGH_RS 相對市場報酬 +6.9pp vs LOW_RS −0.4pp，「殺盤時 RS 高者相對抗跌（相對強）」方向不變。
LOW_RS n 因母體少 7 檔而 3530→3519，指標四捨五入後不變。**方向保留；event_count 極少，confidence 仍 LOW，不可外推。**

---

## 5. Stop benchmark diff（R6-3）

stop benchmark CSV **逐 byte 相同**（第 3 節），以下結論 OLD = FRESH。

1. **BASELINE 是否仍偏嚴 → ✅ STABLE。** 每個 setup 中 stop_hit_rate 最高（75–96%）、avg_return 最差、realized drawdown 最淺 → 仍是「保護最強但最易洗出」。
2. **ATR_3 是否仍是較穩健候選 → ✅ STABLE。** 含停損者中 avg_60d 最佳或並列最佳、`stop_delta` 最接近 0、stop_hit 中等、回撤介於 BASELINE 與 NO_STOP 之間。
3. **PCT_15（PCT_ONLY_15）是否仍是簡化候選 → ✅ STABLE。** 淺回檔接近 ATR_3、深回檔略遜；勝在規則簡單。
4. **NO_STOP 是否仍報酬最高但回撤最大 → ✅ STABLE。** 每個 setup 20d / 60d avg_return 全面最高，dd_p90 尾部最深；報酬優勢「非免費」。

（單一 setup `C_VCP_BASE_LOW_RETEST` 的 20d best 為 ATR_3、60d 仍 NO_STOP — 與 OLD 完全相同，非翻轉。）

---

## 6. Stability verdict

```text
VERDICT: STABLE
```

判定依據（全部滿足）：

- **排序保留**：A_MA20 > A_MA60；B 深回檔 hold 最強；C_VCP_MA20 樣本足且優於其餘；
  stop benchmark 中 BASELINE 最嚴、ATR_3 最穩健、NO_STOP 報酬最高 + 回撤最深。
- **無符號翻轉**：HIGH_RS 相對報酬正、LOW_RS ~0；各 stop_delta 符號不變。
- **樣本充足度維持**：C_VCP_MA20 = 2592；A/B/C 樣本與 OLD 完全相同。
- **數值漂移**：交易結果 0 漂移（CSV 逐格相同）；僅 metadata（universe/coverage/LOW_RS n）與非門檻 breadth context 微移，遠在容忍範圍內。

---

## 7. 是否需暫停 R6-4 stop profile 推進

```text
不需要暫停（NO PAUSE）。
```

R6-5 預設的暫停條件皆未觸發：

- ATR_3 仍為含停損者最佳 / 並列最佳 → 未翻轉。
- BASELINE 仍最嚴 / 報酬最差 → 「baseline 過嚴」前提未動搖。
- NO_STOP 仍 dd 尾部最深 → regime-bias 警語不需重寫。
- C_VCP_MA20 樣本 2592 充足 → 未崩。

但這**不等於可以推進定案**：R6-5 僅為同日獨立重抓的 reproducibility check，
`docs/SPEC_R6_4_STOP_PROFILE_PROPOSAL.md` 所列「定案前需 fresh / 全市場 / **跨 regime** 再驗證」仍未完成。
**stop profile 預設維持不變（BASELINE 不改；ATR_3 / PCT_15 仍為候選）。**

---

## 8. 測試與 sanity check

| 檢查 | 結果 |
|---|---|
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `go test ./...` | PASS（含 `internal/r6backtest` forbidden-token 測試） |
| forbidden tokens（BUY / AUTO_BUY / PLACE_ORDER）掃描 OLD+FRESH 報表 | 無命中 |
| 可重現性（CSV-level） | A/B/C 26334 trades 數值 0 差異；stop benchmark 90 列逐 byte 相同；Setup D 除非門檻 breadth context 外 0 差異 |
| fresh cache 末日抽查（2330 / 2317 / 0050） | 末根 bar = 2026-06-08，fetched_at = 2026-06-08，n=482~483 |
| 引擎 / setup / stop 邏輯 | 未改動（僅以 `-cache` flag 指向 fresh 目錄重跑） |

---

## 9. 影響檔案

**新增（本 commit 唯一提交）**

- `docs/SPEC_R6_5_FRESH_DATA_RERUN.md`

**新增但 gitignored（不提交）**

- `.cache_fresh_r6_5/`、`reports/r6_5_fresh/`、`reports/r6_5_baseline_old/`

**未進 repo（放 `/tmp`）**

- 臨時研究 config、proxy 補抓清單

**保證未碰**

- 全部 `internal/r6backtest/*.go`（engine / setups / stops / output / regime / types）
- `cmd/r6backtest/main.go`、`cmd/scanner/**` 與所有 live scanner 程式
- `configs/config.yaml` 預設、`configs/sectors.yaml`、`stocks.yaml`
- RocketScore / WatchAction / ExplosionProb / report / 評分 / stop profile 預設
- 既有 `.cache/`（未覆蓋）、既有 R6 / R6-3 / R6-4 文件（本文件為新增）
- 無任何 broker / 自動下單 / 排程下單路徑

---

## 10. 最終定位宣告（務必保留）

```text
本次 R6-5 是同日 fresh rerun / reproducibility check，不是跨 regime 驗證。
R6 是離線回測與策略研究，不是自動交易系統。
不自動下單、不接 broker、不替使用者執行交易。
BASELINE 不改；ATR_3 / PCT_15 是候選，不是正式預設；live scanner 預設不改。
結論為 STABLE 僅代表「可在獨立重抓資料上重現」，不代表已通過跨 regime 驗證。
```

---

*（本文件為 R6-5 獨立 fresh-cache 可重現性驗證，docs only，不修改任何程式碼、設定、scanner 或 report。
所有「仍成立 / STABLE」皆指同日重抓下的可重現性，最終策略與停損政策仍須以台股 fresh / 全市場 / 跨 regime 資料驗證為準。）*
