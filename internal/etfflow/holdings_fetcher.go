package etfflow

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Auto holdings fetcher (R8-6a).
//
// This is the source-agnostic half of the auto fetcher: it drives an HTML
// HoldingsParser (e.g. MoneyDJ), turns the parsed result into an R8-1 Snapshot via
// the existing Ingest path, applies the require-date / staleness policy, and (only
// when the policy allows) Saves the snapshot.
//
// Hard constraints (R8-6 spec §9): public pages only — NO login, NO captcha bypass,
// NO private API, NO browser automation (Playwright/Selenium). HTTP fetching is
// injected as a Pager so tests NEVER touch the network; all parsing/policy tests run
// against fixtures. Nothing here is a trading signal or an order.
// ──────────────────────────────────────────────────────────────────────────────

// FetchStatus is the outcome the CLI reports and exits on.
type FetchStatus string

const (
	// StatusOK: holdings detail fetched and (if a require-date was given) it matched
	// or was newer. Snapshot written.
	StatusOK FetchStatus = "OK"
	// StatusStale: holdings detail is OLDER than the required date. The source simply
	// has not disclosed the requested day yet. Snapshot written only with allow-stale.
	StatusStale FetchStatus = "STALE"
	// StatusFailed: could not fetch or parse a usable holdings-detail snapshot (incl.
	// missing share counts). No snapshot written.
	StatusFailed FetchStatus = "FAILED"
	// StatusPartial: the source's coverage is NOT full daily holdings (e.g. MoneyDJ
	// top-10). The parsed snapshot is context-only and must never feed R8 flow. No
	// snapshot is written unless --allow-partial; even then it is tagged so
	// LoadHistory skips it.
	StatusPartial FetchStatus = "PARTIAL"
)

// Coverage describes how complete a parser's source is. The fetcher stamps it onto
// the snapshot and refuses to write (by default) or feed a non-full source into R8
// flow. See docs/SPEC_R8_6B_SOURCE_DISCOVERY.md.
type Coverage struct {
	Type string // CoverageFullHoldings | CoverageTopNOnly | CoverageQuarterlyComposition | CoverageUnknown
	TopN int    // set when Type == CoverageTopNOnly
	Note string // human-readable caveat
}

// IsFull reports whether this coverage is complete enough for R8 flow. An empty Type
// is treated as full (legacy / manual full holdings).
func (c Coverage) IsFull() bool {
	return c.Type == "" || c.Type == CoverageFullHoldings
}

// HoldingsParser knows one public source's holdings-detail page: how to address it
// and how to read the AS-OF DATE OF THE HOLDINGS DETAIL (never industry-breakdown
// date) plus the raw positions. Parse must return an error rather than guess when the
// holdings-detail date or the share counts are absent — a partial snapshot is useless
// to R8 flow, which needs delta_shares.
type HoldingsParser interface {
	Name() string              // e.g. "MoneyDJ"
	URL(etfCode string) string // public page URL
	Coverage() Coverage        // how complete this source is (R8-6 guardrail)
	Parse(body string) (asOfDate string, holdings []RawHolding, err error)
}

// SelfValidatingParser is an optional capability for full-holdings parsers whose source
// declares a category total they can reconcile against (e.g. ezMoney's ST.Value vs
// sum(Amount)). When a parser implements it, the Fetcher uses ParseValidated and records
// the reconciliation on the snapshot (Snapshot.Validation). Partial sources (MoneyDJ
// top-N) do NOT implement it — they have nothing complete to reconcile against. This is
// provenance only; flow eligibility is still gated solely by Coverage.
type SelfValidatingParser interface {
	HoldingsParser
	ParseValidated(body string) (asOfDate string, holdings []RawHolding, v *HoldingsValidation, err error)
}

// Pager fetches a page's body text. Injected so tests can supply fixtures offline.
type Pager func(url string) (string, error)

// defaultPager is the only place that performs a real network request. It adds a
// User-Agent and an overall timeout, and reads a single page (no crawling, no loops).
func defaultPager(timeout time.Duration) Pager {
	// A cookie jar lets us follow a site's first-request session bootstrap (e.g.
	// ezMoney's 302-to-self that sets __nxquid) the way any browser would. It is NOT
	// authentication — there is no login/captcha — and is harmless for cookieless
	// sources like MoneyDJ.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: timeout, Jar: jar}
	return func(url string) (string, error) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return "", fmt.Errorf("etfflow: build request %s: %w", url, err)
		}
		// Identify ourselves honestly; public pages only.
		req.Header.Set("User-Agent", "stock-scanner-etfflow/1.0 (+R8 holdings fetcher; contact: local)")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("etfflow: GET %s: %w", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("etfflow: GET %s: HTTP %d", url, resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // cap at 8 MiB
		if err != nil {
			return "", fmt.Errorf("etfflow: read body %s: %w", url, err)
		}
		return string(b), nil
	}
}

// Fetcher orchestrates one fetch. Get and Now are injectable for offline, deterministic
// tests; NewFetcher wires the real defaults.
type Fetcher struct {
	Parser HoldingsParser
	Get    Pager
	Now    func() time.Time
	// DryRun fetches, parses and validates but never persists a snapshot (no Save). The
	// FetchResult still reports the status it WOULD have written, with OutputPath "".
	DryRun bool
}

// NewFetcher returns a Fetcher wired to a live Pager (real network) for the given parser
// and request timeout. Tests construct Fetcher directly with a fixture Pager instead.
func NewFetcher(parser HoldingsParser, timeout time.Duration) Fetcher {
	return Fetcher{Parser: parser, Get: defaultPager(timeout), Now: time.Now}
}

// FetchResult is the full, structured outcome of a fetch — both for the CLI's report
// and for tests to assert on.
type FetchResult struct {
	Status      FetchStatus
	Source      string    // parser name
	SourceURL   string    // actual URL fetched
	AsOfDate    string    // holdings-detail as-of date (YYYY-MM-DD), "" if unknown
	RequireDate string    // the --require-date, "" if none
	Holdings    int       // parsed position count
	DataLagDays int       // now - AsOfDate, in whole days (>=0); -1 if unknown
	Coverage    Coverage  // source coverage (R8-6); Coverage.IsFull() gates flow eligibility
	OutputPath  string    // snapshot path written, "" if none written
	Snapshot    *Snapshot // nil unless built
	Err         error     // populated on StatusFailed
}

// rawSliceSource adapts an already-parsed []RawHolding to the HoldingsSource interface
// so the fetched holdings flow through the SAME Ingest path (unit normalisation +
// validation) as the manual CSV ingest. It never normalises — Ingest does.
type rawSliceSource []RawHolding

func (s rawSliceSource) Load(_, _ string) ([]RawHolding, error) {
	if len(s) == 0 {
		return nil, fmt.Errorf("etfflow: no holdings parsed")
	}
	return []RawHolding(s), nil
}

// Fetch fetches → parses → (policy) → optionally saves, returning a FetchResult.
//
// meta supplies the ETF identity (code/name/type); its AsOfDate and SourceURL are
// overwritten with what the parser actually found / fetched. requireDate is "" for
// "latest disclosed". allowStale lets a STALE result still be written. allowPartial
// lets a non-full (context-only) source's tagged snapshot be written as a probe.
//
// Policy:
//   - parse/fetch error, or zero holdings, or no share counts → StatusFailed, no write.
//   - coverage NOT full (e.g. MoneyDJ top-10) → StatusPartial. The snapshot is tagged
//     (coverage_type/top_n/note) so LoadHistory skips it; written ONLY if allowPartial.
//     This dominates the stale check — a partial source's require-date is moot.
//   - full coverage + requireDate == "" → StatusOK (latest), write.
//   - full coverage + asOf >= requireDate → StatusOK, write.
//   - full coverage + asOf <  requireDate → StatusStale; write only if allowStale.
func (f Fetcher) Fetch(snapshotDir string, meta SnapshotMeta, requireDate string, allowStale, allowPartial bool) FetchResult {
	cov := f.Parser.Coverage()
	res := FetchResult{
		Source:      f.Parser.Name(),
		SourceURL:   f.Parser.URL(meta.ETFCode),
		RequireDate: requireDate,
		DataLagDays: -1,
		Coverage:    cov,
	}

	body, err := f.Get(res.SourceURL)
	if err != nil {
		res.Status, res.Err = StatusFailed, err
		return res
	}

	// Full-holdings parsers that can reconcile against a source-declared total produce
	// validation metadata; partial sources just Parse. Either way a parse error means
	// no snapshot is built (the parser refuses to emit degraded data).
	var (
		asOf       string
		raws       []RawHolding
		validation *HoldingsValidation
	)
	if sv, ok := f.Parser.(SelfValidatingParser); ok {
		asOf, raws, validation, err = sv.ParseValidated(body)
	} else {
		asOf, raws, err = f.Parser.Parse(body)
	}
	if err != nil {
		res.Status, res.Err = StatusFailed, err
		return res
	}
	res.AsOfDate = asOf
	res.Holdings = len(raws)

	meta.AsOfDate = asOf
	meta.SourceURL = res.SourceURL
	snap, err := Ingest(rawSliceSource(raws), meta, f.Now())
	if err != nil {
		res.Status, res.Err = StatusFailed, err
		return res
	}
	// Stamp coverage so the persisted snapshot self-identifies and LoadHistory can
	// enforce the flow guardrail without re-classifying the source.
	snap.CoverageType = cov.Type
	snap.TopN = cov.TopN
	snap.CoverageNote = cov.Note
	snap.Validation = validation
	res.Snapshot = snap
	res.DataLagDays = lagDays(asOf, f.Now())

	// Partial / quarterly / unknown coverage is context-only: it can never be OK and
	// is never written by default. allow-partial only persists a TAGGED probe, which
	// LoadHistory still skips — it does NOT make the data flow-eligible.
	if !cov.IsFull() {
		res.Status = StatusPartial
		if !allowPartial || f.DryRun {
			return res
		}
		if err := Save(snapshotDir, snap); err != nil {
			res.Status, res.Err = StatusFailed, err
			return res
		}
		res.OutputPath = snapshotFile(snap.ETFCode, snap.AsOfDate)
		return res
	}

	stale := false
	if rd := strings.TrimSpace(requireDate); rd != "" {
		cmp, err := compareDates(asOf, rd)
		if err != nil {
			res.Status, res.Err = StatusFailed, err
			return res
		}
		stale = cmp < 0 // asOf older than required
	}

	if stale {
		res.Status = StatusStale
		if !allowStale {
			return res // honour spec: do NOT silently accept stale data
		}
	} else {
		res.Status = StatusOK
	}

	if f.DryRun {
		return res // validated; deliberately not persisted
	}
	if err := Save(snapshotDir, snap); err != nil {
		res.Status, res.Err = StatusFailed, err
		return res
	}
	res.OutputPath = snapshotFile(snap.ETFCode, snap.AsOfDate)
	return res
}

// compareDates returns -1/0/+1 for a<b / a==b / a>b on YYYY-MM-DD strings.
func compareDates(a, b string) (int, error) {
	ta, err := time.Parse(dateLayout, a)
	if err != nil {
		return 0, fmt.Errorf("etfflow: bad as_of_date %q: %w", a, err)
	}
	tb, err := time.Parse(dateLayout, b)
	if err != nil {
		return 0, fmt.Errorf("etfflow: bad require-date %q: %w", b, err)
	}
	switch {
	case ta.Before(tb):
		return -1, nil
	case ta.After(tb):
		return 1, nil
	default:
		return 0, nil
	}
}

// lagDays is whole calendar days from asOf to now (>=0). Returns -1 if asOf unparseable.
func lagDays(asOf string, now time.Time) int {
	ta, err := time.Parse(dateLayout, asOf)
	if err != nil {
		return -1
	}
	d := int(now.Truncate(24*time.Hour).Sub(ta).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}
