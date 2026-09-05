// Package stockcatalog is the canonical answer to "which stock is this?".
//
// Every R14 Health Dashboard request starts by resolving what the human typed — a code, a
// Chinese name, a nickname — into ONE identity. That resolution has to happen in exactly one
// place, because the failure it prevents is silent and expensive:
//
//	symbol = 2330
//	name   = 鴻海
//
// A dashboard that lets those two travel separately will eventually show one company's price
// beside another company's earnings, and nothing in the page will look wrong. So identity is
// a value produced HERE, from the catalog, and every layer downstream carries it whole.
//
// # Why this is not stocks.yaml
//
// The spec asks whether the existing stocks YAML can be extended. It cannot, for two
// independent reasons:
//
//	.gitignore carries `/stocks.yaml`   it is a personal file holding real positions, and is
//	                                    deliberately not in version control
//	make merge-watchlist REBUILDS it    the watchlist section is a machine snapshot, replaced
//	                                    wholesale on every scan; aliases or tags typed there
//	                                    would be deleted by the next run
//
// That second reason is the same one that created candidate.yaml as a separate file, and the
// same failure the watchlist / watchlist_pinned split exists to stop.
//
// # Where the catalog actually comes from
//
// It is COMPOSED from what the repo already has, so there is no second universe list to
// disagree with the first:
//
//	data/stock_names_zh.yaml   1,980 code → official Chinese name, generated from the TWSE +
//	                           TPEx ISIN lists and version-controlled. The identity base.
//	configs/sectors.yaml       industry membership, and a market hint where one was typed.
//	configs/news_aliases.yaml  the alias dictionary the news resolver ALREADY maintains.
//	                           Reused rather than copied — a second alias file would drift.
//	configs/stock_catalog.yaml a thin OVERLAY: only the fields no existing source carries
//	                           (market, aliases, tags, enabled). It does not restate the
//	                           universe, and it is the only file a human edits for R14.
//
// # Three deliberate non-guarantees
//
//	a name never resolves identity   the base file is GENERATED from the exchanges' ISIN
//	                       lists, so its name uniqueness is a property of today's download,
//	                       not a guarantee this package may rely on — and a regenerated file
//	                       must never be able to break the dashboard by gaining a collision.
//	                       Codes are the identity. A name produces search RESULTS and the
//	                       human picks. (This is not hypothetical caution: an alias CAN
//	                       shadow another company's official name, and one in the first draft
//	                       of the overlay did — 聯發 is 1459's name, and attaching it to 2454
//	                       made 1459 rank below 2454 in a search for its own name. build()
//	                       now refuses that.)
//	aliases ARE unique     an alias resolving to two stocks is a config ERROR, because an
//	                       alias IS used to resolve identity directly. It may also not
//	                       collide with another stock's code or official name.
//	market may be UNKNOWN  no offline source records which market each of the 1,980 codes is
//	                       on. Guessing would put a 上櫃 stock's archive lookup under .TW and
//	                       return "no data" for a company that has plenty. So it is reported
//	                       as unknown and resolved later, by the fetcher's own detection.
package stockcatalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// MarketSource records HOW a stock's market was determined, so a caller can tell a recorded
// fact from an absent one. There is no "GUESSED" value, and there must never be.
type MarketSource string

const (
	// MarketFromCatalog means a config file states the market explicitly.
	MarketFromCatalog MarketSource = "CATALOG"
	// MarketUnknown means no offline source records it. The health check resolves it through
	// the fetcher's existing .TW/.TWO detection and says so in its response.
	MarketUnknown MarketSource = "UNKNOWN"
)

// Market display labels. The repo's internal vocabulary is fetcher.MarketTWSE ("TW") and
// fetcher.MarketTPEX ("TWO"); these are what a human reads.
const (
	LabelTWSE    = "TWSE"
	LabelTPEX    = "TPEX"
	LabelUnknown = "UNKNOWN"
)

// Stock is one catalog entry — the whole identity, and nothing that could go stale.
//
// There is no price, no score and no rating here. Those belong to an analysis of a moment;
// a catalog entry is what the stock IS, and mixing the two would mean a config file quietly
// carrying a frozen opinion.
type Stock struct {
	// Code is the identity. Unique across the catalog, e.g. "2330".
	Code string
	// Name is the official Chinese name. NOT unique — see the package doc.
	Name string
	// Market is fetcher.MarketTWSE, fetcher.MarketTPEX, or "" when no source records it.
	Market string
	// MarketSource says whether Market was stated or is simply unknown.
	MarketSource MarketSource
	// Industry is the primary sector, from configs/sectors.yaml or the overlay.
	Industry string
	// Sectors is every sector this stock belongs to, in sectors.yaml order.
	Sectors []string
	// Enabled gates a stock out of search without deleting it. Default true.
	Enabled bool
	// Aliases are alternative ways to name this stock. Matching is case-insensitive for
	// ASCII; the strings here are kept exactly as a human typed them, for display.
	Aliases []string
	// Tags are free-form labels from the overlay. Never read by any calculation.
	Tags []string
	// NameSource names the file the name came from, so a surprising name is traceable.
	NameSource string
}

// Symbol is the public identifier — the code, which is what a human types and what the API
// puts in a URL. The market is a separate attribute precisely because it can be unknown.
func (s Stock) Symbol() string { return s.Code }

// CanonicalSymbol is the repo-wide "2330.TW" form used by every archive path and the price
// cache. ok is false when the market is unknown, in which case there is no canonical symbol
// yet — the caller must resolve the market first rather than defaulting to TWSE.
func (s Stock) CanonicalSymbol() (string, bool) {
	if s.Market == "" {
		return "", false
	}
	return fetcher.YahooSymbol(s.Code, s.Market), true
}

// MarketLabel is the human-readable market.
func (s Stock) MarketLabel() string { return MarketLabel(s.Market) }

// MarketLabel converts the repo's internal market code to a display label.
func MarketLabel(market string) string {
	switch market {
	case fetcher.MarketTWSE:
		return LabelTWSE
	case fetcher.MarketTPEX:
		return LabelTPEX
	default:
		return LabelUnknown
	}
}

// Catalog is the loaded, validated, immutable stock universe.
//
// Immutable on purpose: it is shared by every concurrent HTTP request, and a catalog that
// could be mutated by one handler would need a lock on every read. Reloading means building
// a new Catalog and swapping the pointer.
type Catalog struct {
	stocks []Stock // sorted by Code — the deterministic order every listing inherits

	byCode  map[string]int // code → index
	byAlias map[string]int // normalised alias → index (unique by construction)
}

// Len is how many stocks the catalog holds, enabled or not.
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.stocks)
}

// All returns every stock in code order, DEEP-copied.
//
// The copy has to be deep. A shallow one shares the Aliases / Tags / Sectors backing arrays
// with the catalog, so a caller writing into a returned Stock would mutate what every
// concurrent HTTP handler is reading — a genuine data race, not a theoretical one.
func (c *Catalog) All() []Stock {
	if c == nil {
		return nil
	}
	out := make([]Stock, 0, len(c.stocks))
	for i := range c.stocks {
		out = append(out, c.stocks[i].clone())
	}
	return out
}

// clone deep-copies the slice fields. Everything else on Stock is a string or a bool, which
// copy by value already.
func (s Stock) clone() Stock {
	s.Aliases = cloneStrings(s.Aliases)
	s.Tags = cloneStrings(s.Tags)
	s.Sectors = cloneStrings(s.Sectors)
	return s
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// Lookup finds a stock by its exact code, disabled ones included.
//
// Disabled stocks are still findable here because "this stock is hidden from search" and
// "this stock does not exist" are different answers, and an API asked directly for a code
// should give the accurate one.
func (c *Catalog) Lookup(code string) (Stock, bool) {
	if c == nil {
		return Stock{}, false
	}
	i, ok := c.byCode[normaliseCode(code)]
	if !ok {
		return Stock{}, false
	}
	return c.stocks[i].clone(), true
}

// Resolve turns a user-supplied string into ONE identity, or reports that it cannot.
//
// It accepts only forms that identify a stock UNAMBIGUOUSLY: an exact code ("2330"), a
// canonical symbol ("2330.TW"), or an exact alias. A name is deliberately not accepted —
// names are not unique, and a Resolve that quietly picked the first 一詮 would be exactly the
// symbol/name mismatch this package exists to prevent. Callers wanting name matching use
// Search and let the human choose.
func (c *Catalog) Resolve(q string) (Stock, bool) {
	if c == nil {
		return Stock{}, false
	}
	s := strings.TrimSpace(q)
	if s == "" {
		return Stock{}, false
	}
	// "2330.TW" — accept the canonical form, but only when it agrees with the catalog. A
	// symbol naming the wrong market is a mistake worth surfacing, not one to normalise away.
	if code, market, err := fetcher.ParseYahooSymbol(s); err == nil {
		st, ok := c.Lookup(code)
		if !ok {
			return Stock{}, false
		}
		if st.Market != "" && st.Market != market {
			return Stock{}, false
		}
		return st, true
	}
	if st, ok := c.Lookup(s); ok {
		return st, true
	}
	if i, ok := c.byAlias[normaliseQuery(s)]; ok {
		return c.stocks[i].clone(), true
	}
	return Stock{}, false
}

// normaliseCode canonicalises a stock code for lookup: trimmed, upper-cased so the trailing
// letter of a code like "00981A" matches however it was typed.
func normaliseCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// normaliseQuery is the matching form for names and aliases: trimmed and lower-cased.
//
// ToLower is a no-op for CJK, so a Chinese name compares byte-for-byte while "tsmc",
// "TSMC" and "Tsmc" all collapse to one key — which is the "case-insensitive ASCII alias"
// requirement, obtained without a second code path for the two scripts.
func normaliseQuery(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// build validates a set of draft entries and freezes them into a Catalog.
//
// Validation order is deliberate — codes first, then aliases — so a file with both problems
// reports the identity collision, which is the one that makes every later answer wrong.
func build(drafts map[string]*Stock, origin map[string]string) (*Catalog, error) {
	if len(drafts) == 0 {
		return nil, fmt.Errorf("stockcatalog: catalog is empty — no stock source produced any entry")
	}

	codes := make([]string, 0, len(drafts))
	for code := range drafts {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	c := &Catalog{
		stocks:  make([]Stock, 0, len(codes)),
		byCode:  make(map[string]int, len(codes)),
		byAlias: make(map[string]int, len(codes)),
	}

	for _, code := range codes {
		s := *drafts[code]
		if s.Name == "" {
			return nil, fmt.Errorf("stockcatalog: %s has no name (source: %s)", code, origin[code])
		}
		s.Aliases = dedupeStrings(s.Aliases)
		s.Tags = dedupeStrings(s.Tags)
		s.Sectors = dedupeStrings(s.Sectors)
		if s.Market == "" {
			s.MarketSource = MarketUnknown
		} else {
			s.MarketSource = MarketFromCatalog
		}
		c.stocks = append(c.stocks, s)
		c.byCode[s.Code] = len(c.stocks) - 1
	}

	// byName is built first so an alias can be checked against every stock's official name,
	// including stocks that sort after the one carrying the alias. Names are NOT required to
	// be unique — the base file is generated, and a collision there must degrade rather than
	// refuse to start — so a repeated name simply keeps its first (lowest-code) owner here.
	byName := make(map[string]int, len(c.stocks))
	for i := range c.stocks {
		if k := normaliseQuery(c.stocks[i].Name); k != "" {
			if _, seen := byName[k]; !seen {
				byName[k] = i
			}
		}
	}

	// Aliases must resolve uniquely: Resolve uses them to pick an identity outright, so an
	// alias claimed by two stocks would silently hand back whichever loaded first.
	for i := range c.stocks {
		for _, a := range c.stocks[i].Aliases {
			key := normaliseQuery(a)
			if key == "" {
				continue
			}
			if prev, dup := c.byAlias[key]; dup && prev != i {
				return nil, fmt.Errorf("stockcatalog: alias %q is claimed by both %s (%s) and %s (%s)",
					a, c.stocks[prev].Code, c.stocks[prev].Name, c.stocks[i].Code, c.stocks[i].Name)
			}
			// An alias that collides with a DIFFERENT stock's code would make Resolve
			// ambiguous in the other direction: Lookup wins, and the alias would never fire.
			if j, isCode := c.byCode[normaliseCode(a)]; isCode && j != i {
				return nil, fmt.Errorf("stockcatalog: alias %q on %s is the code of %s (%s)",
					a, c.stocks[i].Code, c.stocks[j].Code, c.stocks[j].Name)
			}
			// An alias that is another company's OFFICIAL NAME is the identity failure this
			// package exists to stop, and it is silent: Resolve would hand back the alias's
			// owner, and a search for that name would rank the company it actually belongs to
			// BELOW it (ALIAS_EXACT outranks NAME_EXACT). Refused outright.
			if j, isName := byName[key]; isName && j != i {
				return nil, fmt.Errorf("stockcatalog: alias %q on %s (%s) is the official name "+
					"of %s", a, c.stocks[i].Code, c.stocks[i].Name, c.stocks[j].Code)
			}
			c.byAlias[key] = i
		}
	}
	return c, nil
}

// dedupeStrings drops blanks and repeats while PRESERVING first-seen order — the order a
// human wrote, which is the only order that means anything here.
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := normaliseQuery(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
