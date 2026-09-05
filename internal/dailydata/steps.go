package dailydata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/fundamental"
	"github.com/deep-huang/stock-scanner/internal/healthcheck"
	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// The steps, each ORCHESTRATING an existing module rather than reimplementing it.
//
// Every fetch and every archive layout already exists and is already tested where it lives.
// Copying any of it here would create a second implementation that agrees until the day it
// does not — and the day it stops agreeing, the archive would hold two shapes under one name.
// So these functions decide WHEN to call something and how to report what came back, and
// nothing else.

// Deps is everything the real steps need. Each is an interface or an existing type so a test
// can supply a stub without reaching the network.
type Deps struct {
	// Symbols is the acquisition universe, as canonical symbols.
	Symbols []string
	// Prices fetches bars, built against the RESEARCH cache — never the scanner's.
	Prices PriceSource
	// Dirs
	ValuationDir   string
	FundamentalDir string
	InstitutionDir string
	// Outcomes grades stored health checks. Nil → the step is skipped.
	Outcomes *healthcheck.Outcomes
	// InstitutionTimeout bounds one exchange call.
	InstitutionTimeout time.Duration
	// InstitutionProviders builds the exchange providers. Nil → the real TWSE and TPEx ones.
	//
	// Injectable because the branch that matters most cannot otherwise be reached: the real
	// providers answer an unreachable network with a TRANSPORT error, while the case being
	// tested is ErrNoData — "the exchange answered, and has nothing for this date yet". A
	// test that used a tiny timeout to "simulate" no-data exercised the wrong branch and
	// passed while the behaviour under test was broken.
	InstitutionProviders func(timeout time.Duration) []institution.Provider
	Logf                 func(string, ...any)
}

func (d Deps) logf(f string, a ...any) {
	if d.Logf != nil {
		d.Logf(f, a...)
	}
}

// PriceStep confirms the session's bars actually exist for the universe.
//
// It acquires nothing on its own — the fetcher's cache is filled as a side effect of asking —
// but it is the step that decides whether the rest of the day is worth attempting, and it is
// the only one that can tell "the market was shut" from "our price source is down".
func PriceStep(d Deps) Step {
	return Step{Name: "price", Run: func(ctx context.Context, day Day) SourceReport {
		if d.Prices == nil {
			return SourceReport{Status: StatusFailed, Reason: "沒有設定價格來源"}
		}
		have := map[string]bool{}
		var stale []string
		for _, sym := range day.Symbols {
			code, market, err := fetcher.ParseYahooSymbol(sym)
			if err != nil {
				continue
			}
			sd, err := d.Prices.Fetch(fetcher.StockInfo{Symbol: code, Market: market})
			if err != nil || len(sd.Candles) == 0 {
				continue
			}
			// A bar FOR the session, not merely a series that reaches past it. The
			// difference is invisible for today and decisive for any backfill: for a past
			// date, "the last bar is not older than the target" is true of every series
			// that has kept running, so the check would pass for a stock that never traded
			// that day — and for a public holiday it would pass for all of them.
			if hasSession(sd.Candles, day.Date) {
				have[sym] = true
			} else {
				last := sd.Candles[len(sd.Candles)-1].Date.In(Taipei).Format(dateLayout)
				stale = append(stale, sym+"(最後 "+last+")")
			}
		}
		rep := SourceReport{
			Expected: len(day.Symbols), Available: len(have),
			Missing: MissingSymbols(day.Symbols, have),
		}
		switch {
		case len(have) == len(day.Symbols):
			rep.Status = StatusComplete
		case len(have) == 0:
			rep.Status = StatusFailed
			rep.Reason = "沒有任何一檔取得該交易日的 K 棒"
		default:
			rep.Status = StatusPartial
			rep.Reason = "部分標的沒有該交易日的 K 棒"
		}
		if len(stale) > 0 {
			rep.Note = fmt.Sprintf("%d 檔序列落後", len(stale))
		}
		return rep
	}}
}

// ValuationStep archives the day's published ratios — the LIVE observation.
//
// This must keep running even though a 12-month backfill already exists. The two are
// different facts: a backfilled row says the exchange published that figure, a live row says
// THIS REPOSITORY observed it that day. Point-in-time research needs the second, and no
// amount of the first substitutes for it — which is why the provenance fields exist and why
// this step never stops because history is present.
func ValuationStep(d Deps) Step {
	return Step{Name: "valuation", Run: func(ctx context.Context, day Day) SourceReport {
		if r, skip := refuseBackdated("valuation", day); skip {
			return r
		}
		svc := valuation.NewService(nil, d.ValuationDir, d.Logf)
		symbols := universe(day, d)
		snaps, err := svc.FetchAndStore(ctx, symbols)
		if err != nil && len(snaps) == 0 {
			return SourceReport{Status: StatusFailed, Expected: len(symbols),
				Reason: "抓取失敗：" + err.Error()}
		}
		have := map[string]bool{}
		for _, s := range snaps {
			if s.Status == valuation.Available && s.Ratios != nil {
				have[s.Symbol] = true
			}
		}
		rep := SourceReport{
			Expected: len(symbols), Available: len(have),
			Missing: MissingSymbols(symbols, have),
		}
		// Live rows carry Backfilled=false by construction — FetchAndStore never sets it.
		// The check is here rather than assumed because the whole point-in-time story
		// depends on the two being distinguishable.
		rep.Note = "live observation (backfilled=false)"
		rep.Status = tally(len(have), len(symbols))
		if err != nil {
			rep.Reason = err.Error()
		}
		return rep
	}}
}

// FundamentalStep archives MOPS figures.
//
// A no-op is the NORMAL case: the endpoints serve the current reporting period, so most days
// re-archive a period already stored. That is not a failure and is not reported as one —
// COMPLETE with a note, because "nothing changed" and "nothing arrived" must not look alike.
func FundamentalStep(d Deps) Step {
	return Step{Name: "fundamental", Run: func(ctx context.Context, day Day) SourceReport {
		if r, skip := refuseBackdated("fundamental", day); skip {
			return r
		}
		svc := fundamental.NewService(nil, d.FundamentalDir, d.Logf)
		symbols := universe(day, d)
		snaps, err := svc.FetchAndStore(ctx, symbols)
		if err != nil && len(snaps) == 0 {
			return SourceReport{Status: StatusFailed, Expected: len(symbols),
				Reason: "抓取失敗：" + err.Error()}
		}
		have := map[string]bool{}
		for _, s := range snaps {
			if s.Status != fundamental.Unavailable {
				have[s.Symbol] = true
			}
		}
		rep := SourceReport{
			Expected: len(symbols), Available: len(have),
			Missing: MissingSymbols(symbols, have),
			Status:  tally(len(have), len(symbols)),
		}
		if err != nil {
			rep.Reason = err.Error()
		}
		return rep
	}}
}

// InstitutionStep archives the day's three-way institutional flows.
//
// The exchanges publish well after the close and sometimes late, so "not ready yet" is an
// ordinary outcome and is reported as FAILED-with-a-reason rather than as an absence: the
// operator's response is to run again this evening, and only a stated reason conveys that.
//
// It never writes a zero. institution.Save refuses a write that would downgrade an existing
// snapshot, and a source that answered with nothing produces no record at all.
//
// institution.ErrNoData does NOT mean "the day had no trading data" — that reading is what
// made this step report NO_SESSION on every on-time run, turning the whole report COMPLETE
// and the exit code 0. It means the exchange answered and has nothing for this date YET,
// which on a session the pipeline already called CLOSED is a "come back this evening".
func InstitutionStep(d Deps) Step {
	return Step{Name: "institution", Run: func(ctx context.Context, day Day) SourceReport {
		build := d.InstitutionProviders
		if build == nil {
			build = func(t time.Duration) []institution.Provider {
				return []institution.Provider{
					institution.NewTWSEProvider(t), institution.NewTPEXProvider(t),
				}
			}
		}
		res := institution.Acquire(ctx, build(d.InstitutionTimeout), d.InstitutionDir, day.Date)

		rep := SourceReport{
			Expected: len(res.Results), Available: res.Saved(),
			Reason: joinWith(res.Reasons(), "；"),
		}
		switch {
		case res.Saved() == len(res.Results):
			rep.Status = StatusComplete
		case res.NoData() == len(res.Results):
			// Both exchanges reported nothing. What that MEANS depends on the session, and
			// the pipeline has already decided that:
			//
			//	NON_TRADING  a holiday — genuinely nothing to fetch (this step does not run)
			//	CLOSED       the data has not been published YET
			//
			// TWSE T86 typically appears between 16:00 and 18:00, and this job's cut-off is
			// 14:00 — so on an ordinary trading day, run at its own designed time, "no data"
			// is the EXPECTED first answer. Calling that NO_SESSION made the whole report
			// COMPLETE and the exit code 0, so a scheduler saw success and never came back.
			// It is the one failure mode this report exists to prevent, and it was on the
			// main path rather than an edge.
			rep.Status = StatusFailed
			rep.Note = "尚未公布"
			rep.Reason = "兩個交易所都還沒有 " + day.Date + " 的資料。" +
				"TWSE T86 通常 16:00–18:00 才公布，而本流程的門檻是 14:00 —— " +
				"傍晚重跑即可補上。"
		case res.Saved() == 0:
			rep.Status = StatusFailed
		default:
			rep.Status = StatusPartial
		}
		return rep
	}}
}

// OutcomeStep grades stored health checks against what happened next.
//
// It runs LAST because it measures forward returns from sessions the earlier steps just
// confirmed. Its own guards — canonical row per symbol-day, write-once horizons, no unclosed
// session — live in internal/healthcheck and are not re-implemented here.
func OutcomeStep(d Deps) Step {
	return Step{Name: "outcomes", Run: func(ctx context.Context, day Day) SourceReport {
		if d.Outcomes == nil {
			return SourceReport{Status: StatusSkipped, Reason: "未啟用健檢歷史紀錄"}
		}
		rep, err := d.Outcomes.Run(ctx, healthcheck.OutcomeConfig{AsOf: day.Date})
		if err != nil {
			return SourceReport{Status: StatusFailed, Reason: err.Error()}
		}
		filled := 0
		for _, n := range rep.Filled {
			filled += n
		}
		out := SourceReport{
			Status: StatusComplete, Expected: rep.Examined, Available: rep.Examined,
			Note: fmt.Sprintf("評分 %d 檔，填入 %d 個 horizon，%d 個已知；跳過 %d 重複／%d 無基準價／%d 太近",
				rep.Examined, filled, rep.AlreadyKnown, rep.SkippedDuplicate,
				rep.SkippedNoBase, rep.SkippedTooEarly),
		}
		if len(rep.Errors) > 0 {
			out.Status = StatusPartial
			out.Reason = joinWith(rep.Errors[:min(len(rep.Errors), 3)], "；")
		}
		return out
	}}
}

// NewsStep reports whether a news snapshot exists for the session.
//
// It does NOT fetch. News is collected by cmd/scanner, whose providers, resolve index and
// alias handling are wired into the scan; building a second collector here would be a second
// implementation of a source whose whole risk is inconsistency between runs.
//
// So this step CHECKS rather than acquires, and says so — a completeness report that silently
// omitted news would let a permanent EXACT_DATE hole open without anyone noticing, which is
// the specific thing FU-8 exists to prevent.
func NewsStep(reportDir string) Step {
	return Step{Name: "news", Run: func(ctx context.Context, day Day) SourceReport {
		d, err := time.ParseInLocation(dateLayout, day.Date, Taipei)
		if err != nil {
			return SourceReport{Status: StatusFailed, Reason: "日期無法解析"}
		}
		pattern := filepath.Join(reportDir, "news_"+d.Format("20060102")+"_*.json")
		files, err := filepath.Glob(pattern)
		if err != nil {
			return SourceReport{Status: StatusFailed, Reason: err.Error()}
		}
		if len(files) == 0 {
			return SourceReport{
				Status:   StatusFailed,
				Expected: 1,
				Reason: "該日沒有新聞快照。新聞由 cmd/scanner 產生，此步驟只檢查不抓取；" +
					"EXACT_DATE 語意下漏跑當天就是永久的洞",
			}
		}
		return SourceReport{Status: StatusComplete, Expected: 1, Available: 1,
			Note: fmt.Sprintf("%d 份快照（由 cmd/scanner 產生）", len(files))}
	}}
}

// universe is the ONE list of symbols a run is about.
//
// Day carries it because it is a property of the run; Deps carries it because the steps close
// over their dependencies. Two fields holding the same list is two chances to disagree about
// what "expected" means, so Day wins and Deps is the fallback.
func universe(day Day, d Deps) []string {
	if len(day.Symbols) > 0 {
		return day.Symbols
	}
	return d.Symbols
}

// hasSession reports whether the series contains a bar FOR that session.
func hasSession(cs []fetcher.Candle, date string) bool {
	for i := len(cs) - 1; i >= 0; i-- {
		d := cs[i].Date.In(Taipei).Format(dateLayout)
		if d == date {
			return true
		}
		if d < date {
			return false // ascending; nothing earlier can match
		}
	}
	return false
}

// refuseBackdated stops the two sources that CANNOT target a past session.
//
// valuation.FetchAndStore and fundamental.FetchAndStore take no date. Their endpoints serve
// only the current period, and the archive directory is named from time.Now(). Running them
// for an older session therefore fetches TODAY's figures and files them under TODAY, while
// the report claims the older date — the exact misattribution a backfill run is most likely
// to produce, and the report would state it confidently.
//
// So they are skipped with the reason said out loud. Repairing an older session's ratios is
// what cmd/valuation-backfill is for; it uses the exchanges' per-date endpoints and stamps
// every row Backfilled.
func refuseBackdated(name string, day Day) (SourceReport, bool) {
	today := day.Now.In(Taipei).Format(dateLayout)
	if day.Date == today {
		return SourceReport{}, false
	}
	return SourceReport{
		Status: StatusSkipped,
		Note:   "只能取得當日",
		Reason: name + " 的來源端點沒有日期參數，只提供最新一期，封存目錄也以抓取日命名。" +
			"為 " + day.Date + " 執行會抓到 " + today + " 的數字並歸檔到 " + today +
			"，卻回報成 " + day.Date + "。補舊資料請用 cmd/valuation-backfill。",
	}, true
}

// tally turns a count into a status.
func tally(have, want int) Status {
	switch {
	case want == 0:
		return StatusSkipped
	case have == want:
		return StatusComplete
	case have == 0:
		return StatusFailed
	default:
		return StatusPartial
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// EnsureDirs creates the archive directories a run will write to.
func EnsureDirs(dirs ...string) error {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("dailydata: mkdir %s: %w", d, err)
		}
	}
	return nil
}
