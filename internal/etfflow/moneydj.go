package etfflow

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// ──────────────────────────────────────────────────────────────────────────────
// MoneyDJ holdings-detail adapter (R8-6a).
//
// Reads ONLY the public holdings-detail ("持股明細") page. It must NOT read, and must
// NOT confuse the as-of date with, the industry-breakdown ("產業分布") section, the
// fund-flow, beneficiary-count, NAV or dividend sections (R8-6 spec §4). If share
// counts ("持有股數") are absent it returns an error rather than a partial snapshot,
// because R8 flow needs delta_shares.
//
// Parsing is heuristic over the rendered table (stdlib only — no DOM dependency): the
// exact MoneyDJ markup may shift, so selectors are intentionally tolerant and every
// rule is pinned by fixture tests. NO network here; that lives in the Pager.
// ──────────────────────────────────────────────────────────────────────────────

// MoneyDJ is the HoldingsParser for MoneyDJ's ETF holdings-detail page.
type MoneyDJ struct{}

// Name implements HoldingsParser.
func (MoneyDJ) Name() string { return "MoneyDJ" }

// URL is MoneyDJ's basic holdings page for a TW-listed ETF (e.g. 00981A → ...?etfid=00981A.TW).
func (MoneyDJ) URL(etfCode string) string {
	return "https://www.moneydj.com/etf/x/basic/basic0007.xdjhtm?etfid=" + etfCode + ".TW"
}

// Coverage marks MoneyDJ as top_n_only: the holdings-detail page exposes only the
// top 10 constituents (with daily share counts), NOT the full portfolio. This is the
// R8-6 guardrail — a top-10 view cannot distinguish "dropped out of the top 10" from
// "fully sold", so it must never feed delta_shares / sell-pressure (SPEC R8-6b §1).
func (MoneyDJ) Coverage() Coverage {
	return Coverage{
		Type: CoverageTopNOnly,
		TopN: 10,
		Note: "MoneyDJ page exposes top holdings only; not a full holdings snapshot.",
	}
}

// Parse extracts the holdings-detail as-of date and the raw positions from page HTML.
//
// MoneyDJ renders several tables (region / sector / top-holdings / full holdings). We
// pick the holdings-detail table — the first one that yields rows carrying BOTH a stock
// code AND a share count — and read its as-of date from the "資料日期：YYYY/MM/DD" label
// that immediately precedes that table. This ties the date to the actual holdings table
// instead of any 產業分布 / NAV date elsewhere on the page (spec §4). A table whose rows
// have codes but no share counts is surfaced as the missing-shares error only if NO
// table provides share counts.
func (MoneyDJ) Parse(body string) (string, []RawHolding, error) {
	var missingSharesErr error
	for _, tbl := range reTable.FindAllStringSubmatchIndex(body, -1) {
		inner := body[tbl[2]:tbl[3]] // submatch 1 = table contents
		unit := detectHoldingsUnit(inner)
		rows, err := parseHoldingsRows(inner, unit)
		if err != nil { // code present but a row lacked 持有股數
			if missingSharesErr == nil {
				missingSharesErr = err
			}
			continue
		}
		if len(rows) == 0 {
			continue // not a holdings table (no stock codes)
		}
		asOf := asOfDateBefore(body, tbl[0])
		if asOf == "" {
			return "", nil, fmt.Errorf("etfflow: moneydj: found holdings table but no " +
				"資料日期 above it")
		}
		return asOf, rows, nil
	}
	if missingSharesErr != nil {
		return "", nil, missingSharesErr
	}
	return "", nil, fmt.Errorf("etfflow: moneydj: no holdings-detail table found " +
		"(page markup may have changed or holdings are JS-rendered)")
}

// ── as-of date ──────────────────────────────────────────────────────────────────

var reDate = regexp.MustCompile(`(\d{4})[/-](\d{1,2})[/-](\d{1,2})`)

// asOfDateBefore returns the canonical date of the LAST "資料日期" date that ends at or
// before pos (i.e. the date immediately above the holdings table). "" if none.
func asOfDateBefore(body string, pos int) string {
	last := ""
	for _, loc := range reDate.FindAllStringIndex(body, -1) {
		if loc[1] > pos {
			break
		}
		if d := canonDate(body[loc[0]:loc[1]]); d != "" {
			last = d
		}
	}
	return last
}

// canonDate normalises "2026/6/5" → "2026-06-05"; returns "" if invalid.
func canonDate(s string) string {
	m := reDate.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	if mo < 1 || mo > 12 || d < 1 || d > 31 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
}

// ── holdings table ───────────────────────────────────────────────────────────────

var (
	reTable    = regexp.MustCompile(`(?is)<table[^>]*>(.*?)</table>`)
	reTR       = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	reCell     = regexp.MustCompile(`(?is)<t[dh][^>]*>(.*?)</t[dh]>`)
	reTags     = regexp.MustCompile(`(?s)<[^>]+>`)
	reCodeName = regexp.MustCompile(`^(\d{4}[A-Z]?)[\s\x{3000}]+(\S.*)$`)
	reCodeOnly = regexp.MustCompile(`^\d{4}[A-Z]?$`)
	// reCodeTW matches the linked "名稱(2330.TW)" form MoneyDJ renders.
	reCodeTW    = regexp.MustCompile(`(?i)(\d{4}[A-Z]?)\.tw[a-z]?`)
	reCodeStrip = regexp.MustCompile(`(?i)[(（]\s*\d{4}[A-Z]?\.tw[a-z]?\s*[)）]`)
	reNum       = regexp.MustCompile(`^\d[\d,]*(\.\d+)?$`)
)

// parseHoldingsRows walks one table's <tr> rows and returns the holdings it carries. A
// row with a code but no share count is a hard error (spec §4: no 持有股數 → no
// snapshot). A table with NO stock codes returns (nil, nil) so the caller can skip it.
// unit is the raw share unit detected for this table; Ingest normalises it to 股.
func parseHoldingsRows(tableInner, unit string) ([]RawHolding, error) {
	var out []RawHolding
	for _, tr := range reTR.FindAllStringSubmatch(tableInner, -1) {
		cells := rowCells(tr[1])
		if len(cells) == 0 {
			continue
		}
		h, hasCode, err := parseHTMLHoldingRow(cells, unit)
		if err != nil {
			return nil, err
		}
		if !hasCode {
			continue // header / total / non-holding row
		}
		out = append(out, *h)
	}
	return out, nil
}

// rowCells returns the trimmed, tag-stripped, entity-decoded text of each cell in a row.
func rowCells(tr string) []string {
	ms := reCell.FindAllStringSubmatch(tr, -1)
	cells := make([]string, 0, len(ms))
	for _, m := range ms {
		cells = append(cells, cellText(m[1]))
	}
	return cells
}

// cellText strips tags, decodes entities, and collapses whitespace.
func cellText(s string) string {
	s = reTags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// parseHTMLHoldingRow classifies one row's cells. hasCode is false for rows without a
// stock code (headers, totals) so the caller can skip them. When a code IS present but
// no share count can be read, it returns an error.
//
// Number columns are separated by magnitude, which is robust across MoneyDJ's
// label-free numeric cells: a holding weight (投資比例) is a percentage ≤ 100, while a
// share count (持有股數) is always > 100. So the row's largest number > 100 is the share
// count and the value ≤ 100 is the weight. % signs, commas and trailing ".00" are all
// tolerated.
func parseHTMLHoldingRow(cells []string, unit string) (*RawHolding, bool, error) {
	var (
		code, name string
		codeIdx    = -1
	)
	for i, c := range cells {
		if m := reCodeTW.FindStringSubmatch(c); m != nil {
			code, codeIdx = m[1], i
			name = strings.TrimSpace(reCodeStrip.ReplaceAllString(c, ""))
			break
		}
		if m := reCodeName.FindStringSubmatch(c); m != nil {
			code, name, codeIdx = m[1], m[2], i
			break
		}
		if reCodeOnly.MatchString(c) {
			code, codeIdx = c, i
			if i+1 < len(cells) {
				name = cells[i+1]
			}
			break
		}
	}
	if code == "" {
		return nil, false, nil
	}

	var (
		weight     float64
		shares     int64
		haveShares bool
	)
	for i, c := range cells {
		if i == codeIdx || c == "" {
			continue
		}
		v, ok := parseNum(c)
		if !ok {
			continue
		}
		if v > 100 { // share count
			if n := int64(v + 0.5); !haveShares || n > shares {
				shares, haveShares = n, true
			}
			continue
		}
		if v > 0 { // percentage weight (≤ 100)
			weight = v
		}
	}

	if !haveShares {
		return nil, true, fmt.Errorf("etfflow: moneydj: holding %s (%s) has no 持有股數 "+
			"(cannot build flow snapshot without delta_shares)", code, name)
	}

	return &RawHolding{
		StockCode: code,
		StockName: name,
		Quantity:  float64(shares),
		RawUnit:   unit,
		Weight:    weight,
	}, true, nil
}

// parseNum reads a numeric cell tolerating a leading/trailing %, thousands commas, and a
// decimal part: "9.75", "9.75%", "11,960,000.00" → 9.75 / 9.75 / 1.196e7.
func parseNum(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if !reNum.MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// detectHoldingsUnit reads the share-quantity unit from the table headers, defaulting to
// 股. A header mentioning 千股 / 張 switches the unit so Ingest scales correctly.
func detectHoldingsUnit(body string) string {
	// Only inspect header-ish cells to avoid matching 股 inside stock names.
	for _, m := range reCell.FindAllStringSubmatch(body, -1) {
		c := cellText(m[1])
		if strings.Contains(c, "千股") {
			return unitThousand
		}
		if strings.Contains(c, "張數") || strings.Contains(c, "(張)") || strings.Contains(c, "（張）") {
			return unitLot
		}
	}
	return unitShare
}
