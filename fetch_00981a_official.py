#!/usr/bin/env python3
import csv
import re
import time
from pathlib import Path

from bs4 import BeautifulSoup
from playwright.sync_api import sync_playwright

ETF = "00981A"
FUND_CODE = "49YTW"
URL = f"https://www.ezmoney.com.tw/ETF/Fund/Info?fundCode={FUND_CODE}"

OUT_DIR = Path("data")
OUT_DIR.mkdir(exist_ok=True)

def norm(s: str) -> str:
    return re.sub(r"\s+", " ", s).strip()

def parse_rows_from_html(html: str):
    soup = BeautifulSoup(html, "lxml")
    rows = []

    for tr in soup.select("tr"):
        cells = [norm(td.get_text(" ", strip=True)) for td in tr.select("td, th")]
        if len(cells) < 3:
            continue

        line = " ".join(cells)

        # 00981A 官方持股可能長這樣：
        # 2330 台積電 11,960,000 9.75%
        # 或 台積電 2330 11,960,000 9.75%
        if not re.search(r"\b\d{4}[A-Z]?\b", line):
            continue
        if "%" not in line:
            continue

        rows.append(cells)

    return rows

def parse_best_effort(cells):
    line = " ".join(cells)

    ticker_m = re.search(r"\b(\d{4}[A-Z]?)\b", line)
    weight_m = re.search(r"([0-9]+(?:\.[0-9]+)?)\s*%", line)

    numbers = re.findall(r"[0-9]{1,3}(?:,[0-9]{3})+(?:\.[0-9]+)?|[0-9]+(?:\.[0-9]+)?", line)

    ticker = ticker_m.group(1) if ticker_m else ""
    weight = weight_m.group(1) if weight_m else ""

    # 找最大的整數型數字當持有股數，避開 ticker / weight
    shares = ""
    for n in numbers:
        raw = n.replace(",", "")
        if ticker and raw == ticker:
            continue
        if weight and raw == weight:
            continue
        try:
            v = float(raw)
            if v >= 1000:
                shares = str(int(v))
                break
        except ValueError:
            pass

    # 股票名稱：移除代號、百分比、數字後，留下中文/英文名稱
    stock_name = line
    stock_name = re.sub(r"\b\d{4}[A-Z]?\b", " ", stock_name)
    stock_name = re.sub(r"[0-9]{1,3}(?:,[0-9]{3})+(?:\.[0-9]+)?", " ", stock_name)
    stock_name = re.sub(r"[0-9]+(?:\.[0-9]+)?\s*%", " ", stock_name)
    stock_name = re.sub(r"\s+", " ", stock_name).strip()

    return {
        "ticker": ticker,
        "stock_name": stock_name,
        "weight_pct": weight,
        "shares": shares,
        "raw": line,
    }

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page(
        locale="zh-TW",
        user_agent="Mozilla/5.0"
    )

    api_urls = []

    def on_response(resp):
        url = resp.url
        low = url.lower()
        if any(k in low for k in ["api", "fund", "etf", "portfolio", "pcf", "holding", "component"]):
            api_urls.append(url)

    page.on("response", on_response)

    page.goto(URL, wait_until="networkidle", timeout=90000)

    # 嘗試點開「基金投資組合」tab
    for label in ["基金投資組合", "投資組合"]:
        try:
            page.get_by_text(label, exact=True).click(timeout=5000)
            page.wait_for_load_state("networkidle", timeout=30000)
            time.sleep(2)
            break
        except Exception:
            pass

    html = page.content()
    browser.close()

rendered_html = OUT_DIR / "00981A_official_rendered.html"
rendered_html.write_text(html, encoding="utf-8")

network_log = OUT_DIR / "00981A_official_network_urls.txt"
network_log.write_text("\n".join(sorted(set(api_urls))), encoding="utf-8")

rows = parse_rows_from_html(html)

raw_rows_file = OUT_DIR / "00981A_official_raw_rows.txt"
with raw_rows_file.open("w", encoding="utf-8") as f:
    for r in rows:
        f.write(" | ".join(r) + "\n")

parsed = [parse_best_effort(r) for r in rows]
parsed = [r for r in parsed if r["ticker"] and r["weight_pct"]]

csv_file = OUT_DIR / "00981A_official_holding.csv"
with csv_file.open("w", newline="", encoding="utf-8-sig") as f:
    writer = csv.DictWriter(f, fieldnames=["ticker", "stock_name", "weight_pct", "shares", "raw"])
    writer.writeheader()
    writer.writerows(parsed)

print(f"rendered html: {rendered_html}")
print(f"network urls : {network_log}")
print(f"raw rows     : {raw_rows_file}")
print(f"csv          : {csv_file}")
print(f"parsed rows  : {len(parsed)}")
