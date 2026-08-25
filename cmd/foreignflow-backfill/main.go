// Command foreignflow-backfill acquires and audits the market-level foreign cash flow
// archive (TWSE BFI82U).
//
// Two modes:
//
//	-audit    compare the archive against the trading calendar and classify every gap
//	(default) fetch whatever the archive is missing, recording a diagnostic per attempt
//
// The trading calendar comes from the scanner's own price cache: those dates ARE the days
// TWSE traded, they are already local, and deriving the calendar from the same source the
// research uses means the audit cannot disagree with the study about which days exist.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/provider"
	"github.com/deep-huang/stock-scanner/internal/r6backtest"
)

const diagFile = "foreign_flow_diagnostics.json"

func main() {
	log.SetFlags(log.Ltime)
	audit := flag.Bool("audit", false, "classify coverage gaps without fetching")
	from := flag.String("from", "", "first date (YYYY-MM-DD); default = start of the calendar")
	to := flag.String("to", "", "last date (YYYY-MM-DD); default = end of the calendar")
	dir := flag.String("out", "data/market", "archive directory")
	cacheDir := flag.String("cache", ".cache", "price cache, used as the trading calendar")
	delay := flag.Duration("delay", 900*time.Millisecond, "pause between dates")
	attempts := flag.Int("attempts", 4, "attempts per date for TRANSIENT outcomes only")
	backoff := flag.Duration("backoff", 2*time.Second, "base backoff between attempts")
	limit := flag.Int("limit", 0, "stop after this many dates (0 = no limit)")
	saveEvery := flag.Int("save-every", 20, "checkpoint the archive every N successful fetches")
	flag.Parse()

	calendar := tradingCalendar(*cacheDir, *from, *to)
	if len(calendar) == 0 {
		log.Fatalf("no trading dates in %s for the requested range", *cacheDir)
	}
	fmt.Printf("trading calendar: %d dates, %s → %s\n",
		len(calendar), calendar[0], calendar[len(calendar)-1])

	series, _ := provider.LoadForeignFlow(*dir)
	diags := loadDiagnostics(*dir)

	if *audit {
		report(provider.AuditCoverage(calendar, series, diags), diags)
		return
	}

	if series == nil {
		series = &provider.ForeignFlowSeries{Source: "TWSE BFI82U", Unit: "NTD"}
	}
	have := map[string]bool{}
	for _, p := range series.Points {
		have[p.Date] = true
	}

	httpc := &http.Client{Timeout: 25 * time.Second}
	// Checkpointing, because this walk is long and the endpoint throttles: a run that only
	// persisted at the end would lose an hour of successful fetches to one interruption, and
	// this one already did once.
	checkpoint := func() {
		if len(series.Points) == 0 {
			return
		}
		if _, err := provider.SaveForeignFlow(*dir, series); err != nil {
			log.Printf("checkpoint: %v", err)
		}
		saveDiagnostics(*dir, diags)
	}

	var fetched, noSession, failed, seen int
	for _, date := range calendar {
		if have[date] {
			continue
		}
		if *limit > 0 && seen >= *limit {
			fmt.Printf("stopping at the -limit of %d dates\n", *limit)
			break
		}
		seen++
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		pt, dg := provider.FetchForeignFlowDiagnosed(ctx, httpc, date, *attempts, *backoff)
		cancel()
		diags[date] = dg

		switch {
		case pt != nil:
			series.Points = append(series.Points, *pt)
			have[date] = true
			fetched++
			if fetched%*saveEvery == 0 {
				checkpoint()
				fmt.Printf("  +%d (latest %s, checkpointed)\n", fetched, date)
			}
		case dg.Outcome == provider.OutcomeNoSession:
			noSession++
		default:
			failed++
			log.Printf("%s: %s (http=%d body=%dB attempts=%d) %s",
				date, dg.Outcome, dg.HTTPStatus, dg.BodyBytes, dg.Attempts, dg.Err)
		}
		time.Sleep(*delay)
	}

	if len(series.Points) > 0 {
		path, err := provider.SaveForeignFlow(*dir, series)
		if err != nil {
			log.Fatalf("%v", err)
		}
		fmt.Printf("archive: %d dates (+%d new, %d no-session, %d unresolved)\nwrote %s\n",
			len(series.Points), fetched, noSession, failed, path)
	}
	saveDiagnostics(*dir, diags)
	report(provider.AuditCoverage(calendar, series, diags), diags)
}

// tradingCalendar reads the universe date axis from the price cache.
func tradingCalendar(cacheDir, from, to string) []string {
	u, err := r6backtest.LoadUniverse(cacheDir, 130, nil, nil)
	if err != nil {
		log.Fatalf("load calendar from %s: %v", cacheDir, err)
	}
	var out []string
	for _, d := range u.Axis {
		if from != "" && d < from {
			continue
		}
		if to != "" && d > to {
			continue
		}
		out = append(out, d)
	}
	return out
}

func report(a provider.CoverageAudit, diags map[string]provider.FetchDiagnostic) {
	fmt.Printf("\n=== coverage audit ===\n")
	fmt.Printf("expected trading days: %d\n", a.Expected)
	fmt.Printf("available:             %d  (%.1f%%)\n", a.Available,
		float64(a.Available)/float64(a.Expected)*100)
	fmt.Printf("missing:               %d\n\n", len(a.Missing))

	if len(a.ByOutcome) > 0 {
		fmt.Println("missing classification:")
		keys := make([]string, 0, len(a.ByOutcome))
		for k := range a.ByOutcome {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-18s %4d\n", k, a.ByOutcome[provider.FetchOutcome(k)])
		}
		fmt.Println()
	}

	months := make([]string, 0, len(a.ByMonth))
	for m := range a.ByMonth {
		months = append(months, m)
	}
	sort.Strings(months)
	fmt.Println("by month (only months with gaps):")
	clean := 0
	for _, m := range months {
		e := a.ByMonth[m]
		if e.Miss == 0 {
			clean++
			continue
		}
		fmt.Printf("  %s  have %2d  miss %2d  (%.0f%%)\n", m, e.Have, e.Miss,
			float64(e.Have)/float64(e.Have+e.Miss)*100)
	}
	if clean > 0 {
		fmt.Printf("  (%d months fully covered)\n", clean)
	}

	// The 307 diagnosis matters enough to surface on its own: an earlier round of this work
	// concluded it was date-specific and permanent, and built a research verdict on that.
	// It was transient. Printing the evidence keeps the next reader from repeating it.
	var redirects, withLocation int
	for _, d := range diags {
		if d.Outcome == provider.OutcomeHTTPRedirect {
			redirects++
			if d.HasLocation {
				withLocation++
			}
		}
	}
	if redirects > 0 {
		fmt.Printf("\nunresolved redirects: %d (%d carried a Location header)\n", redirects, withLocation)
	}
}

func loadDiagnostics(dir string) map[string]provider.FetchDiagnostic {
	out := map[string]provider.FetchDiagnostic{}
	b, err := os.ReadFile(filepath.Join(dir, diagFile))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func saveDiagnostics(dir string, diags map[string]provider.FetchDiagnostic) {
	b, err := json.MarshalIndent(diags, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, diagFile), b, 0o644)
}
