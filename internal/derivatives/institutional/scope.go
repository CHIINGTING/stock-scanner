package institutional

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// Instrument scope: which contract's lots a number is made of, recorded rather than inferred.
//
// The institutional futures feed carries 22 products in one response. 臺股期貨 (TX),
// 小型臺指期貨 (MTX) and 微型臺指期貨 are three different instruments with three different
// contract sizes, so their lots are not comparable and adding them produces a number with no
// unit at all — 1 TX lot is 4 MTX lots is 40 微型 lots, and a sum weights them equally.
// §10.4: scope is explicit and never inferred.

// The exchange's own product names, spelled once. These are the ContractCode values in the
// institutional feed, which are Chinese product names rather than the TX/MTX symbols the
// by-strike and settlement feeds use — the reconciliation between the three naming conventions
// belongs to derivative_contracts, not here.
const (
	ProductTX      = "臺股期貨"
	ProductMTX     = "小型臺指期貨"
	ProductMicroTX = "微型臺指期貨"
	ProductTE      = "電子期貨"
	ProductTF      = "金融期貨"
)

// SelectionPolicy names how the contracts in a scope were chosen. Recorded on every view: a
// coverage figure whose selection rule is unknown cannot be compared with another day's.
type SelectionPolicy string

// SelectSingleProductExact — exactly one product, matched on the exchange's ContractCode with
// no prefix or fuzzy matching. The only policy in v1.
//
// A multi-product policy is not merely unimplemented, it is unspecified: combining TX and MTX
// requires a contract-size conversion and an explicit statement of what the combined unit IS,
// and until that exists the honest answer is that the scope does not compose.
const SelectSingleProductExact SelectionPolicy = "SINGLE_PRODUCT_EXACT"

// ErrScopeMismatch is returned wherever two different instrument scopes would otherwise be
// combined. §10.4: mixing scopes must be impossible or an error, never a silent sum.
var ErrScopeMismatch = errors.New("institutional: instrument scopes differ")

// ErrInstitutionMismatch is returned wherever two different institutions would otherwise be
// combined into one series or one observation.
var ErrInstitutionMismatch = errors.New("institutional: institutions differ")

// InstrumentScope is the answer to "which contract are these lots".
type InstrumentScope struct {
	// ID is the short symbol used in output: TX, MTX, TMX. Not parsed from Product; the two
	// are supplied together so a rename on either side is visible.
	ID string `json:"id"`
	// Product is the exchange's ContractCode, verbatim.
	Product string `json:"product"`
	// Policy is how contracts were selected.
	Policy SelectionPolicy `json:"selection_policy"`
}

// NewSingleProductScope builds the only scope shape v1 supports.
func NewSingleProductScope(id, product string) (InstrumentScope, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(product) == "" {
		return InstrumentScope{}, fmt.Errorf(
			"institutional: a scope needs both an id and a product, got %q/%q", id, product)
	}
	return InstrumentScope{ID: id, Product: product, Policy: SelectSingleProductExact}, nil
}

func mustScope(id, product string) InstrumentScope {
	s, err := NewSingleProductScope(id, product)
	if err != nil {
		panic(err)
	}
	return s
}

// The three index-future scopes M4 reports on. Separate variables rather than a list, because
// the only correct way to use them is one at a time.
var (
	// ScopeTX is 臺股期貨, the large contract.
	ScopeTX = mustScope("TX", ProductTX)
	// ScopeMTX is 小型臺指期貨, one quarter of TX.
	ScopeMTX = mustScope("MTX", ProductMTX)
	// ScopeMicroTX is 微型臺指期貨, one tenth of MTX.
	ScopeMicroTX = mustScope("TMX", ProductMicroTX)
)

// Valid reports whether the scope was built through a constructor.
func (s InstrumentScope) Valid() error {
	if s.ID == "" || s.Product == "" {
		return fmt.Errorf("institutional: scope %+v is incomplete", s)
	}
	if s.Policy != SelectSingleProductExact {
		return fmt.Errorf("institutional: scope %s has selection policy %q; v1 supports only %s",
			s.ID, s.Policy, SelectSingleProductExact)
	}
	return nil
}

// Same reports whether two scopes select the same contracts under the same policy.
func (s InstrumentScope) Same(o InstrumentScope) bool {
	return s.ID == o.ID && s.Product == o.Product && s.Policy == o.Policy
}

// String renders the scope for an error message.
func (s InstrumentScope) String() string {
	return fmt.Sprintf("%s(%s)", s.ID, s.Product)
}

// Exclusion reasons, closed and named, so an excluded row is never just a smaller count.
const (
	// ExcludedOutOfScope — a different product. This is the common case and the important
	// one: 電子期貨 and 小型臺指期貨 rows sit in the same response as 臺股期貨.
	ExcludedOutOfScope = "OUT_OF_SCOPE_PRODUCT"
	// ExcludedOtherInstitution — a different institution's row.
	ExcludedOtherInstitution = "OTHER_INSTITUTION"
	// ExcludedOptionRow — a call/put row. The futures feed leaves CallPut empty; a non-empty
	// value means these are options, which are a different instrument again.
	ExcludedOptionRow = "OPTION_ROW"
	// ExcludedOtherTradingDate — a row from a different session. Never silently included:
	// that is how a CHANGE acquires a baseline it did not measure.
	ExcludedOtherTradingDate = "OTHER_TRADING_DATE"
)

// ExcludedContract is one rejected row, named with its reason.
type ExcludedContract struct {
	Product     string `json:"product"`
	Institution string `json:"institution"`
	CallPut     string `json:"call_put,omitempty"`
	TradingDate string `json:"trading_date,omitempty"`
	Reason      string `json:"reason"`
	Rows        int    `json:"rows"`
}

// ScopeCoverage is what the selection actually saw and kept.
//
// It exists so a view can state its own completeness. "AVAILABLE" over a response whose
// 臺股期貨 rows were missing entirely, with 21 other products present, is a fetch that
// succeeded and an observation that did not.
type ScopeCoverage struct {
	Scope             InstrumentScope    `json:"scope"`
	Institution       string             `json:"institution"`
	RowsSeen          int                `json:"rows_seen"`
	RowsIncluded      int                `json:"rows_included"`
	RowsExcluded      int                `json:"rows_excluded"`
	IncludedContracts []string           `json:"included_contracts"`
	ExcludedContracts []ExcludedContract `json:"excluded_contracts,omitempty"`
	// ProductsSeen is every distinct ContractCode in the input, in sorted order. A scope
	// whose product is not in this list did not merely have no data — it was not in the
	// response, which is a different fact and one worth seeing.
	ProductsSeen []string `json:"products_seen"`
}

// Ratio is included rows over rows seen, 0 when nothing was seen.
//
// It is a description of the selection, NOT a data-quality score: a TX-scoped view of a
// 22-product response has a ratio near 0.05 on a perfectly complete day.
func (c ScopeCoverage) Ratio() float64 {
	if c.RowsSeen == 0 {
		return 0
	}
	return float64(c.RowsIncluded) / float64(c.RowsSeen)
}

// FoundScopeProduct reports whether the scope's product appeared in the input at all.
func (c ScopeCoverage) FoundScopeProduct() bool {
	for _, p := range c.ProductsSeen {
		if p == c.Scope.Product {
			return true
		}
	}
	return false
}

// selection is the in-scope rows plus the coverage record describing how they were chosen.
type selection struct {
	rows     []provider.InstitutionalRow
	coverage ScopeCoverage
}

// selectScope keeps the rows belonging to exactly one (product, institution, trading date) and
// records every rejection with a reason.
//
// Nothing here sums across products. The function returns rows; the caller reads the ONE net
// row it needs out of them, and a duplicate is an error rather than an addend.
func selectScope(rows []provider.InstitutionalRow, scope InstrumentScope, institution, tradingDate string) selection {
	cov := ScopeCoverage{
		Scope:       scope,
		Institution: institution,
		RowsSeen:    len(rows),
	}
	products := map[string]bool{}
	excluded := map[string]*ExcludedContract{}
	contracts := map[string]bool{}

	note := func(r provider.InstitutionalRow, reason string) {
		key := reason + "|" + r.Product + "|" + r.Institution + "|" + r.CallPut + "|" + r.TradingDate
		e, ok := excluded[key]
		if !ok {
			e = &ExcludedContract{
				Product:     r.Product,
				Institution: r.Institution,
				CallPut:     r.CallPut,
				Reason:      reason,
			}
			if reason == ExcludedOtherTradingDate {
				e.TradingDate = r.TradingDate
			}
			excluded[key] = e
		}
		e.Rows++
		cov.RowsExcluded++
	}

	var kept []provider.InstitutionalRow
	for _, r := range rows {
		products[r.Product] = true
		switch {
		case r.Product != scope.Product:
			note(r, ExcludedOutOfScope)
		case r.Institution != institution:
			note(r, ExcludedOtherInstitution)
		case r.CallPut != "":
			note(r, ExcludedOptionRow)
		case tradingDate != "" && r.TradingDate != tradingDate:
			note(r, ExcludedOtherTradingDate)
		default:
			kept = append(kept, r)
			contracts[r.Product] = true
			cov.RowsIncluded++
		}
	}

	for p := range products {
		cov.ProductsSeen = append(cov.ProductsSeen, p)
	}
	sort.Strings(cov.ProductsSeen)
	for c := range contracts {
		cov.IncludedContracts = append(cov.IncludedContracts, c)
	}
	sort.Strings(cov.IncludedContracts)
	for _, e := range excluded {
		cov.ExcludedContracts = append(cov.ExcludedContracts, *e)
	}
	sort.Slice(cov.ExcludedContracts, func(i, j int) bool {
		a, b := cov.ExcludedContracts[i], cov.ExcludedContracts[j]
		if a.Reason != b.Reason {
			return a.Reason < b.Reason
		}
		if a.Product != b.Product {
			return a.Product < b.Product
		}
		return a.Institution < b.Institution
	})
	return selection{rows: kept, coverage: cov}
}
