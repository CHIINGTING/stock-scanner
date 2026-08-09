# SPEC R10 — Market Dashboard (Market Regime Engine)

> **來源**：本文 §A 為使用者提出的原始需求全文（2026-07 提出，2026-08-09 補歸檔）。
> `docs/SPEC_R9_1_MARKET_REGIME_LAYER.md` 開頭指向的就是這份文件——R9 只看 0050 的價格／廣度，
> 被本案取代：Market Dashboard 接手 regime 這件事，改用五態分類，並把 R9 的價格廣度想法
> 降級成其中一個評分模組。
>
> §B 是**實作對照表**（依 2026-08-09 的工作區狀態），標明哪些已完成、哪些刻意留到後續階段、
> 哪些與原始需求有出入。程式碼位於 `internal/market/` + `cmd/market-dump/`（尚未 commit）。

---

# §A 原始需求

## Goal

目前 stock-scanner 已經有：

- 新聞情緒 (News Shadow)
- 股票分析
- 產業分析
- Podcast 情報

希望新增一個更上層的 Market Dashboard。

理念：

不要一開始就分析股票。

而是先回答：

> 現在市場適合進攻？
> 還是保守？
> 哪些族群勝率最高？

Market Dashboard 應該成為所有分析的第一頁。

---

## Architecture

新增 package

market/

例如

```
market/
    provider/
    analyzer/
    model/
    service/
```

不要影響既有 scanner。

Market Dashboard 是額外模組。

---

## Daily Flow

每天開盤前產生

Market Report

例如

Market Score

76 /100

State

Bull Pullback

Confidence

87%

---

## Data Sources

第一階段先預留 Provider。

不要先寫死。

例如

```
interface {
    Futures()
    ForeignBuySell()
    Margin()
    Options()
    Currency()
    VIX()
}
```

之後再接：

- TWSE
- TAIFEX
- FinMind
- Fugle
- Yahoo
- 自建資料

Provider 可替換。

---

## Score Engine

市場分數：

0~100

不是固定權重。

而是由各模組貢獻。

例如

| 模組 | 權重 |
| --- | --- |
| Foreign Futures | 20% |
| Foreign Cash | 20% |
| Margin | 15% |
| Options | 15% |
| Currency | 10% |
| VIX | 10% |
| Sector Cycle | 10% |

最後輸出

Market Score。

---

## Foreign Futures Module

每天保存：

Date / Net Position / Daily Change / Trend

例如

2026-07-14 ／ Net Position -83390 ／ Daily Change -2324 ／ Trend More Short

Trend 判斷

| Daily Change | Trend |
| --- | --- |
| <= -3000 | Aggressive Short |
| -3000 ~ -500 | More Short |
| -500 ~ +500 | Stable |
| +500 ~ +3000 | Cover Short |
| >= +3000 | Aggressive Cover |

另外增加 Extreme Indicator

| Net Position | Indicator |
| --- | --- |
| > -10000 | Neutral |
| -10000 ~ -30000 | Light Short |
| -30000 ~ -50000 | Short |
| -50000 ~ -70000 | Heavy Short |
| < -70000 | Extreme Short |

不要把 Extreme Short 直接視為 Bearish。

因為有可能只是避險。

需要配合其他資料。

---

## Foreign Cash Module

輸出

Buy / Sell / Net / Trend

例如

Foreign Buy +230億 ／ Trend Strong Buy

---

## Margin Module

分析

融資 / 融券

判斷：

散戶是否追價。

例如

股價上漲 + 融資下降 → Bullish

股價下跌 + 融資增加 → Bearish

---

## Currency Module

美元 DXY

美元台幣 USD/TWD

分析：Risk On / Neutral / Risk Off

---

## VIX Module

分析：Normal / Elevated / Fear / Extreme Fear

---

## Sector Cycle

直接重用目前已有資料：

Memory / Passive / Power / HVDC / PCB / ABF / Networking / AI / Storage

每個產業都有：

Cycle Score / News Score

最後整合。

---

## News

直接重用

News Shadow。

不用重寫。

只需要引用。

---

## Market Regime

最後不是只有 Bull / Bear。

請分類：

Bull Market / Bull Pullback / Sideways / Distribution / Bear Market

每種 Regime 都要有：description / strategy / risk

例如

Bull Pullback

- Description：長期仍偏多，短期修正。
- Strategy：優先布局強勢族群。
- Risk：避免追高。

---

## Dashboard

新增 Market Dashboard，包含：

1. Market Score
2. Market Regime
3. Confidence
4. Foreign Futures
5. Foreign Cash
6. Margin
7. Currency
8. VIX
9. Sector Ranking
10. Top Opportunities
11. Top Risks

Sector Ranking

例如

```
★★★★★ Memory
★★★★☆ Passive
★★★★☆ Power
★★★☆☆ PCB
★★☆☆☆ Steel
★☆☆☆☆ Shipping
```

Top Opportunities

列出目前最值得觀察族群。例如 Memory — 原因：價格持續上漲／新聞偏多／需求增加

Top Risks

例如 Steel — 原因：景氣循環轉弱／新聞偏空／價格下降

---

## Storage

每天保存歷史。

例如 `market/YYYY-MM-DD.json`

方便：畫圖 / 回測 / 統計 / 驗證策略。

---

## Future

第二階段加入：Put Call Ratio、外資成本、美國十年公債、SOX、NASDAQ、美元指數、FedWatch、CPI、PPI

全部做成可插拔 Provider。

---

## Important

請保持：

- 高內聚
- 低耦合
- Provider Pattern
- Interface First
- 可測試
- 不影響現有 scanner
- 保留未來新增資料來源的彈性

Market Dashboard 應作為 stock-scanner 的最高層分析，所有股票分析都應建立在 Market Regime
的判斷之上，而不是直接分析個股。

---

# §B 實作對照（2026-08-09）

程式碼：`internal/market/{model,provider,analyzer,service}` + `cmd/market-dump`，含測試，
`go test` 綠。**尚未 commit。**

## B-1 已完成且符合需求

| 需求 | 實作 |
| --- | --- |
| 四層 package | `model` / `provider` / `analyzer` / `service`，`model` 零內部相依，無循環 |
| 不影響既有 scanner | `internal/market/` 完全沒有 import `internal/scanner`、`internal/report`、`cmd/scanner` |
| Provider Pattern / Interface First | `provider.Provider` 介面方法即需求列的 Futures / ForeignBuySell / Margin / Options / Currency / VIX，另加 Sectors；未實作的來源回 `ErrUnavailable`，單一來源失敗被隔離，其餘照常出表 |
| 七模組權重 20/20/15/15/10/10/10 | `model.defaultWeights`，且**權重放 config 不寫死**；模組缺席時自動重新正規化 |
| Futures Trend 五級 | `-3000 / -500 / +500 / +3000` 完全照需求，且進 config 可調 |
| Extreme Indicator 五級 | `-10000 / -30000 / -50000 / -70000` 完全照需求 |
| Extreme Short ≠ Bearish | 有明確 caveat；`cmd/market-dump` 的示範資料就是刻意做成「極度偏空但屬避險」的情境 |
| Margin 判斷 | 價漲＋融資降 → Bullish；價跌＋融資增 → Bearish；價漲＋融資增 → 追價警告 |
| Regime 五態 + description/strategy/risk | `model.RegimeInfo` 三個欄位齊備，另加 Reasons/Caveats |
| Dashboard 十一項 | Market Score／Regime／Confidence／七個模組（在 `MarketScore.Modules`）／Sector Ranking（★1–5）／Top Opportunities／Top Risks |
| Storage | `data/market/YYYY-MM-DD.json`，每日一檔 |
| 可測試 | 各 analyzer 皆有單元測試；`service` 有 storage 測試 |

額外加的紅線（需求沒寫，實作自行加上）：regime 的 description/strategy/risk **不得包含買／賣／
下單／進場價**，只描述環境。

## B-2 刻意留到後續階段（符合「第一階段先預留 Provider」）

| 項目 | 現況 |
| --- | --- |
| 真實資料源（TWSE / TAIFEX / FinMind / Fugle / Yahoo） | 只有 `provider/mock`，全部回確定性假資料 |
| 「每天開盤前產生」 | 沒有排程；只有 `cmd/market-dump` 手動離線產生 |
| 「成為所有分析的第一頁」 | 未接進日報；`market.enabled` / `market.show` 兩層開關預設 false，且尚未在 `configs/config.yaml` 列出 |
| 第二階段資料（Put/Call、十年債、SOX、CPI…） | 未做，介面已可擴充 |

## B-3 與需求有出入，需要你決定

1. **Confidence 是分級不是百分比。**
   需求寫「Confidence 87%」，實作只有 `LOW` / `MEDIUM` 兩級（**刻意沒有 HIGH**），由
   `confidence_min_modules`（預設 5）決定。理由是門檻尚未校準，給百分比會製造假精確——
   這與 R6／R9 的紀律一致。若你要百分比顯示，需要先定義它由什麼推導。

2. **News Shadow 還沒真的被引用。**
   需求寫「直接重用 News Shadow，不用重寫，只需要引用」。目前 `internal/market/` **完全沒有
   import `internal/news`**：`SectorScore.NewsScore` 這個欄位有留，但值是由 Provider 餵進來的，
   而現在只有 mock provider。接線是 phase 2 的工作，欄位已預留。

3. **Sector Cycle 還沒接現有族群資料。**
   同上——需求說「直接重用目前已有資料（Memory / Passive / Power / …）」，但 `Provider.Sectors()`
   目前只有 mock，沒有讀 `configs/sectors.yaml`，也沒有接 scanner 既有的輪動計算。

4. **Regime 分數門檻是自訂的。**
   需求沒給 Market Score → Regime 的切點，實作暫用 `bull_min 65 / bull_pullback_min 55 /
   bear_max 40`，程式碼明確標記 **BASELINE, NOT CALIBRATED**。要校準需要歷史資料，而歷史資料
   需要真實 provider——所以這件事排在 B-2 之後。
