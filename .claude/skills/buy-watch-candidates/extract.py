#!/usr/bin/env python3
"""Extract BUY & WATCH candidates from a daily report's 市場掃描 (market-scan) tab.

The report HTML (reports/report_YYYYMMDD.html) renders every scanned stock in the
`<div id="tab-market">` section with an action badge — STRONG BUY / BUY / WATCH /
HOLD / REDUCE / SELL. This script keeps only the actionable signals (STRONG BUY +
BUY -> `buy`, WATCH -> `watch`) and writes them to reports/candidates_YYYYMMDD.yaml.

Usage:
    python3 extract.py [report.html]      # explicit report file
    python3 extract.py 20260609           # reports/report_20260609.html
    python3 extract.py                     # today's report, else newest report_*.html
"""
import glob
import html
import os
import re
import sys
from datetime import date

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(__file__))))
REPORTS_DIR = os.path.join(ROOT, "reports")
NAME_MAP_PATH = os.path.join(ROOT, "data", "stock_names_zh.yaml")
CJK_RE = re.compile(r"[一-鿿]")


def load_name_map() -> dict[str, str]:
    """code -> 中文股名，來自 build_name_map.py 產生的對照檔（可缺檔）。"""
    if not os.path.isfile(NAME_MAP_PATH):
        return {}
    out: dict[str, str] = {}
    line_re = re.compile(r'^"(\d+)":\s*"(.+)"\s*$')
    with open(NAME_MAP_PATH, encoding="utf-8") as f:
        for line in f:
            m = line_re.match(line.strip())
            if m:
                out[m.group(1)] = m.group(2)
    return out


def localise(code: str, name: str, name_map: dict[str, str]) -> str:
    """報告股名若不含中文，且對照表有官方中文名，則改用中文名。"""
    if name_map and not CJK_RE.search(name):
        return name_map.get(code, name)
    return name


def resolve_report(arg: str | None) -> str:
    if arg:
        if os.path.isfile(arg):
            return arg
        # bare date like 20260609
        cand = os.path.join(REPORTS_DIR, f"report_{arg}.html")
        if os.path.isfile(cand):
            return cand
        sys.exit(f"找不到報告：{arg}")
    today = os.path.join(REPORTS_DIR, f"report_{date.today():%Y%m%d}.html")
    if os.path.isfile(today):
        return today
    reports = sorted(glob.glob(os.path.join(REPORTS_DIR, "report_2*.html")))
    if not reports:
        sys.exit("reports/ 內找不到任何 report_*.html")
    return reports[-1]


# Pull a single market-scan tab row's sym / name / action out of the HTML.
ROW_RE = re.compile(
    r'<td class="sym">(?P<code>[^<]+)</td>\s*'
    r'<td class="name-col">(?P<name>[^<]*)</td>'
    r'.*?action-badge action-(?P<css>[\w-]+)">(?P<action>[^<]+)<',
    re.S,
)


def market_segment(html_text: str) -> str:
    """Isolate the 市場掃描 tab so we never pick up positions / rotation rows."""
    m = re.search(
        r'<div id="tab-market".*?>(.*?)<div id="tab-rotation"', html_text, re.S
    )
    if not m:
        sys.exit("報告中找不到市場掃描分頁 (tab-market)")
    return m.group(1)


def extract(report_path: str):
    text = open(report_path, encoding="utf-8").read()
    seg = market_segment(text)
    name_map = load_name_map()
    buy, watch = [], []
    for m in ROW_RE.finditer(seg):
        action = m.group("action").strip()
        code = m.group("code").strip()
        item = {
            "code": code,
            "name": localise(code, html.unescape(m.group("name").strip()), name_map),
        }
        if action in ("STRONG BUY", "BUY"):
            buy.append(item)
        elif action == "WATCH":
            watch.append(item)
    return buy, watch


def render(report_path: str, buy, watch) -> str:
    base = os.path.basename(report_path)            # report_20260609.html
    stamp = re.search(r"(\d{8})", base).group(1)    # 20260609
    rdate = f"{stamp[:4]}-{stamp[4:6]}-{stamp[6:]}"
    lines = [
        "# 飆股候選清單 — 市場掃描 BUY & WATCH",
        f"# 來源：{base}（市場掃描分頁）",
        f"# 產生日期：{rdate}",
        "meta:",
        f"  report_date: {rdate}",
        f"  source: {base}",
        "  section: 市場掃描 (tab-market)",
        f"  total_candidates: {len(buy) + len(watch)}",
        f"  buy_count: {len(buy)}",
        f"  watch_count: {len(watch)}",
        "",
        "# === BUY：訊號成立，可考慮進場 ===",
        "buy:" if buy else "buy: []",
    ]
    for it in buy:
        lines.append(f'  - code: "{it["code"]}"')
        lines.append(f'    name: "{it["name"]}"')
    lines.append("")
    lines.append("# === WATCH：觀察中，等待進場訊號確認 ===")
    lines.append("watch:" if watch else "watch: []")
    for it in watch:
        lines.append(f'  - code: "{it["code"]}"')
        lines.append(f'    name: "{it["name"]}"')
    return "\n".join(lines) + "\n", stamp


def main():
    arg = sys.argv[1] if len(sys.argv) > 1 else None
    report = resolve_report(arg)
    buy, watch = extract(report)
    content, stamp = render(report, buy, watch)
    out = os.path.join(REPORTS_DIR, f"candidates_{stamp}.yaml")
    with open(out, "w", encoding="utf-8") as f:
        f.write(content)
    print(f"來源報告：{report}")
    print(f"輸出：{out}")
    print(f"BUY {len(buy)} 檔｜WATCH {len(watch)} 檔｜合計 {len(buy) + len(watch)} 檔")


if __name__ == "__main__":
    main()
