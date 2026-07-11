package validator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Report file naming: report_YYYYMMDD.html is the canonical end-of-day report.
// Intraday drafts (report_YYYYMMDD_1.html) are ignored so a date is validated once.
var reportFileRe = regexp.MustCompile(`^report_(\d{8})\.html$`)

var (
	rowRe    = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	symRe    = regexp.MustCompile(`(?is)class="sym"[^>]*>\s*([0-9]{4}[0-9A-Z]?)\b`)
	nameRe   = regexp.MustCompile(`(?is)class="name-col"[^>]*>\s*([^<]+?)\s*<`)
	actionRe = regexp.MustCompile(`(?is)class="(?:action-badge|act-badge)[^"]*"[^>]*>\s*([^<]+?)\s*</span>`)
	scoreRe  = regexp.MustCompile(`(?is)data-score="([0-9.]+)"`)
	stageRe  = regexp.MustCompile(`(?is)class="stage-badge[^"]*"[^>]*>\s*([^<]+?)\s*</span>`)
	riskRe   = regexp.MustCompile(`(?is)class="risk-tag"[^>]*>\s*([^<]+?)\s*</span>`)
	// asciiTagRe pulls the English half out of a bilingual stage label like
	// "主升 MAIN_RUN" so it becomes a stable reason tag "MAIN_RUN".
	asciiTagRe = regexp.MustCompile(`[A-Z][A-Z_]{2,}`)
)

// tabMarker locates the byte offset where each report tab section begins so a
// parsed row can be attributed to the correct tab.
type tabMarker struct {
	offset int
	tab    string
}

// ParseReportsDir parses every canonical report in dir whose date is within
// [from, to] (inclusive). Reports are returned sorted by (date, code).
func ParseReportsDir(dir string, from, to time.Time) ([]SignalSnapshot, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read reports dir: %w", err)
	}

	var snaps []SignalSnapshot
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := reportFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		date, err := time.Parse("20060102", m[1])
		if err != nil {
			continue
		}
		if date.Before(from) || date.After(to) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		s, err := ParseReport(path, date)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		snaps = append(snaps, s...)
	}

	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].SignalDate.Equal(snaps[j].SignalDate) {
			return snaps[i].SignalDate.Before(snaps[j].SignalDate)
		}
		return snaps[i].Code < snaps[j].Code
	})
	return snaps, nil
}

// ParseReport extracts signal snapshots from a single report HTML file.
//
// It keys off the stable structural classes the scanner's own template emits
// (.sym, .name-col, .action-badge / .act-badge) rather than scraping prose, and
// only keeps rows whose .sym cell is a valid TW stock code. Each distinct
// (code, action) within a report is kept once.
func ParseReport(path string, signalDate time.Time) ([]SignalSnapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	html := string(raw)
	source := path

	markers := locateTabs(html)

	seen := make(map[string]bool) // code|normalizedAction, dedup within a report
	var out []SignalSnapshot

	for _, loc := range rowRe.FindAllStringSubmatchIndex(html, -1) {
		rowStart := loc[0]
		openTag := html[loc[0]:loc[2]] // <tr ...> carries data-score/data-* attrs
		inner := html[loc[2]:loc[3]]

		sm := symRe.FindStringSubmatch(inner)
		if sm == nil {
			continue // not a stock row
		}
		am := actionRe.FindStringSubmatch(inner)
		if am == nil {
			continue // no trading judgment in this row
		}
		code := sm[1]
		action := strings.TrimSpace(am[1])
		if action == "" {
			continue
		}

		key := code + "|" + normalizeAction(action)
		if seen[key] {
			continue
		}
		seen[key] = true

		snap := SignalSnapshot{
			SignalDate:   signalDate,
			Code:         code,
			Action:       action,
			SourceReport: source,
			SourceTab:    tabAt(markers, rowStart),
		}
		if nm := nameRe.FindStringSubmatch(inner); nm != nil {
			snap.Name = strings.TrimSpace(nm[1])
		}
		if scm := scoreRe.FindStringSubmatch(openTag); scm != nil {
			snap.Score, _ = strconv.ParseFloat(scm[1], 64)
		} else if scm := scoreRe.FindStringSubmatch(inner); scm != nil {
			snap.Score, _ = strconv.ParseFloat(scm[1], 64)
		}
		if stm := stageRe.FindStringSubmatch(inner); stm != nil {
			snap.Stage = strings.TrimSpace(stm[1])
		}
		snap.ReasonTags = extractReasonTags(inner, snap.Stage)
		// Group last: needs Stage + ReasonTags + SourceTab to split OVERHEATED
		// entry cautions out of ReduceGroup.
		snap.ActionGroup = GroupForSnapshot(snap)

		out = append(out, snap)
	}
	return out, nil
}

// locateTabs records the byte offset of each `id="tab-..."` section, in order.
func locateTabs(html string) []tabMarker {
	tabIDRe := regexp.MustCompile(`id="tab-([a-z0-9-]+)"`)
	var markers []tabMarker
	for _, m := range tabIDRe.FindAllStringSubmatchIndex(html, -1) {
		markers = append(markers, tabMarker{offset: m[0], tab: html[m[2]:m[3]]})
	}
	sort.Slice(markers, func(i, j int) bool { return markers[i].offset < markers[j].offset })
	return markers
}

// tabAt returns the tab that owns the byte offset (the last marker at or before it).
func tabAt(markers []tabMarker, offset int) string {
	tab := ""
	for _, m := range markers {
		if m.offset <= offset {
			tab = m.tab
		} else {
			break
		}
	}
	return tab
}

// extractReasonTags derives lightweight, stable tags from the stage label and the
// row's risk-tag text. Reason-tag accuracy is a best-effort signal, not the core
// of the report, so this stays deliberately simple.
func extractReasonTags(inner, stage string) []string {
	var tags []string
	seen := make(map[string]bool)
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		tags = append(tags, t)
	}
	for _, t := range asciiTagRe.FindAllString(stage, -1) {
		add(t)
	}
	if rm := riskRe.FindStringSubmatch(inner); rm != nil {
		for _, part := range strings.FieldsFunc(rm[1], func(r rune) bool {
			return r == '/' || r == '／' || r == ';' || r == '；' || r == ',' || r == '，'
		}) {
			add(part)
		}
	}
	return tags
}
