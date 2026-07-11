#!/usr/bin/env python3
"""Merge a daily report's 市場掃描 (market-scan) BUY & WATCH stocks into stocks.yaml.

The report HTML (reports/report_YYYYMMDD.html) renders every scanned stock in the
`<div id="tab-market">` section with an action badge — STRONG BUY / BUY / WATCH /
HOLD / REDUCE / SELL. This script keeps only the actionable signals (STRONG BUY +
BUY + WATCH) and merges them into the `watchlist:` block of stocks.yaml.

stocks.yaml has no buy:/watch: levels — only positions: and watchlist:. Both BUY
and WATCH candidates go into watchlist:. Codes already in positions: or already on
the watchlist are skipped, so the merge is idempotent. Comments, positions, and the
existing watchlist order are preserved; new entries are appended to the watchlist.

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
STOCKS_PATH = os.path.join(ROOT, "stocks.yaml")
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
    """Return the actionable candidates (STRONG BUY / BUY / WATCH) as [{code,name}].

    BUY-tier signals are listed before WATCH so the more actionable names land
    first in the watchlist, but both tiers go into the same watchlist block.
    """
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


# --- stocks.yaml merge -------------------------------------------------------

TOPLEVEL_RE = re.compile(r"^(?P<key>\w[\w-]*)\s*:")
CODE_RE = re.compile(r'^\s*-\s*code:\s*"?(?P<code>[0-9A-Za-z]+)"?')


def section_codes(lines: list[str]) -> dict[str, set[str]]:
    """map top-level key (positions / watchlist / …) -> set of its codes."""
    current = None
    codes: dict[str, set[str]] = {}
    for line in lines:
        if line[:1] not in (" ", "\t", "#", "") and TOPLEVEL_RE.match(line):
            current = TOPLEVEL_RE.match(line).group("key")
            codes.setdefault(current, set())
            continue
        m = CODE_RE.match(line)
        if m and current:
            codes[current].add(m.group("code"))
    return codes


def watchlist_insert_at(lines: list[str]) -> int | None:
    """Index at which to insert new watchlist entries (after the last item).

    Returns None if there is no watchlist: block. The insert point is placed
    right after the final non-blank line of the block so trailing blank lines
    and any following top-level section stay put.
    """
    start = None
    for i, line in enumerate(lines):
        if re.match(r"^watchlist\s*:", line):
            start = i
            break
    if start is None:
        return None
    end = len(lines)
    for i in range(start + 1, len(lines)):
        line = lines[i]
        if line[:1] not in (" ", "\t", "#", "") and TOPLEVEL_RE.match(line):
            end = i
            break
    j = end - 1
    while j > start and not lines[j].strip():
        j -= 1
    return j + 1


def merge_into_stocks(buy, watch):
    """Append new BUY+WATCH codes to stocks.yaml's watchlist. Returns (added, skipped)."""
    with open(STOCKS_PATH, encoding="utf-8") as f:
        lines = f.read().splitlines()

    codes = section_codes(lines)
    held = codes.get("positions", set())
    watching = codes.get("watchlist", set())
    seen = set(held) | set(watching)

    added, skipped = [], []
    new_lines: list[str] = []
    for item in buy + watch:
        code = item["code"]
        if code in seen:
            skipped.append(item)
            continue
        seen.add(code)
        added.append(item)
        new_lines.append(f'  - code: "{code}"')
        new_lines.append(f'    name: "{item["name"]}"')

    if not added:
        return added, skipped

    at = watchlist_insert_at(lines)
    if at is None:
        # No watchlist: block — create one at the end of the file.
        if lines and lines[-1].strip():
            lines.append("")
        lines.append("watchlist:")
        lines.extend(new_lines)
    else:
        lines[at:at] = new_lines

    with open(STOCKS_PATH, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    return added, skipped


def main():
    arg = sys.argv[1] if len(sys.argv) > 1 else None
    report = resolve_report(arg)
    buy, watch = extract(report)
    added, skipped = merge_into_stocks(buy, watch)

    print(f"來源報告：{report}")
    print(f"掃描訊號：BUY {len(buy)} 檔｜WATCH {len(watch)} 檔")
    print(f"寫入 stocks.yaml：新增 {len(added)} 檔｜已存在跳過 {len(skipped)} 檔")
    if added:
        preview = "、".join(f'{it["code"]} {it["name"]}' for it in added[:20])
        more = f" …(+{len(added) - 20})" if len(added) > 20 else ""
        print(f"新增：{preview}{more}")


if __name__ == "__main__":
    main()
