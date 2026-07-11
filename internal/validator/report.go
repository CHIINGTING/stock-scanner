package validator

import (
	"fmt"
	"html/template"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ReportMeta carries the run-level context shown in the report header.
type ReportMeta struct {
	From, To         time.Time
	Horizons         []int
	Benchmark        string
	BenchmarkEnabled bool
	FilterDesc       string
	GeneratedAt      time.Time
}

// WriteHTMLReport renders the full validation report to path.
func WriteHTMLReport(path string, meta ReportMeta, results []ValidationResult) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return renderHTML(f, meta, results)
}

// ─── view models ────────────────────────────────────────────────────────────

type cellRet struct {
	Text string
	Cls  string // pos | neg | neu | na
}

type tableRow struct {
	Date, Code, Name, Action, Stage   string
	SignalPrice, EntryPrice, EntryDate string
	Rets                              []cellRet
	MaxDD                             cellRet
	Result, ResultCls                 string
	Reason, Source                    string
}

type reasonRow struct {
	Tag                  string
	Count                int
	BuyHit, ReduceHit    string
	AvgT5, AvgT10        string
}

type summaryView struct {
	Period                                   string
	Total, Buy, Reduce, Watch, EntryCaution  int
	Validatable, Insufficient                int
	OverallHit                               string
	BuyT5, BuyT10, ReduceT5, ReduceT10       string
	EntryCautionHit                          string
	BenchmarkLine                            string
}

type reportModel struct {
	Period, GeneratedAt, FilterDesc string
	HorizonLabels                   []string
	Summary                         summaryView
	BuyRows, ReduceRows, WatchRows  []tableRow
	EntryCautionRows                []tableRow
	BestBuy, BestReduce             []tableRow
	WrongBuy, WrongReduce           []tableRow
	WrongEntryCaution               []tableRow
	ReasonRows                      []reasonRow
	HasReason                       bool
	HasWatch                        bool
	HasEntryCaution                 bool
}

func renderHTML(w io.Writer, meta ReportMeta, results []ValidationResult) error {
	hs := SortedHorizons(meta.Horizons)
	model := buildModel(meta, results, hs)
	funcs := template.FuncMap{
		// dict builds a map from alternating key/value args so a sub-template can
		// receive more than one value (used to pass Rows + horizon labels to vtable).
		"dict": func(kv ...interface{}) map[string]interface{} {
			m := make(map[string]interface{}, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				key, _ := kv[i].(string)
				m[key] = kv[i+1]
			}
			return m
		},
	}
	tpl, err := template.New("validation").Funcs(funcs).Parse(htmlTemplate)
	if err != nil {
		return err
	}
	return tpl.Execute(w, model)
}

func buildModel(meta ReportMeta, results []ValidationResult, hs []int) reportModel {
	m := reportModel{
		GeneratedAt: meta.GeneratedAt.Format("2006-01-02 15:04"),
		FilterDesc:  meta.FilterDesc,
	}
	for _, h := range hs {
		m.HorizonLabels = append(m.HorizonLabels, fmt.Sprintf("T+%d", h))
	}

	buyStat := newGroupStat(hs)
	reduceStat := newGroupStat(hs)
	cautionStat := newGroupStat(hs)

	for _, r := range results {
		row := toRow(r, hs)
		switch r.Signal.ActionGroup {
		case BuyGroup:
			m.BuyRows = append(m.BuyRows, row)
			buyStat.add(r)
		case ReduceGroup:
			m.ReduceRows = append(m.ReduceRows, row)
			reduceStat.add(r)
		case EntryCautionGroup:
			m.EntryCautionRows = append(m.EntryCautionRows, row)
			cautionStat.add(r)
		default:
			m.WatchRows = append(m.WatchRows, row)
		}
	}
	m.HasWatch = len(m.WatchRows) > 0
	m.HasEntryCaution = len(m.EntryCautionRows) > 0

	// Summary
	total := len(results)
	correct := buyStat.correct + reduceStat.correct
	wrong := buyStat.wrong + reduceStat.wrong
	insufficient := buyStat.pending + buyStat.noData + reduceStat.pending + reduceStat.noData +
		countInsufficientWatch(m.WatchRows)
	m.Summary = summaryView{
		Period:          meta.From.Format("2006-01-02") + " ～ " + meta.To.Format("2006-01-02"),
		Total:           total,
		Buy:             buyStat.count,
		Reduce:          reduceStat.count,
		Watch:           len(m.WatchRows),
		EntryCaution:    cautionStat.count,
		Validatable:     correct + wrong,
		Insufficient:    insufficient,
		OverallHit:      fmtRate(correct, correct+wrong),
		BuyT5:           buyStat.dirRate(pickH(hs, 5, 3), true),
		BuyT10:          buyStat.dirRate(pickH(hs, 10, 20), true),
		ReduceT5:        reduceStat.dirRate(pickH(hs, 5, 3), false),
		ReduceT10:       reduceStat.dirRate(pickH(hs, 10, 20), false),
		EntryCautionHit: fmtRate(cautionStat.correct, cautionStat.correct+cautionStat.wrong),
	}
	if meta.BenchmarkEnabled {
		m.Summary.BenchmarkLine = "對照 benchmark：" + meta.Benchmark + "（excess return 已納入判斷）"
	} else if meta.Benchmark != "" {
		m.Summary.BenchmarkLine = "benchmark " + meta.Benchmark + " 無價格資料，改用絕對報酬判斷"
	} else {
		m.Summary.BenchmarkLine = "未指定 benchmark，使用絕對報酬判斷"
	}

	longH := pickH(hs, 10, 20)
	m.BestBuy = topRows(results, BuyGroup, longH, true, 5)
	m.BestReduce = topRows(results, ReduceGroup, longH, false, 5)
	m.WrongBuy = wrongRows(results, BuyGroup, hs)
	m.WrongReduce = wrongRows(results, ReduceGroup, hs)
	m.WrongEntryCaution = wrongRows(results, EntryCautionGroup, hs)

	m.ReasonRows = buildReasonRows(results, hs)
	m.HasReason = len(m.ReasonRows) > 0
	return m
}

// ─── group statistics ───────────────────────────────────────────────────────

type groupStat struct {
	count                        int
	correct, wrong, neutral      int
	pending, noData              int
	dirHits, dirN                map[int]int // directional hit counts per horizon
}

func newGroupStat(hs []int) *groupStat {
	g := &groupStat{dirHits: map[int]int{}, dirN: map[int]int{}}
	for _, h := range hs {
		g.dirHits[h] = 0
		g.dirN[h] = 0
	}
	return g
}

func (g *groupStat) add(r ValidationResult) {
	g.count++
	switch r.Result {
	case ResultCorrect:
		g.correct++
	case ResultWrong:
		g.wrong++
	case ResultNeutral:
		g.neutral++
	case ResultPending:
		g.pending++
	case ResultNoPriceData:
		g.noData++
	}
	buy := r.Signal.ActionGroup == BuyGroup
	for h, ret := range r.ReturnByHorizon {
		if _, ok := g.dirN[h]; !ok {
			continue
		}
		g.dirN[h]++
		if (buy && ret > 0) || (!buy && ret < 0) {
			g.dirHits[h]++
		}
	}
}

// dirRate is the directional hit rate at horizon h (BUY: up; REDUCE: down).
func (g *groupStat) dirRate(h int, _ bool) string {
	if h == 0 {
		return "—"
	}
	return fmtRate(g.dirHits[h], g.dirN[h])
}

// ─── row / helpers ──────────────────────────────────────────────────────────

func toRow(r ValidationResult, hs []int) tableRow {
	row := tableRow{
		Date:        r.Signal.SignalDate.Format("2006-01-02"),
		Code:        r.Signal.Code,
		Name:        r.Signal.Name,
		Action:      r.Signal.Action,
		Stage:       r.Signal.Stage,
		SignalPrice: fmtPrice(r.Signal.SignalPrice),
		EntryPrice:  fmtPrice(r.EntryPrice),
		EntryDate:   fmtDate(r.EntryDate),
		MaxDD:       retCellVal(r.MaxDrawdown, true),
		Result:      string(r.Result),
		ResultCls:   resultClass(r.Result),
		Reason:      r.Reason,
		Source:      filepath.Base(r.Signal.SourceReport),
	}
	for _, h := range hs {
		if v, ok := r.ReturnByHorizon[h]; ok {
			row.Rets = append(row.Rets, retCellVal(v, false))
		} else {
			row.Rets = append(row.Rets, cellRet{Text: "—", Cls: "na"})
		}
	}
	return row
}

func retCellVal(v float64, drawdown bool) cellRet {
	if math.IsNaN(v) {
		return cellRet{Text: "—", Cls: "na"}
	}
	cls := "neu"
	if v > 0 {
		cls = "pos"
	} else if v < 0 {
		cls = "neg"
	}
	if drawdown {
		cls = "neg"
		if v == 0 {
			cls = "neu"
		}
	}
	return cellRet{Text: fmt.Sprintf("%+.1f%%", v), Cls: cls}
}

func resultClass(r Result) string {
	switch r {
	case ResultCorrect:
		return "r-correct"
	case ResultWrong:
		return "r-wrong"
	case ResultNeutral:
		return "r-neutral"
	case ResultPending:
		return "r-pending"
	default:
		return "r-nodata"
	}
}

// topRows returns the best CORRECT cases for a group: BUY sorted by highest gain
// at horizon h, REDUCE by largest drop.
func topRows(results []ValidationResult, group ActionGroup, h int, gain bool, limit int) []tableRow {
	var picked []ValidationResult
	for _, r := range results {
		if r.Signal.ActionGroup != group || r.Result != ResultCorrect {
			continue
		}
		if _, ok := r.ReturnByHorizon[h]; !ok {
			continue
		}
		picked = append(picked, r)
	}
	sort.Slice(picked, func(i, j int) bool {
		if gain {
			return picked[i].ReturnByHorizon[h] > picked[j].ReturnByHorizon[h]
		}
		return picked[i].ReturnByHorizon[h] < picked[j].ReturnByHorizon[h]
	})
	if len(picked) > limit {
		picked = picked[:limit]
	}
	rows := make([]tableRow, 0, len(picked))
	for _, r := range picked {
		rows = append(rows, toRow(r, sortedHorizonKeys(r)))
	}
	return rows
}

func wrongRows(results []ValidationResult, group ActionGroup, hs []int) []tableRow {
	var rows []tableRow
	for _, r := range results {
		if r.Signal.ActionGroup == group && r.Result == ResultWrong {
			rows = append(rows, toRow(r, hs))
		}
	}
	return rows
}

// sortedHorizonKeys returns the horizons present for a result, ascending, so the
// best-case mini tables stay column-aligned with the data they have.
func sortedHorizonKeys(r ValidationResult) []int {
	keys := make([]int, 0, len(r.ReturnByHorizon))
	for h := range r.ReturnByHorizon {
		keys = append(keys, h)
	}
	sort.Ints(keys)
	return keys
}

func buildReasonRows(results []ValidationResult, hs []int) []reasonRow {
	type acc struct {
		count                            int
		buyHit, buyN, reduceHit, reduceN int
		sumT5                            float64
		nT5                              int
		sumT10                           float64
		nT10                             int
	}
	t5 := pickH(hs, 5, 3)
	t10 := pickH(hs, 10, 20)
	m := map[string]*acc{}
	for _, r := range results {
		if !r.Signal.ActionGroup.CountsTowardHitRate() {
			continue
		}
		buy := r.Signal.ActionGroup == BuyGroup
		for _, tag := range r.Signal.ReasonTags {
			a := m[tag]
			if a == nil {
				a = &acc{}
				m[tag] = a
			}
			a.count++
			if r.Result == ResultCorrect || r.Result == ResultWrong {
				if buy {
					a.buyN++
					if r.Result == ResultCorrect {
						a.buyHit++
					}
				} else {
					a.reduceN++
					if r.Result == ResultCorrect {
						a.reduceHit++
					}
				}
			}
			if v, ok := r.ReturnByHorizon[t5]; ok {
				a.sumT5 += v
				a.nT5++
			}
			if v, ok := r.ReturnByHorizon[t10]; ok {
				a.sumT10 += v
				a.nT10++
			}
		}
	}
	rows := make([]reasonRow, 0, len(m))
	for tag, a := range m {
		rows = append(rows, reasonRow{
			Tag:       tag,
			Count:     a.count,
			BuyHit:    fmtRate(a.buyHit, a.buyN),
			ReduceHit: fmtRate(a.reduceHit, a.reduceN),
			AvgT5:     fmtAvg(a.sumT5, a.nT5),
			AvgT10:    fmtAvg(a.sumT10, a.nT10),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })
	if len(rows) > 25 {
		rows = rows[:25]
	}
	return rows
}

func countInsufficientWatch(rows []tableRow) int {
	n := 0
	for _, r := range rows {
		if r.Result == string(ResultPending) || r.Result == string(ResultNoPriceData) {
			n++
		}
	}
	return n
}

// pickH returns the first of the preferred horizons that exists in hs, else 0.
func pickH(hs []int, prefs ...int) int {
	have := map[int]bool{}
	for _, h := range hs {
		have[h] = true
	}
	for _, p := range prefs {
		if have[p] {
			return p
		}
	}
	return 0
}

func fmtRate(hits, n int) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%% (%d/%d)", float64(hits)/float64(n)*100, hits, n)
}

func fmtAvg(sum float64, n int) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprintf("%+.1f%%", sum/float64(n))
}

func fmtPrice(v float64) string {
	if v <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f", v)
}
