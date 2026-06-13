# etfflow-ingest — ETF Flow 手動匯入速查

把**手動匯出**的 ETF 持股 CSV 轉成 R8 snapshot JSON，供 scanner 的 ETF Flow（report ⑨）讀取。
這是維運/資料轉檔工具，不在 scanner 主流程內。完整設計見 `docs/SPEC_R8_5_MANUAL_INGEST.md`。

> ⚠️ 這**不是 crawler**：不自動下載 ETF 持股、不接 broker、不下單、不做任何交易執行。
> CSV 由你手動匯出後放進來。

---

## 1. CSV 欄位格式

表頭（順序無關、大小寫無關，容忍前後空白／千分位逗號／`%` 後綴）：

```text
stock_code, stock_name, holding_shares, holding_raw_unit, holding_weight
```

- `holding_shares`：**原始數量**（不是已換算股數），搭配 `holding_raw_unit` 轉成股。
- `holding_raw_unit`：支援 `股` / `張` / `千股`（張、千股 ×1000 → 股）。**未知單位會報錯，不會猜。**
- `holding_weight`：權重（%），可帶 `%`。

範例（手動建立，**勿 commit**）：

```text
stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight
2330,台積電,1200,張,12.5
2303,聯電,800000,股,6.0
```

---

## 2. 00981A（active ETF）範例指令

```bash
go run ./cmd/etfflow-ingest \
  --etf-code 00981A \
  --etf-name "統一台股增長主動式 ETF" \
  --etf-type active \
  --date 2026-06-10 \
  --input ./incoming/00981A_20260610.csv \
  --snapshot-dir data/etf_holdings \
  --source-url "manual"
```

## 3. 00919（passive / high-dividend ETF）範例指令

```bash
go run ./cmd/etfflow-ingest \
  --etf-code 00919 \
  --etf-name "群益台灣精選高息 ETF" \
  --etf-type passive \
  --date 2026-06-10 \
  --input ./incoming/00919_20260610.csv \
  --snapshot-dir data/etf_holdings \
  --source-url "manual"
```

`--etf-type` 只接受 `active` 或 `passive`；`--date` 為 `YYYY-MM-DD`；任一必填旗標缺漏會報錯。

---

## 4. snapshot 輸出位置

```text
data/etf_holdings/<etf_code>_<as_of_date>.json
# 例：data/etf_holdings/00981A_2026-06-10.json
```

連續多日各跑一次即可累積歷史；scanner 端用 `etf_flow.history_days`（預設 10）回看。

---

## 5. 如何「本機」開啟（預設關閉，opt-in）

`configs/config.yaml` 預設 `enable_etf_flow: false`、`show_etf_flow: false`。本機驗證時暫時改為：

```yaml
scanner:
  enable_etf_flow: true    # 載入 snapshot 並 attach 到 watchlist
  show_etf_flow: true      # report ⑨ 顯示
  etf_flow:
    snapshot_dir: "data/etf_holdings"
    history_days: 10
    watch_etfs:
      - { code: "00981A", type: "active" }
      - { code: "00919",  type: "passive" }
```

> 這個開關**只在本機驗證用**，請勿把 `true` 提交進 repo（或改用一份獨立的本機 config 檔）。
> 填了 `watch_etfs` 不代表啟用——`enable_etf_flow: false` 時仍完全不載 snapshot、不 attach。

跑 scanner 後，watchlist 內相關個股的卡片會出現「⑨ ETF Flow / Active Rotation Context」。

---

## 6. 產物不要進版控

`data/etf_holdings/*.json` 與 `data/etf_holdings/raw/*.csv` 已被 `.gitignore` 忽略；只有
`.gitkeep` 進 repo 以保留目錄結構。**請勿 commit 真實 snapshot JSON 或原始 CSV。**

---

## 7. 性質與紅線

- 不是 crawler、不自動下載 ETF 持股、不接 broker、不下單、不做交易執行。
- report ⑨「ETF Flow」是**延遲揭露**資料（T+N），**不是盤中即時訊號**，也不是交易指令。
- ETF Flow 只是觀察 context：不影響 RocketScore / WatchAction / ExplosionProb / 排序。
- strength 門檻目前**只觀察、不校準**（維持預設值），待蒐集樣本後再議。
