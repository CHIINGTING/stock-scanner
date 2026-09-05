package stockcatalog

import (
	"sort"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// DefaultSearchLimit caps a search that did not ask for a size. The dashboard shows a
// dropdown, not a directory: returning 1,980 rows for "1" would be a slower way of saying
// nothing.
const DefaultSearchLimit = 20

// MatchKind says WHY a stock matched. It is returned to the caller because the dashboard
// shows it — a user who typed "台積" should be able to see that 台積電 matched by name and
// TSMC matched by alias, rather than wondering how the list was ordered.
type MatchKind string

const (
	MatchSymbolExact   MatchKind = "SYMBOL_EXACT"
	MatchAliasExact    MatchKind = "ALIAS_EXACT"
	MatchNameExact     MatchKind = "NAME_EXACT"
	MatchSymbolPrefix  MatchKind = "SYMBOL_PREFIX"
	MatchNamePrefix    MatchKind = "NAME_PREFIX"
	MatchAliasPrefix   MatchKind = "ALIAS_PREFIX"
	MatchNameContains  MatchKind = "NAME_CONTAINS"
	MatchAliasContains MatchKind = "ALIAS_CONTAINS"
)

// rank orders the kinds. Lower is better, and the order is the one a human expects: what you
// typed exactly, then what you started typing, then what merely contains it.
func (k MatchKind) rank() int {
	switch k {
	case MatchSymbolExact:
		return 0
	case MatchAliasExact:
		return 1
	case MatchNameExact:
		return 2
	case MatchSymbolPrefix:
		return 3
	case MatchNamePrefix:
		return 4
	case MatchAliasPrefix:
		return 5
	case MatchNameContains:
		return 6
	case MatchAliasContains:
		return 7
	default:
		return 99
	}
}

// Match is one search hit.
type Match struct {
	Stock Stock
	Kind  MatchKind
}

// SearchOptions tunes a search. The zero value is the dashboard's behaviour: enabled stocks
// only, DefaultSearchLimit results.
type SearchOptions struct {
	// Limit caps the result count. Zero or negative → DefaultSearchLimit.
	Limit int
	// IncludeDisabled brings stocks with enabled:false back into the results.
	IncludeDisabled bool
}

// Search finds stocks matching a free-text query.
//
// # Ordering is total, and that is a requirement rather than a nicety
//
// Results are sorted by (match kind, code). Both keys together are unique — codes are —
// so two runs over the same catalog with the same query produce byte-identical output. A
// search whose order depended on map iteration would make the dashboard's first row, and
// therefore the stock a hurried human clicks, effectively random.
//
// # An empty query returns nothing
//
// Not everything. A blank input in a search box means "you have not told me what you want
// yet", and answering it with the first 20 stocks by code invites someone to run a health
// check on 0050 by accident.
//
// # A stock appears at most once
//
// It is credited with its BEST match: 2330 matching "台積" by both name and alias is one
// result of kind NAME_PREFIX, not two rows for the same company.
func (c *Catalog) Search(q string, opt SearchOptions) []Match {
	if c == nil {
		return nil
	}
	needle := normaliseQuery(q)
	if needle == "" {
		return nil
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	best := make(map[int]MatchKind, 8)
	consider := func(i int, k MatchKind) {
		if prev, ok := best[i]; ok && prev.rank() <= k.rank() {
			return
		}
		best[i] = k
	}

	// A pasted canonical symbol ("2330.TW") must find the stock. Resolve already accepts it;
	// a search box that answered "no results" for the very form the API hands out would be a
	// dead end the user has no way to diagnose.
	codeNeedle := normaliseCode(q)
	if code, _, err := fetcher.ParseYahooSymbol(strings.TrimSpace(q)); err == nil {
		codeNeedle = normaliseCode(code)
	}
	for i := range c.stocks {
		s := &c.stocks[i]
		if !s.Enabled && !opt.IncludeDisabled {
			continue
		}
		switch {
		case s.Code == codeNeedle:
			consider(i, MatchSymbolExact)
		case strings.HasPrefix(s.Code, codeNeedle):
			consider(i, MatchSymbolPrefix)
		}

		name := normaliseQuery(s.Name)
		switch {
		case name == needle:
			consider(i, MatchNameExact)
		case strings.HasPrefix(name, needle):
			consider(i, MatchNamePrefix)
		case strings.Contains(name, needle):
			consider(i, MatchNameContains)
		}

		for _, a := range s.Aliases {
			alias := normaliseQuery(a)
			switch {
			case alias == needle:
				consider(i, MatchAliasExact)
			case strings.HasPrefix(alias, needle):
				consider(i, MatchAliasPrefix)
			case strings.Contains(alias, needle):
				consider(i, MatchAliasContains)
			}
		}
	}

	out := make([]Match, 0, len(best))
	for i, k := range best {
		// clone(), not a shallow copy: the returned Stock's slices must not share backing
		// arrays with the catalog that concurrent handlers are reading.
		out = append(out, Match{Stock: c.stocks[i].clone(), Kind: k})
	}
	sort.Slice(out, func(a, b int) bool {
		if ra, rb := out[a].Kind.rank(), out[b].Kind.rank(); ra != rb {
			return ra < rb
		}
		return out[a].Stock.Code < out[b].Stock.Code
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
