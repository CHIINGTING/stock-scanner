#!/usr/bin/env python3
"""Build a code -> Chinese-name map from the official TWSE/TPEX ISIN listings.

The daily report (and the data cache) carries English names for some stocks
(e.g. 3290 -> "DONPON PRECISION INC"). This script fetches the authoritative
Chinese names from the TWSE ISIN service and writes them to
data/stock_names_zh.yaml, which extract.py loads to localise candidate names.

Sources (Big5-encoded HTML tables):
    strMode=2  上市 TWSE
    strMode=4  上櫃 TPEX

Only 4-digit numeric codes are kept (common stock + 0050-style ETFs); 6-digit
warrants are skipped. Run again any time to refresh:

    python3 .claude/skills/buy-watch-candidates/build_name_map.py
"""
import os
import re
import urllib.request

OUT = os.path.join(
    os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(__file__)))),
    "data",
    "stock_names_zh.yaml",
)
ROW_RE = re.compile(r"<td[^>]*>(\d{4})　([^<]+)</td>")


def fetch(mode: int) -> dict[str, str]:
    url = f"https://isin.twse.com.tw/isin/C_public.jsp?strMode={mode}"
    raw = urllib.request.urlopen(url, timeout=30).read().decode("big5", "ignore")
    out: dict[str, str] = {}
    for code, name in ROW_RE.findall(raw):
        name = name.strip()
        if name:
            out.setdefault(code, name)
    return out


def main() -> None:
    tw = fetch(2)
    two = fetch(4)
    merged = dict(two)
    merged.update(tw)  # 上市優先
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    with open(OUT, "w", encoding="utf-8") as f:
        f.write("# 台股 code -> 中文股名對照（TWSE 上市 + TPEX 上櫃 ISIN 官方清單）\n")
        f.write("# 由 build_name_map.py 產生，可重跑刷新。供 extract.py 把英文股名補成中文。\n")
        f.write(f"# 上市 {len(tw)} 檔｜上櫃 {len(two)} 檔｜合計 {len(merged)} 檔\n")
        for code in sorted(merged):
            f.write(f'"{code}": "{merged[code]}"\n')
    print(f"上市(TW) {len(tw)}｜上櫃(TWO) {len(two)}｜合計 {len(merged)}")
    print(f"輸出：{OUT}")


if __name__ == "__main__":
    main()
