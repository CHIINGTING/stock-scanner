package httpapi

import (
	"net/http"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
)

// maxSearchLimit bounds what one request may ask for. The catalog holds ~2,000 stocks; a
// dashboard dropdown that wanted all of them would be a directory listing, not a search.
const maxSearchLimit = 100

// StockResult is one search hit, as the API presents it.
//
// The fields are chosen to be exactly what a client needs to display a row and then send
// back an identity. Deliberately absent: NameSource and Enabled — those are internal
// bookkeeping, and shipping configuration detail to a browser is how internals become a
// contract nobody meant to make.
type StockResult struct {
	// Symbol is the identity — the code, which is what the health endpoint takes.
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
	// Market is the display label: TWSE / TPEX / UNKNOWN.
	Market string `json:"market"`
	// MarketCode is the repo's internal form (TW / TWO), empty when unknown. A client that
	// wants to build "2330.TW" uses this; one that wants to show a badge uses Market.
	MarketCode string `json:"market_code,omitempty"`
	// MarketSource is CATALOG or UNKNOWN, so a UI can say "market not recorded" rather than
	// implying the stock is on some third exchange.
	MarketSource string `json:"market_source"`

	Industry string   `json:"industry,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
	Tags     []string `json:"tags,omitempty"`

	// Match says why this row matched — SYMBOL_EXACT, ALIAS_EXACT, NAME_PREFIX, …
	Match string `json:"match"`
}

// SearchResponse is the /api/v1/stocks payload.
type SearchResponse struct {
	Query string `json:"query"`
	Count int    `json:"count"`
	// Results is never null: an empty search returns [], because a client that has to handle
	// both null and [] will eventually handle only one of them.
	Results []StockResult `json:"results"`
}

// handleStockSearch serves GET /api/v1/stocks?q=…
//
// Read-only in every sense: it touches the in-memory catalog and nothing else — no disk, no
// network, no scanner state.
func (s *Server) handleStockSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	limit, err := intParam(r, "limit", stockcatalog.DefaultSearchLimit, 1, maxSearchLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
		return
	}
	includeDisabled, err := boolParam(r, "include_disabled", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
		return
	}
	if s.Catalog == nil {
		writeError(w, http.StatusServiceUnavailable, ErrUnavailable, "stock catalog is not loaded")
		return
	}

	// An empty query is a valid request with an empty answer — 200 with zero results, not a
	// 400. The search box starts empty on every page load, and a browser that greeted the
	// user with a red error would be reporting a bug that does not exist.
	matches := s.Catalog.Search(q, stockcatalog.SearchOptions{
		Limit:           limit,
		IncludeDisabled: includeDisabled,
	})

	resp := SearchResponse{Query: q, Count: len(matches), Results: make([]StockResult, 0, len(matches))}
	for _, m := range matches {
		resp.Results = append(resp.Results, newStockResult(m.Stock, string(m.Kind)))
	}
	writeJSON(w, http.StatusOK, resp)
}

// newStockResult projects a catalog entry onto the wire shape.
func newStockResult(s stockcatalog.Stock, match string) StockResult {
	return StockResult{
		Symbol:       s.Symbol(),
		Name:         s.Name,
		Market:       s.MarketLabel(),
		MarketCode:   s.Market,
		MarketSource: string(s.MarketSource),
		Industry:     s.Industry,
		Aliases:      s.Aliases,
		Tags:         s.Tags,
		Match:        match,
	}
}
