package options

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// Institutional call/put positions: M4's FLOW / POSITION / CHANGE, on the two option sides.
//
// §10.8: "M4's definitions carry over unchanged. What must not appear is `call net + put net`
// presented as directional exposure: calls and puts have different deltas, different strikes
// and different sides, so their lot counts do not add into a market view. A combined figure
// may only be labelled a contract-count summary. Delta-weighted exposure needs the delta feed
// and its own work item."
//
// The reuse here is total rather than nominal: each side becomes an institutional.Observation,
// so institutional.ChangeOver, ResolveBaseline, ChangeHorizons and Percentiles all apply to it
// verbatim, with M4's baseline walk, M4's tolerance, M4's horizons and M4's three-distribution
// rule. Nothing about CHANGE is reimplemented here.
//
// The side lives in the InstrumentScope, which is exactly what that type is for: 臺指選擇權
// calls and 臺指選擇權 puts are different instruments whose lots are not comparable, so M4's
// ErrScopeMismatch already refuses to difference one against the other, and the refusal costs
// no new code.

// ProductTXOOptions is the institutional feed's own name for the TXO options series. The
// institutional feeds use Chinese product names where the by-strike feed uses TXO; the
// reconciliation between the naming conventions belongs to derivative_contracts, not here.
const ProductTXOOptions = "臺指選擇權"

// The two side scopes. Separate variables, because the only correct way to use them is one at
// a time — and because M4's Same() then makes a call/put mix an error rather than a sum.
var (
	// ScopeTXOCall is 臺指選擇權 買權.
	ScopeTXOCall = mustSideScope(provider.CallSide)
	// ScopeTXOPut is 臺指選擇權 賣權.
	ScopeTXOPut = mustSideScope(provider.PutSide)
)

func mustSideScope(side string) institutional.InstrumentScope {
	s, err := institutional.NewSingleProductScope("TXO_"+side, ProductTXOOptions)
	if err != nil {
		panic(err)
	}
	return s
}

// SideScope is the scope for one side of one product.
func SideScope(product, side string) (institutional.InstrumentScope, error) {
	switch side {
	case provider.CallSide, provider.PutSide:
	default:
		return institutional.InstrumentScope{},
			fmt.Errorf("options: %q is not CALL or PUT", side)
	}
	return institutional.NewSingleProductScope(shortSide(product, side), product)
}

func shortSide(product, side string) string {
	if product == ProductTXOOptions {
		return "TXO_" + side
	}
	return product + "_" + side
}

// ── the contract-count summary ────────────────────────────────────────────────────────

// ContractCountSummaryLabel is the ONLY thing a combined call+put figure may be called.
const ContractCountSummaryLabel = "CONTRACT_COUNT_SUMMARY"

// ContractCountAggregation records HOW the two sides were combined, and it is the load-bearing
// half of the label.
//
// SUM_OF_MAGNITUDES: |call| + |put|. Not call + put. The distinction is not presentational —
// a signed sum of the two nets is precisely the "directional exposure" §10.8 forbids, and it
// is arithmetically capable of reporting 0 for an institution holding 100 net calls and 100
// net puts. A sum of magnitudes cannot express a side at all, which is what makes it safe to
// publish beside two figures that can.
const ContractCountAggregation = "SUM_OF_MAGNITUDES"

// ContractCountSummary is how many CONTRACTS an institution has on the two sides together.
//
// It is not a market view and cannot be turned into one: calls and puts have different deltas,
// different strikes and different sides. Delta-weighted exposure needs the delta feed and its
// own work item (§10.8).
type ContractCountSummary struct {
	Label       string                       `json:"label"`
	Aggregation string                       `json:"aggregation"`
	Semantics   institutional.ValueSemantics `json:"semantics"`
	Contracts   institutional.ObservedMetric `json:"contracts"`
	Institution string                       `json:"institution"`
	Product     string                       `json:"product"`
	// Caveat travels with the value so a renderer that prints one field prints the warning.
	Caveat string `json:"caveat"`
}

// Directional is false, always, and exists so a caller can assert it rather than assume it.
func (s ContractCountSummary) Directional() bool { return false }

// Value returns the contract count and whether there is one.
func (s ContractCountSummary) Value() (float64, bool) { return s.Contracts.Lots() }

// newContractCountSummary combines two sides' lot counts as MAGNITUDES.
//
// Absent when either side is absent: a "both sides" count over one side is not a both-sides
// count, and filling the other with 0 would understate it silently.
func newContractCountSummary(sem institutional.ValueSemantics, institution, product string,
	call, put institutional.ObservedMetric, prov institutional.Provenance) ContractCountSummary {

	s := ContractCountSummary{
		Label:       ContractCountSummaryLabel,
		Aggregation: ContractCountAggregation,
		Semantics:   sem,
		Institution: institution,
		Product:     product,
		Caveat: "a count of contracts on both sides, combined as |call| + |put| so that it " +
			"cannot express a direction; calls and puts have different deltas, strikes and " +
			"sides, and a delta-weighted exposure needs the delta feed and its own work item",
	}
	c, hasCall := call.Lots()
	p, hasPut := put.Lots()
	switch {
	case hasCall && hasPut:
		s.Contracts = institutional.NewObservedMetric(sem, math.Abs(c)+math.Abs(p), prov)
	case !hasCall && !hasPut:
		s.Contracts = institutional.NewAbsentMetric(sem, call.Status,
			"neither side carries a "+string(sem)+" figure", prov)
	case !hasCall:
		s.Contracts = institutional.NewAbsentMetric(sem, call.Status,
			"the CALL side carries no "+string(sem)+" figure, so a both-sides count would be "+
				"the put side wearing a both-sides label", prov)
	default:
		s.Contracts = institutional.NewAbsentMetric(sem, put.Status,
			"the PUT side carries no "+string(sem)+" figure, so a both-sides count would be "+
				"the call side wearing a both-sides label", prov)
	}
	return s
}

// ── the call/put observation ──────────────────────────────────────────────────────────

// CallPutCoverage is what the side selection saw and kept.
type CallPutCoverage struct {
	Product      string   `json:"product"`
	Institution  string   `json:"institution"`
	RowsSeen     int      `json:"rows_seen"`
	CallRows     int      `json:"call_rows"`
	PutRows      int      `json:"put_rows"`
	RowsExcluded int      `json:"rows_excluded"`
	ProductsSeen []string `json:"products_seen"`
	SidesSeen    []string `json:"sides_seen"`
	SidesMissing []string `json:"sides_missing,omitempty"`
	FoundProduct bool     `json:"found_product"`
}

// InstitutionalCallPutRequest is everything about a reading that does not come from the rows.
type InstitutionalCallPutRequest struct {
	TradingDate string
	// Session must be COMBINED: the institutional calls-and-puts feed carries Date,
	// ContractCode, CallPut and Item and no session dimension, so its policy is fixed by the
	// data contract exactly as the futures feed's is (§10.4). It is never read off a clock.
	Session      string
	Dataset      string
	Product      string
	Institution  string
	SourceStatus institutional.MetricStatus
	SnapshotID   int64
	Revision     int
	FetchedAt    time.Time
}

// InstitutionalCallPut is one institution's two sides for one date, plus the only combined
// figure §10.8 allows.
type InstitutionalCallPut struct {
	TradingDate  string                     `json:"trading_date"`
	Session      string                     `json:"session"`
	Dataset      string                     `json:"dataset"`
	Product      string                     `json:"product"`
	Institution  string                     `json:"institution"`
	SourceStatus institutional.MetricStatus `json:"source_status"`
	// Call and Put are M4 observations, so every M4 function applies to them unchanged.
	Call institutional.Observation `json:"call"`
	Put  institutional.Observation `json:"put"`
	// FlowSummary and PositionSummary are contract counts, never exposures.
	FlowSummary     ContractCountSummary `json:"flow_summary"`
	PositionSummary ContractCountSummary `json:"position_summary"`
	Coverage        CallPutCoverage      `json:"coverage"`
}

// Side returns one side's M4 observation.
func (c InstitutionalCallPut) Side(side string) (institutional.Observation, error) {
	switch side {
	case provider.CallSide:
		return c.Call, nil
	case provider.PutSide:
		return c.Put, nil
	}
	return institutional.Observation{}, fmt.Errorf("options: %q is not CALL or PUT", side)
}

// NewInstitutionalCallPut reads ONE institution's two sides out of a decoded calls-and-puts
// response.
//
// It picks the NET rows of each semantics for the requested product, institution and side, and
// ignores the rest: it does not add LONG and SHORT, and it does not fall back to long − short
// when NET is absent. An absent NET stays absent. Those are M4's rules and they are the same
// rules because the failure is the same one.
func NewInstitutionalCallPut(req InstitutionalCallPutRequest, rows []provider.InstitutionalRow) (
	InstitutionalCallPut, error) {

	if _, err := time.Parse(institutional.DateLayout, req.TradingDate); err != nil {
		return InstitutionalCallPut{}, fmt.Errorf("%w: trading date %q", ErrBadDate, req.TradingDate)
	}
	if req.Session != institutional.SessionCombined {
		return InstitutionalCallPut{}, fmt.Errorf(
			"%w: the institutional calls-and-puts feed has no session dimension, so its session "+
				"policy is %s, not %q", institutional.ErrSessionMismatch,
			institutional.SessionCombined, req.Session)
	}
	if req.Product == "" || req.Institution == "" || req.Dataset == "" {
		return InstitutionalCallPut{}, errors.New(
			"options: a call/put observation needs a product, an institution and a dataset")
	}
	if !req.SourceStatus.IsSourceStatus() {
		return InstitutionalCallPut{}, fmt.Errorf(
			"options: source status %q is not one of %v", req.SourceStatus,
			institutional.SourceStatuses)
	}

	cov := CallPutCoverage{
		Product: req.Product, Institution: req.Institution, RowsSeen: len(rows),
	}
	products := map[string]bool{}
	sides := map[string]bool{}
	kept := map[string][]provider.InstitutionalRow{}
	for _, r := range rows {
		products[r.Product] = true
		if r.Product != req.Product || r.Institution != req.Institution ||
			(r.TradingDate != "" && r.TradingDate != req.TradingDate) {
			cov.RowsExcluded++
			continue
		}
		if r.CallPut != provider.CallSide && r.CallPut != provider.PutSide {
			// The futures feed leaves CallPut empty. A row without a side in a call/put
			// aggregate has no place to go and is never counted into one of them.
			cov.RowsExcluded++
			continue
		}
		sides[r.CallPut] = true
		kept[r.CallPut] = append(kept[r.CallPut], r)
		if r.CallPut == provider.CallSide {
			cov.CallRows++
		} else {
			cov.PutRows++
		}
	}
	for p := range products {
		cov.ProductsSeen = append(cov.ProductsSeen, p)
		if p == req.Product {
			cov.FoundProduct = true
		}
	}
	sort.Strings(cov.ProductsSeen)
	for _, s := range []string{provider.CallSide, provider.PutSide} {
		if sides[s] {
			cov.SidesSeen = append(cov.SidesSeen, s)
		} else {
			cov.SidesMissing = append(cov.SidesMissing, s)
		}
	}

	if !req.SourceStatus.Usable() && (cov.CallRows > 0 || cov.PutRows > 0) {
		return InstitutionalCallPut{}, fmt.Errorf(
			"options: source status %s carries in-scope rows for %s %s %s — a status saying "+
				"nothing was observed cannot travel with an observation",
			req.SourceStatus, req.Institution, req.Product, req.TradingDate)
	}

	out := InstitutionalCallPut{
		TradingDate: req.TradingDate, Session: req.Session, Dataset: req.Dataset,
		Product: req.Product, Institution: req.Institution,
		SourceStatus: req.SourceStatus, Coverage: cov,
	}
	for _, side := range []string{provider.CallSide, provider.PutSide} {
		scope, err := SideScope(req.Product, side)
		if err != nil {
			return InstitutionalCallPut{}, err
		}
		obs := institutional.Observation{
			TradingDate:  req.TradingDate,
			Session:      req.Session,
			Dataset:      req.Dataset,
			Institution:  req.Institution,
			Scope:        scope,
			SourceStatus: req.SourceStatus,
			SnapshotID:   req.SnapshotID,
			Revision:     req.Revision,
			FetchedAt:    req.FetchedAt,
			Coverage: institutional.ScopeCoverage{
				Scope:             scope,
				Institution:       req.Institution,
				RowsSeen:          len(rows),
				RowsIncluded:      len(kept[side]),
				RowsExcluded:      len(rows) - len(kept[side]),
				IncludedContracts: includedContracts(kept[side]),
				ProductsSeen:      cov.ProductsSeen,
			},
		}
		seen := map[string]bool{}
		for _, r := range kept[side] {
			if r.Side != provider.SideNet {
				continue
			}
			key := r.Semantics + "|" + r.Side
			if seen[key] {
				return InstitutionalCallPut{}, fmt.Errorf(
					"%w: %s %s %s %s carries two %s NET rows; summing them would double-count "+
						"and choosing one would be arbitrary", institutional.ErrDuplicateObservation,
					req.Institution, req.Product, side, req.TradingDate, r.Semantics)
			}
			seen[key] = true
			switch r.Semantics {
			case provider.SemanticsFlow:
				obs.FlowNetLots = copyLots(r.Lots)
			case provider.SemanticsPosition:
				obs.PositionNetLots = copyLots(r.Lots)
			}
		}
		if side == provider.CallSide {
			out.Call = obs
		} else {
			out.Put = obs
		}
	}

	prov := out.Call.Prov()
	prov.Session = req.Session
	out.FlowSummary = newContractCountSummary(institutional.SemanticsFlow, req.Institution,
		req.Product, out.Call.Flow().ObservedMetric, out.Put.Flow().ObservedMetric, prov)
	out.PositionSummary = newContractCountSummary(institutional.SemanticsPosition,
		req.Institution, req.Product, out.Call.Position().ObservedMetric,
		out.Put.Position().ObservedMetric, prov)
	return out, nil
}

func includedContracts(rows []provider.InstitutionalRow) []string {
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Product] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func copyLots(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
