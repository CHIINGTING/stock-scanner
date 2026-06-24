#!/usr/bin/env bash
# fetch_00981a_daily.sh — 每日抓取 00981A 官方持股並轉 CSV 的整合腳本。
#
#   bash scripts/fetch_00981a_daily.sh             # 抓最新
#   bash scripts/fetch_00981a_daily.sh 2026/06/12  # 指定日期
#
# 流程：建立資料夾 -> Playwright 下載官方 XLSX/XLS -> 找最新檔 -> (xlsx) 轉 CSV -> 寫 log。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OFFICIAL_DIR="data/official"
CSV_DIR="data/official/csv"
LOG_DIR="logs"
LOG="$LOG_DIR/fetch_00981a_daily.log"
DATE_ARG="${1:-}"

mkdir -p "$OFFICIAL_DIR" "$CSV_DIR" "$LOG_DIR"

log() { printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*" | tee -a "$LOG"; }

log "==== fetch_00981a_daily start (date_arg='${DATE_ARG}') ===="

# 1) 下載官方檔案（需 node + playwright）。
if ! command -v node >/dev/null 2>&1; then
  log "ERROR: 找不到 node，請先安裝 Node.js 與 Playwright（見 docs/00981A_DAILY_HOLDING.md）。"
  exit 1
fi

log "執行 Playwright 下載 ..."
if node scripts/fetch_00981a_daily_official.mjs ${DATE_ARG:+"$DATE_ARG"} >>"$LOG" 2>&1; then
  log "Playwright 下載完成。"
else
  rc=$?
  log "ERROR: Playwright 下載失敗 (exit=$rc)。若為選擇器問題，請看 $OFFICIAL_DIR/00981A_debug.png 與 log 中的控制項傾印。"
  exit "$rc"
fi

# 2) 找出最新下載的 .xlsx / .xls。
latest="$(ls -t "$OFFICIAL_DIR"/*.xlsx "$OFFICIAL_DIR"/*.xls 2>/dev/null | head -n1 || true)"
if [[ -z "$latest" ]]; then
  log "ERROR: $OFFICIAL_DIR 內找不到 .xlsx / .xls。"
  exit 1
fi
log "最新官方檔：$latest"

# 3) 若為 .xlsx 則轉 CSV。
case "$latest" in
  *.xlsx)
    if ! command -v python3 >/dev/null 2>&1; then
      log "ERROR: 找不到 python3，無法轉 CSV。"
      exit 1
    fi
    log "轉 CSV ..."
    python3 scripts/xlsx_to_csv.py "$latest" >>"$LOG" 2>&1
    log "CSV 已輸出到 $CSV_DIR/"
    ;;
  *.xls)
    log "NOTE: 取得 .xls（舊格式），xlsx_to_csv.py 僅支援 .xlsx。請另存為 .xlsx 或改用其他工具轉換。"
    ;;
esac

log "==== fetch_00981a_daily done ===="
