# validate-signals — 訊號驗證報告 / Signal Validation Report

驗證過去 `reports/report_YYYYMMDD.html` 裡 **當時已產出的操作判斷**，看後續 T+3/5/10/20 交易日
是否走對方向。目的不是選擇性證明 Scanner 正確，而是建立
「每日判斷 → 後續追蹤 → 命中率驗證 → 錯誤案例回饋」的閉環。

此功能是 **獨立模組**（`internal/validator` + `cmd/validate-signals`），不影響
`cmd/scanner` 每日掃描流程。

## 用法

```bash
go run ./cmd/validate-signals \
  --reports-dir reports \
  --from 2026-06-01 --to 2026-06-13 \
  --horizons 3,5,10,20 \
  --output   reports/signal_validation_20260706.html \
  --csv-output      data/signal_validation_20260706.csv \
  --snapshot-output data/signal_snapshots_20260706.csv \
  --benchmark 0050
```

| flag | 預設 | 說明 |
|------|------|------|
| `--reports-dir` | `reports` | HTML 報告目錄（只讀 `report_YYYYMMDD.html`，忽略盤中草稿 `_1`）|
| `--from` / `--to` | — | 驗證的報告日期範圍（含端點）|
| `--horizons` | `3,5,10,20` | 前瞻交易日，逗號分隔 |
| `--output` | — | HTML 驗證報告輸出 |
| `--csv-output` | — | 驗證明細 CSV |
| `--snapshot-output` | — | （選用）解析後原始訊號 CSV |
| `--benchmark` | `0050` | 相對強弱基準（`0050`/`006208`…）；留空則用絕對報酬 |
| `--cache-dir` | `.cache` | 前瞻價格來源（沿用專案既有 OHLCV cache）|
| `--tabs` | `watchlist,positions,market` | 要驗證的報告分頁；留空 = 全部 |
| `--market-buy-only` | `true` | 市場掃描分頁只取 BUY 買進候選 |

## 重要設計

- **反 look-ahead**：只讀報告當天已存在的 BUY/REDUCE/WATCH 判斷，不用今日邏輯回溯過去。
- **進場價**：Scanner 是盤後 EOD 報告，故績效以 **隔一交易日開盤價**（缺則收盤價）計算；
  `signal_price` 保留為訊號日收盤參考價。
- **市場掃描分頁**：`市場掃描` tab 會掃約千檔並多數標記 SELL/HOLD，那是篩選狀態而非操作建議。
  預設只納入其 BUY 候選（`--market-buy-only`），避免命中率被全市場廣度稀釋；
  操作型判斷主要來自 `watchlist` / `positions` 分頁。
- **前瞻資料邊界**：cache 只到最近一次抓取日，之後的 horizon 會標記 `PENDING`。
  因此有意義的驗證窗口是**已有足夠後續交易日的較早報告**（例如今天回看兩三週前）。

## 判斷規則

`short` = T+5（缺則 T+3），`long` = T+10（缺則 T+20）；有 benchmark 時納入 excess return，
否則自動退回絕對報酬。

- **BUY_GROUP** — CORRECT：short>0 且不弱於大盤，或 long>0 且不弱於大盤；
  WRONG：short≤−3% 或 long≤−5%。
- **REDUCE_GROUP** — CORRECT：short<0 且弱於大盤，或 long<0 且弱於大盤，或最大跌幅≤−5%；
  WRONG：short≥+5% 且強於大盤，或 long≥+8%。
- **WATCH_GROUP**（WAIT/HOLD/觀察）僅追蹤，不計入命中率。

Result：`CORRECT / WRONG / NEUTRAL / PENDING / NO_PRICE_DATA`。
整體命中率 = CORRECT / (CORRECT + WRONG)。
