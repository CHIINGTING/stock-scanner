package model

import (
	"encoding/json"
	"testing"
	"time"
)

// nil dataset means MISSING; a present struct full of zeros means the market printed zeros.
// Conflating the two is how "no margin data" silently becomes "neutral margin".
func TestNilDatasetIsMissingNotZero(t *testing.T) {
	missing := &RawSnapshot{Date: "2026-08-07"}
	if missing.PresentCount() != 0 {
		t.Errorf("PresentCount = %d, want 0", missing.PresentCount())
	}
	if missing.Has(false, false, false, true, false) {
		t.Error("Has() claimed margin present when it is nil")
	}

	genuineZero := &RawSnapshot{
		Date: "2026-08-07",
		Cash: &CashData{ForeignBuy: 0, ForeignSell: 0, ForeignNet: 0},
	}
	if genuineZero.PresentCount() != 1 {
		t.Error("a genuine all-zero observation must count as present")
	}
	if !genuineZero.Has(false, false, true, false, false) {
		t.Error("Has() must see a present-but-zero dataset")
	}
}

func TestPresentCountAndHas(t *testing.T) {
	r := &RawSnapshot{
		Date:    "2026-08-07",
		Index:   &IndexData{Close: 44225.91},
		Cash:    &CashData{ForeignNet: -40715743790},
		Futures: &FuturesOIData{NetOI: -87911},
	}
	if got := r.PresentCount(); got != 3 {
		t.Errorf("PresentCount = %d, want 3", got)
	}
	if !r.Has(true, false, true, false, true) {
		t.Error("Has() should confirm index+cash+futures")
	}
	if r.Has(true, true, true, true, true) {
		t.Error("Has() should reject the missing breadth/margin")
	}
	var nilSnap *RawSnapshot
	if nilSnap.PresentCount() != 0 || nilSnap.Has(false, false, false, false, false) {
		t.Error("nil receiver must be safe and empty")
	}
}

// TWSE and TAIFEX each produce a partial snapshot for the same date; merging must never let
// one blank out what the other already supplied.
func TestMergeCombinesWithoutClobbering(t *testing.T) {
	twse := &RawSnapshot{
		Date:    "2026-08-07",
		Cash:    &CashData{ForeignNet: -40715743790},
		Margin:  &MarginBalanceData{MarginBalanceLots: 8986438, MarginPrevLots: 8958261},
		Sources: []SourceMeta{{Name: "BFI82U", Status: SourceOK}},
	}
	taifex := &RawSnapshot{
		Date:    "2026-08-07",
		Futures: &FuturesOIData{NetOI: -87911},
		Sources: []SourceMeta{{Name: "TAIFEX", Status: SourceOK}},
	}
	twse.Merge(taifex)

	if twse.Futures == nil || twse.Futures.NetOI != -87911 {
		t.Error("merge lost the futures dataset")
	}
	if twse.Cash == nil || twse.Cash.ForeignNet != -40715743790 {
		t.Error("merge clobbered the cash dataset")
	}
	if len(twse.Sources) != 2 {
		t.Errorf("sources = %d, want both provenance records", len(twse.Sources))
	}

	// A later provider must not overwrite an existing dataset.
	other := &RawSnapshot{Futures: &FuturesOIData{NetOI: 12345}}
	twse.Merge(other)
	if twse.Futures.NetOI != -87911 {
		t.Errorf("merge overwrote existing data: NetOI = %v", twse.Futures.NetOI)
	}
	// nil operands must be inert, not panic.
	twse.Merge(nil)
	var nilSnap *RawSnapshot
	nilSnap.Merge(taifex)
}

// The endpoint carries both balances, so the daily change is a subtraction, not a second
// request against yesterday's file.
func TestMarginChangeDerivedFromSameResponse(t *testing.T) {
	m := MarginBalanceData{
		MarginBalanceLots: 8986438, MarginPrevLots: 8958261,
		ShortBalanceLots: 192740, ShortPrevLots: 191897,
	}
	if got := m.MarginChangeLots(); got != 28177 {
		t.Errorf("MarginChangeLots = %v, want 28177", got)
	}
	if got := m.ShortChangeLots(); got != 843 {
		t.Errorf("ShortChangeLots = %v, want 843", got)
	}
}

// NetOI is taken from the official column, not recomputed — but the two should agree, and a
// mismatch is a signal that the upstream layout changed.
func TestFuturesNetMatchesOfficialColumn(t *testing.T) {
	f := FuturesOIData{LongOI: 8321, ShortOI: 96232, NetOI: -87911}
	if f.LongOI-f.ShortOI != f.NetOI {
		t.Errorf("long-short = %v but official net = %v", f.LongOI-f.ShortOI, f.NetOI)
	}
}

func TestRawSnapshotJSONOmitsMissing(t *testing.T) {
	r := RawSnapshot{
		Date:      "2026-08-07",
		FetchedAt: time.Date(2026, 8, 7, 15, 0, 0, 0, time.UTC),
		Cash:      &CashData{ForeignNet: -1},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// Missing datasets must be absent from the JSON, not present as nulls or zero structs.
	for _, absent := range []string{`"index"`, `"breadth"`, `"margin"`, `"futures"`} {
		if contains(s, absent) {
			t.Errorf("missing dataset %s should be omitted, got: %s", absent, s)
		}
	}
	if !contains(s, `"cash"`) {
		t.Errorf("present dataset dropped: %s", s)
	}

	var back RawSnapshot
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Index != nil || back.Margin != nil {
		t.Error("absent datasets must decode back to nil")
	}
	if back.Cash == nil || back.Cash.ForeignNet != -1 {
		t.Error("present dataset lost in round trip")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
