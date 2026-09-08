package options_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/options"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// ── the two formulas, and the swap that produces a plausible wrong number ─────────────

// TestBothFormulasOverTheFixture is §10.8's arithmetic, computed rather than quoted.
func TestBothFormulasOverTheFixture(t *testing.T) {
	rows := fixtureRows(t)

	for _, c := range []struct {
		name            string
		family          options.ContractFamily
		session         string
		putVol, callVol float64
		putOI, callOI   float64
		oiAvailable     bool
		oiStatus        options.RatioStatus
	}{
		{"day monthly", options.FamilyMonthly, provider.SessionDay,
			dayMonthlyPutVol, dayMonthlyCallVol, dayMonthlyPutOI, dayMonthlyCallOI, true, options.RatioAvailable},
		{"day weekly", options.FamilyWeekly, provider.SessionDay,
			dayWeeklyPutVol, dayWeeklyCallVol, dayWeeklyPutOI, dayWeeklyCallOI, true, options.RatioAvailable},
		{"night monthly", options.FamilyMonthly, provider.SessionAfterHours,
			nightMonthlyPutVol, nightMonthlyCallVol, 0, 0, false, options.RatioInsufficientData},
		{"night weekly", options.FamilyWeekly, provider.SessionAfterHours,
			nightWeeklyPutVol, nightWeeklyCallVol, 0, 0, false, options.RatioInsufficientData},
	} {
		t.Run(c.name, func(t *testing.T) {
			obs := mustObserve(t, req(c.family, c.session), rows)

			// VolumePCR = Σ put TradingVolume / Σ call TradingVolume.
			assertLots(t, "put volume", obs.Volume.Numerator, c.putVol)
			assertLots(t, "call volume", obs.Volume.Denominator, c.callVol)
			assertRatio(t, "volume PCR", obs.Volume.Ratio, c.putVol/c.callVol)

			if !c.oiAvailable {
				if obs.OI.Ratio.Observed {
					t.Fatalf("OI PCR has a ratio for %s — every 盤後 row carries "+
						"OpenInterest \"-\", so there is nothing to divide", c.name)
				}
				if obs.OI.Status != c.oiStatus {
					t.Errorf("OI status = %s, want %s", obs.OI.Status, c.oiStatus)
				}
				return
			}
			// OIPCR = Σ put OpenInterest / Σ call OpenInterest.
			assertLots(t, "put OI", obs.OI.Numerator, c.putOI)
			assertLots(t, "call OI", obs.OI.Denominator, c.callOI)
			assertRatio(t, "OI PCR", obs.OI.Ratio, c.putOI/c.callOI)
		})
	}
}

// TestTheTwoMetricsReadDifferentColumns is the swap test.
//
// A volume PCR built from the open-interest columns still produces a ratio, still reports
// AVAILABLE, and is wrong by an order of magnitude — TXO put volume 63,014 against put open
// interest 5,651 on the same session. Nothing but the numbers themselves can catch it, so the
// numbers are what this asserts, on both sides, in both directions.
func TestTheTwoMetricsReadDifferentColumns(t *testing.T) {
	rows := fixtureRows(t)
	vol := mustObserve(t, req(options.FamilyAll, provider.SessionCombined), rows).Volume
	oi := mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows).OI

	assertLots(t, "put volume", vol.Numerator, allBothSessionsPutVol)
	assertLots(t, "put OI", oi.Numerator, allDayPutOI)

	if vol.Numerator.Semantics != institutional.SemanticsFlow {
		t.Errorf("volume numerator carries semantics %s, want FLOW — a sum that reaches a "+
			"reader must say whether it is traded or held", vol.Numerator.Semantics)
	}
	if oi.Numerator.Semantics != institutional.SemanticsPosition {
		t.Errorf("OI numerator carries semantics %s, want POSITION", oi.Numerator.Semantics)
	}

	v, _ := vol.Value()
	o, _ := oi.Value()
	if v == o {
		t.Fatal("the volume and open-interest ratios are identical — one of them is reading " +
			"the other's column")
	}
	if pv, _ := vol.Numerator.Lots(); pv == allDayPutOI {
		t.Error("the volume numerator equals the open-interest numerator")
	}
	if po, _ := oi.Numerator.Lots(); po == allBothSessionsPutVol {
		t.Error("the open-interest numerator equals the volume numerator")
	}
}

// TestTheContainersRefuseTheOtherMetric: the compiler stops `oi = vol`, and Valid() stops the
// same mistake arriving by deserialisation, where the type is gone and only the field survives.
func TestTheContainersRefuseTheOtherMetric(t *testing.T) {
	obs := mustObserve(t, req(options.FamilyAll, provider.SessionDay), fixtureRows(t))
	if err := obs.Volume.Valid(); err != nil {
		t.Errorf("a well-formed VolumePCR reports %v", err)
	}
	if err := obs.OI.Valid(); err != nil {
		t.Errorf("a well-formed OIPCR reports %v", err)
	}
	smuggled := options.OIPCR{PCR: obs.Volume.PCR}
	if err := smuggled.Valid(); err == nil {
		t.Fatal("a VolumePCR inside an OIPCR container validated — the metric type is part " +
			"of the name for exactly this reason")
	}
}

// ── the night session ─────────────────────────────────────────────────────────────────

// TestTheNightSessionHasNoOpenInterestAtAll is §10.8's structural fact, and both of its
// consequences.
func TestTheNightSessionHasNoOpenInterestAtAll(t *testing.T) {
	rows := fixtureRows(t)

	night := mustObserve(t, req(options.FamilyAll, provider.SessionAfterHours), rows)
	if night.OI.Ratio.Observed {
		t.Fatal("an AFTER_HOURS OI PCR exists — 0/0 was computed or the absences were " +
			"zero-filled, and either way the ratio is fabricated")
	}
	if night.OI.Status != options.RatioInsufficientData {
		t.Errorf("AFTER_HOURS OI status = %s, want INSUFFICIENT_DATA", night.OI.Status)
	}
	if night.OI.Numerator.Observed || night.OI.Denominator.Observed {
		t.Error("an AFTER_HOURS open-interest side reports a number; every 盤後 row carries " +
			"OpenInterest \"-\", which §3.1 makes ABSENT rather than zero")
	}
	if night.OI.Coverage.AbsentValues == 0 {
		t.Error("no absent open-interest values were counted, so the coverage record cannot " +
			"show why the ratio is not there")
	}
	if len(night.OI.Coverage.ActualContracts) != 0 {
		t.Errorf("contracts %v contributed open interest at night", night.OI.Coverage.ActualContracts)
	}
	if len(night.OI.Coverage.ExpectedContracts) == 0 {
		t.Error("no contracts were expected either, so the coverage says nothing was asked for")
	}

	// The volume half needs BOTH sessions: 一般 alone is 41,529 against 63,014.
	day := mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows)
	both := mustObserve(t, req(options.FamilyAll, provider.SessionCombined), rows)
	dayPut, _ := day.Volume.Numerator.Lots()
	bothPut, _ := both.Volume.Numerator.Lots()
	if dayPut != dayMonthlyPutVol+dayWeeklyPutVol {
		t.Errorf("一般 put volume = %v, want %v", dayPut, dayMonthlyPutVol+dayWeeklyPutVol)
	}
	if bothPut != allBothSessionsPutVol {
		t.Errorf("both-session put volume = %v, want %v", bothPut, allBothSessionsPutVol)
	}
	if dayPut >= bothPut {
		t.Error("一般 alone is not below the two sessions together — the night rows were " +
			"either counted twice or not at all")
	}
}

// TestDayAndAfterHoursAreDifferentMeasurements: merging the two sessions is silent, and it is
// only visible as a number.
func TestDayAndAfterHoursAreDifferentMeasurements(t *testing.T) {
	rows := fixtureRows(t)
	day := mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows).Volume
	night := mustObserve(t, req(options.FamilyAll, provider.SessionAfterHours), rows).Volume
	both := mustObserve(t, req(options.FamilyAll, provider.SessionCombined), rows).Volume

	dayPut, _ := day.Numerator.Lots()
	nightPut, _ := night.Numerator.Lots()
	bothPut, _ := both.Numerator.Lots()

	if dayPut == nightPut {
		t.Fatal("the two sessions produced identical put volume — the session filter is not " +
			"filtering")
	}
	if dayPut+nightPut != bothPut {
		t.Errorf("%v + %v != %v — the two row sets are disjoint by construction (every row "+
			"carries exactly one TradingSession), so the sum must be exact",
			dayPut, nightPut, bothPut)
	}
	if d, _ := day.Value(); d == mustValue(t, both) {
		t.Error("the DAY ratio equals the combined ratio — the night rows are in both")
	}
	for _, cov := range []options.Coverage{day.Coverage, night.Coverage} {
		if len(cov.SessionsExpected) != 1 {
			t.Errorf("a single-session figure expects %v sessions", cov.SessionsExpected)
		}
	}
	if len(both.Coverage.SessionsPresent) != 2 {
		t.Errorf("the combined figure names %v sessions as present, want both",
			both.Coverage.SessionsPresent)
	}
}

// TestTheSessionPolicyIsPerMetric pins §10.8's table itself.
func TestTheSessionPolicyIsPerMetric(t *testing.T) {
	if got := options.SessionPolicy(options.MetricOIPCR); len(got) != 1 || got[0] != provider.SessionDay {
		t.Errorf("OI PCR session policy = %v, want [DAY] — the night session reports no open "+
			"interest, so there is nothing else to publish", got)
	}
	got := options.SessionPolicy(options.MetricVolumePCR)
	if len(got) != 3 {
		t.Fatalf("volume PCR session policy = %v, want DAY, AFTER_HOURS and their sum", got)
	}
	if options.SessionInPolicy(options.MetricOIPCR, provider.SessionAfterHours) {
		t.Error("an AFTER_HOURS OI PCR is inside the policy")
	}

	// Outside the policy is still COMPUTABLE — §10.8 requires the AFTER_HOURS OI PCR to
	// answer INSUFFICIENT_DATA rather than to be unaskable — and it is warned about.
	night := mustObserve(t, req(options.FamilyAll, provider.SessionAfterHours), fixtureRows(t))
	if !hasWarning(night.OI.Warnings, options.WarnSessionOutsidePolicy) {
		t.Errorf("no %s warning on an out-of-policy figure: %+v",
			options.WarnSessionOutsidePolicy, night.OI.Warnings)
	}
}

// ── weekly and monthly ────────────────────────────────────────────────────────────────

// TestWeeklyAndMonthlyAreNotTheSameNumber.
//
// The families are decided by M3's ClassifyExpiry and nothing else. Merging them, or
// classifying 202609F1 as monthly (which is what reading TXU's ContractDeliveryMonth would
// do — §3.1's trap), moves lots between the two aggregates while both stay AVAILABLE.
func TestWeeklyAndMonthlyAreNotTheSameNumber(t *testing.T) {
	rows := fixtureRows(t)
	weekly := mustObserve(t, req(options.FamilyWeekly, provider.SessionDay), rows)
	monthly := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay), rows)
	all := mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows)

	wv, _ := weekly.Volume.Value()
	mv, _ := monthly.Volume.Value()
	if wv == mv {
		t.Fatal("the weekly and monthly volume ratios are identical — the family filter is " +
			"not filtering")
	}
	assertLots(t, "weekly put volume", weekly.Volume.Numerator, dayWeeklyPutVol)
	assertLots(t, "monthly put volume", monthly.Volume.Numerator, dayMonthlyPutVol)

	// ALL is the two together, which is what makes a merge invisible in the total and
	// visible only in the parts.
	allPut, _ := all.Volume.Numerator.Lots()
	if allPut != dayWeeklyPutVol+dayMonthlyPutVol {
		t.Errorf("ALL put volume = %v, want %v", allPut, dayWeeklyPutVol+dayMonthlyPutVol)
	}

	// The F series is WEEKLY, and it is the bulk of the market. If it were classified
	// monthly the monthly aggregate would swallow it.
	if !contains(weekly.Volume.Identity.Expiries, settlingExpiry) {
		t.Errorf("%s is not in the weekly identity %v — it is a TXO weekly series and §3.1 "+
			"records what excluding the F series costs", settlingExpiry,
			weekly.Volume.Identity.Expiries)
	}
	for _, code := range monthly.Volume.Identity.Expiries {
		if len(code) != 6 {
			t.Errorf("%s is in the MONTHLY identity; only bare YYYYMM codes are monthly", code)
		}
	}
}

// TestOnlyOpenInterestExcludesTheSettlingExpiry: the rule is asymmetric and each half matched
// the exchange exactly on the full capture.
func TestOnlyOpenInterestExcludesTheSettlingExpiry(t *testing.T) {
	rows := fixtureRows(t)
	withRule := mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows)

	notSettlement := req(options.FamilyAll, provider.SessionDay)
	notSettlement.SettlingExpiry = "" // the same session read as a non-settlement day
	without := mustObserve(t, notSettlement, rows)

	a, _ := withRule.OI.Numerator.Lots()
	b, _ := without.OI.Numerator.Lots()
	if b <= a {
		t.Fatalf("including the settling expiry gave put OI %v, not above %v — the fixture no "+
			"longer carries 202609F1's open interest", b, a)
	}
	if ratio := b / a; ratio < 1.5 {
		t.Errorf("including 202609F1 inflates put OI only %.2fx; on the live session it was "+
			"223,014 against 96,420", ratio)
	}

	av, _ := withRule.Volume.Numerator.Lots()
	bv, _ := without.Volume.Numerator.Lots()
	if av != bv {
		t.Errorf("the settling expiry changed VOLUME (%v vs %v) — its lots were genuinely "+
			"traded and the exclusion is open interest's alone", av, bv)
	}

	if !contains(withRule.OI.Identity.ExcludedExpiries, settlingExpiry) {
		t.Errorf("the open-interest identity does not name %s as excluded: %+v",
			settlingExpiry, withRule.OI.Identity)
	}
	if contains(withRule.OI.Identity.Expiries, settlingExpiry) {
		t.Error("the settling expiry is still inside the open-interest identity, so a " +
			"rollover comparison would see it as live exposure")
	}
	if !contains(withRule.Volume.Identity.Expiries, settlingExpiry) {
		t.Error("the settling expiry left the VOLUME identity too")
	}
}

// TestOpenInterestRequiresASettlementRead: §3.1 makes the settlement feed a dependency of the
// open-interest half and of nothing else.
func TestOpenInterestRequiresASettlementRead(t *testing.T) {
	r := req(options.FamilyAll, provider.SessionDay)
	r.SettlementRead = false
	r.SettlingExpiry = ""
	obs := mustObserve(t, r, fixtureRows(t))

	if obs.OI.Ratio.Observed {
		t.Fatal("an open-interest ratio was published with no settlement read behind it; on " +
			"2026-09-04 that is 223,014 against an official 96,420, status AVAILABLE")
	}
	if obs.OI.Status != options.RatioMissing {
		t.Errorf("OI status = %s, want MISSING", obs.OI.Status)
	}
	if !hasWarning(obs.OI.Warnings, options.WarnSettlementFeedMissing) {
		t.Errorf("no %s warning: %+v", options.WarnSettlementFeedMissing, obs.OI.Warnings)
	}
	if !obs.Volume.Ratio.Observed {
		t.Error("the VOLUME half went missing too — the two have different dependencies and " +
			"volume does not need the settlement feed")
	}
}

// ── zero, absent, and the denominator ─────────────────────────────────────────────────

// TestTheZeroAndAbsentTable is §10.8's five lines, each on rows built to isolate it.
func TestTheZeroAndAbsentTable(t *testing.T) {
	const expiry = "202609"

	t.Run("observed zero numerator against a positive denominator is an OBSERVED ZERO", func(t *testing.T) {
		rows := pair(provider.SessionDay, expiry, 46000, fptr(500), fptr(0), fptr(500), fptr(0))
		obs := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay), rows)
		v, ok := obs.Volume.Value()
		if !ok {
			t.Fatalf("no ratio: %s (%s)", obs.Volume.Status, obs.Volume.Ratio.Reason)
		}
		if v != 0 {
			t.Errorf("ratio = %v, want 0", v)
		}
		if !obs.Volume.Ratio.IsObservedZero() {
			t.Error("a real 0 does not report as an observed zero, so a caller cannot tell " +
				"it from an absence")
		}
		if !obs.Volume.Numerator.IsObservedZero() {
			t.Error("the numerator does not report as an observed zero")
		}
	})

	t.Run("observed zero denominator is ZERO_DENOMINATOR and not infinity", func(t *testing.T) {
		rows := pair(provider.SessionDay, expiry, 46000, fptr(0), fptr(500), fptr(0), fptr(500))
		obs := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay), rows)
		if obs.Volume.Ratio.Observed {
			v, _ := obs.Volume.Value()
			t.Fatalf("a ratio of %v was published over a zero denominator", v)
		}
		if obs.Volume.Status != options.RatioZeroDenominator {
			t.Errorf("status = %s, want ZERO_DENOMINATOR", obs.Volume.Status)
		}
		if !obs.Volume.Denominator.IsObservedZero() {
			t.Error("the denominator does not report as an observed zero, so ZERO_DENOMINATOR " +
				"and INSUFFICIENT_DATA would be indistinguishable")
		}
		if !hasWarning(obs.Volume.Warnings, options.WarnZeroDenominator) {
			t.Errorf("no %s warning: %+v", options.WarnZeroDenominator, obs.Volume.Warnings)
		}
	})

	t.Run("absent denominator is INSUFFICIENT_DATA, not zero", func(t *testing.T) {
		rows := pair(provider.SessionDay, expiry, 46000, nil, fptr(500), nil, fptr(500))
		obs := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay), rows)
		if obs.Volume.Ratio.Observed {
			t.Fatal("a ratio was published over an absent denominator")
		}
		if obs.Volume.Status != options.RatioInsufficientData {
			t.Errorf("status = %s, want INSUFFICIENT_DATA", obs.Volume.Status)
		}
		if obs.Volume.Denominator.Observed {
			t.Error("the denominator reports a number — \"-\" was read as 0")
		}
	})

	t.Run("absent numerator leaves the ratio absent", func(t *testing.T) {
		rows := pair(provider.SessionDay, expiry, 46000, fptr(500), nil, fptr(500), nil)
		obs := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay), rows)
		if obs.Volume.Ratio.Observed {
			v, _ := obs.Volume.Value()
			t.Fatalf("a ratio of %v was published over an absent numerator — an absent put "+
				"side treated as 0 reports \"no puts traded\", which is a claim nobody made", v)
		}
		if obs.Volume.Status != options.RatioInsufficientData {
			t.Errorf("status = %s, want INSUFFICIENT_DATA", obs.Volume.Status)
		}
		if obs.Volume.Numerator.Observed {
			t.Error("the numerator reports a number")
		}
		// The denominator is still published: §10.8 forbids the ratio alone, and half an
		// aggregate is still evidence.
		assertLots(t, "denominator", obs.Volume.Denominator, 500)
	})
}

// TestOneSidedSelectionsArePartial: call-only and put-only, both directions.
func TestOneSidedSelectionsArePartial(t *testing.T) {
	const expiry = "202609"
	for _, c := range []struct {
		name string
		rows []provider.OptionStrikeRow
	}{
		{"call only", []provider.OptionStrikeRow{
			row(provider.SessionDay, expiry, 46000, provider.CallSide, fptr(500), fptr(400)),
		}},
		{"put only", []provider.OptionStrikeRow{
			row(provider.SessionDay, expiry, 46000, provider.PutSide, fptr(500), fptr(400)),
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			obs := mustObserve(t, req(options.FamilyMonthly, provider.SessionDay), c.rows)
			for _, p := range []options.PCR{obs.Volume.PCR, obs.OI.PCR} {
				if p.Ratio.Observed {
					v, _ := p.Value()
					t.Fatalf("%s published a ratio of %v from one side; the missing side is "+
						"never inferred from the present one", p.Metric, v)
				}
				if p.Status != options.RatioPartial {
					t.Errorf("%s status = %s, want PARTIAL", p.Metric, p.Status)
				}
				if !hasWarning(p.Warnings, options.WarnOneSideOnly) {
					t.Errorf("%s carries no %s warning: %+v", p.Metric,
						options.WarnOneSideOnly, p.Warnings)
				}
			}
		})
	}
}

// TestNoInfinityAndNoNaNAnywhere walks every shape of input §10.8 names and asserts the whole
// serialised result is free of both — not just the ratio field.
//
// JSON is the check rather than the struct because that is where an Inf actually escapes: Go's
// encoder REFUSES to marshal one, so a value that reached this far would break the archive and
// the report at once.
func TestNoInfinityAndNoNaNAnywhere(t *testing.T) {
	const expiry = "202609"
	cases := map[string][]provider.OptionStrikeRow{
		"zero denominator":   pair(provider.SessionDay, expiry, 46000, fptr(0), fptr(9), fptr(0), fptr(9)),
		"zero both sides":    pair(provider.SessionDay, expiry, 46000, fptr(0), fptr(0), fptr(0), fptr(0)),
		"absent both sides":  pair(provider.SessionDay, expiry, 46000, nil, nil, nil, nil),
		"absent denominator": pair(provider.SessionDay, expiry, 46000, nil, fptr(9), nil, fptr(9)),
		"absent numerator":   pair(provider.SessionDay, expiry, 46000, fptr(9), nil, fptr(9), nil),
		"no rows at all":     nil,
		"live session":       fixtureRows(t),
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			obs, err := options.NewObservation(req(options.FamilyAll, provider.SessionDay), rows)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []options.PCR{obs.Volume.PCR, obs.OI.PCR} {
				if v, ok := p.Value(); ok && (math.IsInf(v, 0) || math.IsNaN(v)) {
					t.Fatalf("%s produced %v", p.Metric, v)
				}
			}
			// json.Marshal REFUSES a non-finite float, so a successful encode is itself
			// the proof for every numeric field on the value, including the ones behind
			// pointers. It is also the concrete failure: a +Inf that got this far would
			// break the archive write and the report render at once.
			if _, err := json.Marshal(obs); err != nil {
				t.Fatalf("the observation will not serialise, which is what a non-finite "+
					"value does to the archive: %v", err)
			}
			// And again over the Go value, because a field with json:"-" or an unexported
			// one would be invisible to the encoder and still reach a caller.
			assertFinite(t, "", reflect.ValueOf(obs))
		})
	}
}

// TestARatioIsNeverAPercentage: 0.94, not 94.17, and no percent sign on a PCR anywhere.
func TestARatioIsNeverAPercentage(t *testing.T) {
	obs := mustObserve(t, req(options.FamilyAll, provider.SessionCombined), fixtureRows(t))
	v, ok := obs.Volume.Value()
	if !ok {
		t.Fatal("no volume ratio")
	}
	if v < 0.5 || v > 1.5 {
		t.Fatalf("the volume PCR is %v; the official figure for the session is 94.17%%, so a "+
			"value near 94 means the x100 happened in the domain", v)
	}
	if strings.Contains(obs.Volume.Ratio.Display, "%") {
		t.Errorf("the ratio renders as %q — the x100 is a presentation choice and happens "+
			"once, at the boundary", obs.Volume.Ratio.Display)
	}
	if obs.Volume.Ratio.Scale != options.RatioScale {
		t.Errorf("scale = %q, want %q", obs.Volume.Ratio.Scale, options.RatioScale)
	}
}

// TestARatioNeverTravelsAlone: §10.8 requires the numerator, the denominator and the
// provenance beside it, because a 1.05 built from 21/20 is not the evidence a 1.05 built from
// 42,000/40,000 is.
func TestARatioNeverTravelsAlone(t *testing.T) {
	r := req(options.FamilyAll, provider.SessionDay)
	r.Snapshots = []options.SnapshotRef{{Session: provider.SessionDay, SnapshotID: 42, Revision: 3}}
	obs := mustObserve(t, r, fixtureRows(t))
	p := obs.OI.PCR

	if _, ok := p.Numerator.Lots(); !ok {
		t.Error("no numerator")
	}
	if _, ok := p.Denominator.Lots(); !ok {
		t.Error("no denominator")
	}
	if p.TradingDate != tradingDate || p.AsOf != tradingDate {
		t.Errorf("trading date %q / as-of %q", p.TradingDate, p.AsOf)
	}
	if p.Key.Session != provider.SessionDay || p.Key.Family != options.FamilyAll {
		t.Errorf("key = %s", p.Key)
	}
	if len(p.Provenance.Snapshots) != 1 || p.Provenance.Snapshots[0].SnapshotID != 42 {
		t.Errorf("provenance carries %+v", p.Provenance.Snapshots)
	}
	if rev, agreed := p.Provenance.Revision(); rev != 3 || !agreed {
		t.Errorf("revision = %d (agreed %v), want 3", rev, agreed)
	}
	if p.Numerator.SnapshotID != 42 {
		t.Errorf("the numerator does not carry its snapshot id: %d", p.Numerator.SnapshotID)
	}
	if p.Coverage.RowsSeen == 0 || len(p.Coverage.ExpectedContracts) == 0 {
		t.Errorf("coverage is empty: %+v", p.Coverage)
	}
	if p.FeatureVersion != options.FeatureVersion {
		t.Errorf("feature version %q", p.FeatureVersion)
	}
}

// TestAPartialSnapshotIsNotPublished — §6.2: an aggregate whose correctness depends on
// completeness may not be published from one, and a PCR over most of the rows would reconcile
// against nothing.
func TestAPartialSnapshotIsNotPublished(t *testing.T) {
	r := req(options.FamilyAll, provider.SessionDay)
	r.SourceStatus = institutional.StatusPartial
	r.RejectedRows = 3
	obs := mustObserve(t, r, fixtureRows(t))

	for _, p := range []options.PCR{obs.Volume.PCR, obs.OI.PCR} {
		if p.Ratio.Observed {
			t.Errorf("%s was published from a PARTIAL snapshot", p.Metric)
		}
		if p.Status != options.RatioPartial {
			t.Errorf("%s status = %s, want PARTIAL", p.Metric, p.Status)
		}
		if p.Coverage.RejectedRows != 3 {
			t.Errorf("%s coverage records %d rejected rows, want 3", p.Metric, p.Coverage.RejectedRows)
		}
	}
}

// TestANonUsableSnapshotKeepsItsOwnStatus: NO_SESSION, NOT_PUBLISHED, MISSING and ERROR imply
// different actions and must not collapse into one another (§6).
func TestANonUsableSnapshotKeepsItsOwnStatus(t *testing.T) {
	for _, status := range []institutional.MetricStatus{
		institutional.StatusNoSession, institutional.StatusNotPublished,
		institutional.StatusMissing, institutional.StatusError, institutional.StatusStale,
	} {
		t.Run(string(status), func(t *testing.T) {
			r := req(options.FamilyAll, provider.SessionDay)
			r.SourceStatus = status
			obs, err := options.NewObservation(r, nil)
			if err != nil {
				t.Fatal(err)
			}
			if obs.Volume.Status != options.RatioStatus(status) {
				t.Errorf("volume status = %s, want %s", obs.Volume.Status, status)
			}
			if obs.OI.Status != options.RatioStatus(status) {
				t.Errorf("OI status = %s, want %s", obs.OI.Status, status)
			}
			if obs.Volume.Ratio.Display == "0" || obs.Volume.Ratio.Display == "" {
				t.Errorf("display %q — a value with nothing behind it must never look like "+
					"a number", obs.Volume.Ratio.Display)
			}
		})
	}
}

// TestAStatusSayingNothingWasObservedCannotCarryRows.
func TestAStatusSayingNothingWasObservedCannotCarryRows(t *testing.T) {
	r := req(options.FamilyAll, provider.SessionDay)
	r.SourceStatus = institutional.StatusNoSession
	if _, err := options.NewObservation(r, fixtureRows(t)); err == nil {
		t.Fatal("a NO_SESSION observation accepted 270 in-scope rows")
	}
}

// TestCombinationAndUnresolvedCodes: a "/" code is one order across two expiries and is
// excluded from every family; an unresolved code is counted in ALL and in neither of the
// other two.
func TestCombinationAndUnresolvedCodes(t *testing.T) {
	rows := []provider.OptionStrikeRow{}
	rows = append(rows, pair(provider.SessionDay, "202609", 46000, fptr(10), fptr(10), fptr(10), fptr(10))...)
	rows = append(rows, pair(provider.SessionDay, "202609W2", 46000, fptr(20), fptr(20), fptr(20), fptr(20))...)
	rows = append(rows, pair(provider.SessionDay, "202609/202610", 46000, fptr(40), fptr(40), nil, nil)...)
	rows = append(rows, pair(provider.SessionDay, "SPREAD", 46000, fptr(80), fptr(80), fptr(80), fptr(80))...)

	all := mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows)
	put, _ := all.Volume.Numerator.Lots()
	if put != 10+20+80 {
		t.Errorf("ALL put volume = %v, want 110: the combination row must be excluded and "+
			"the unresolved one counted", put)
	}
	if all.Volume.Coverage.CombinationRows != 2 {
		t.Errorf("combination rows counted = %d, want 2", all.Volume.Coverage.CombinationRows)
	}
	if all.Volume.Coverage.UnresolvedRows != 2 {
		t.Errorf("unresolved rows counted = %d, want 2", all.Volume.Coverage.UnresolvedRows)
	}
	if all.Volume.Coverage.ClassificationComplete {
		t.Error("classification reports complete with two unresolved codes in it")
	}

	weekly := mustObserve(t, req(options.FamilyWeekly, provider.SessionDay), rows)
	wput, _ := weekly.Volume.Numerator.Lots()
	if wput != 20 {
		t.Errorf("WEEKLY put volume = %v, want 20 — an unresolved code cannot be filed under "+
			"a family whose name it does not carry", wput)
	}
}

// TestARowCarryingASecondClassifiersAnswerIsRejected.
//
// §3.1 allows exactly one classifier input: the expiry CODE. A row arriving pre-classified as
// something the code does not say means a second classifier exists — most plausibly one fed
// SettledPositionsIndexOptions' ContractDeliveryMonth, where the F1 WEEKLY appears as a bare
// 202609 and would come back MONTHLY.
func TestARowCarryingASecondClassifiersAnswerIsRejected(t *testing.T) {
	rows := pair(provider.SessionDay, "202609F1", 46000, fptr(1), fptr(1), fptr(1), fptr(1))
	rows[0].ExpiryKind = provider.ExpiryMonthly // what the TXU delivery month would have said
	if _, err := options.NewObservation(req(options.FamilyAll, provider.SessionDay), rows); err == nil {
		t.Fatal("a row classified MONTHLY under a weekly code was accepted; one contract, " +
			"two kinds, and M6's weekly/monthly split would depend on fetch order")
	}
}

// ── coverage ──────────────────────────────────────────────────────────────────────────

// TestCoverageAnswersTheQuestionsItIsAskedTo: expected vs actual contracts, expected vs actual
// call/put pairs, rejected rows, session availability, classification completeness.
func TestCoverageAnswersTheQuestionsItIsAskedTo(t *testing.T) {
	rows := fixtureRows(t)
	day := mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows)
	night := mustObserve(t, req(options.FamilyAll, provider.SessionAfterHours), rows)

	c := day.OI.Coverage
	if len(c.ExpectedContracts) == 0 || len(c.ActualContracts) == 0 {
		t.Fatalf("day open interest coverage: %+v", c)
	}
	if c.ContractCompleteness() != 1 {
		t.Errorf("day open-interest contract completeness = %v with missing %v",
			c.ContractCompleteness(), c.MissingContracts)
	}
	if c.ExpectedPairs == 0 || c.ActualPairs == 0 {
		t.Errorf("no call/put pairs counted: %+v", c)
	}
	if c.ActualPairs > c.ExpectedPairs {
		t.Errorf("more paired points (%d) than points (%d)", c.ActualPairs, c.ExpectedPairs)
	}
	if !c.SessionsComplete() {
		t.Errorf("the DAY figure reports missing sessions %v", c.SessionsMissing)
	}
	if !c.ClassificationComplete {
		t.Errorf("the fixture's TXO rows classified incompletely: %d unresolved", c.UnresolvedRows)
	}
	if c.CallRows == 0 || c.PutRows == 0 {
		t.Errorf("sides not counted: %+v", c)
	}

	n := night.OI.Coverage
	if len(n.ExpectedContracts) == 0 {
		t.Error("the night open-interest coverage expected no contracts at all")
	}
	if len(n.ActualContracts) != 0 || n.ContractCompleteness() != 0 {
		t.Errorf("night open interest reports contributing contracts %v", n.ActualContracts)
	}
	if n.ActualPairs != 0 {
		t.Errorf("night open interest reports %d paired points", n.ActualPairs)
	}
	if len(n.MissingContracts) == 0 {
		t.Error("no contracts named as missing, so the absence is invisible in the record")
	}
	if !contains(collectReasons(day.OI.Coverage.Excluded), options.ExcludedSettlingExpiry) {
		t.Errorf("the settling expiry is not among the day's open-interest exclusions: %+v",
			day.OI.Coverage.Excluded)
	}
	if contains(collectReasons(day.Volume.Coverage.Excluded), options.ExcludedSettlingExpiry) {
		t.Error("volume excluded the settling expiry")
	}
	if !contains(collectReasons(day.Volume.Coverage.Excluded), options.ExcludedOtherProduct) {
		t.Errorf("the TEO/CAO rows were not recorded as out of scope: %+v",
			day.Volume.Coverage.Excluded)
	}
}

// ── the reconciliation gate (§10.1) ───────────────────────────────────────────────────

// TestTheReconciliationGate: the computed figures against the exchange's own.
func TestTheReconciliationGate(t *testing.T) {
	rows := fixtureRows(t)
	official := officialRatio(t, "put_call_ratio_trimmed_reconcile_20260904.json")

	vol := options.Reconcile(
		mustObserve(t, req(options.FamilyAll, provider.SessionCombined), rows).Volume.PCR, official)
	if vol.Reconciliation.Status != options.ReconciledMatched {
		t.Errorf("volume reconciliation = %s (%s)", vol.Reconciliation.Status, vol.Reconciliation.Reason)
	}
	oi := options.Reconcile(
		mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows).OI.PCR, official)
	if oi.Reconciliation.Status != options.ReconciledMatched {
		t.Errorf("open-interest reconciliation = %s (%s)", oi.Reconciliation.Status, oi.Reconciliation.Reason)
	}
	if !oi.Ratio.Observed {
		t.Error("a matched aggregate lost its ratio")
	}

	t.Run("a mismatch publishes no number", func(t *testing.T) {
		wrong := *official
		wrong.PutOI = fptr(999999)
		got := options.Reconcile(
			mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows).OI.PCR, &wrong)
		if got.Ratio.Observed {
			t.Fatal("a ratio survived a reconciliation mismatch")
		}
		if got.Status != options.RatioError || got.Reconciliation.Status != options.ReconciledMismatch {
			t.Errorf("status %s / reconciliation %s", got.Status, got.Reconciliation.Status)
		}
		if got.Reconciliation.OfficialPut == nil || got.Reconciliation.ComputedPut == nil {
			t.Error("the discrepancy was not recorded, so §3.1's record of what disagreed is gone")
		}
	})

	t.Run("no official figure removes the open-interest backstop", func(t *testing.T) {
		gotOI := options.Reconcile(
			mustObserve(t, req(options.FamilyAll, provider.SessionDay), rows).OI.PCR, nil)
		if gotOI.Ratio.Observed {
			t.Fatal("an unchecked open-interest aggregate was published; an unchecked number " +
				"that happens to be right is indistinguishable from one that happens to be wrong")
		}
		if gotOI.Status != options.RatioMissing {
			t.Errorf("status = %s, want MISSING", gotOI.Status)
		}
		gotVol := options.Reconcile(
			mustObserve(t, req(options.FamilyAll, provider.SessionCombined), rows).Volume.PCR, nil)
		if !gotVol.Ratio.Observed {
			t.Error("the volume half went missing too — §3.1 makes the MISSING ruling for " +
				"the open-interest aggregate, whose other dependency is the settlement feed")
		}
		if gotVol.Reconciliation.Status != options.ReconciledUnavailable {
			t.Errorf("volume reconciliation = %s", gotVol.Reconciliation.Status)
		}
	})

	t.Run("a family the exchange does not publish is NOT_APPLICABLE", func(t *testing.T) {
		got := options.Reconcile(
			mustObserve(t, req(options.FamilyWeekly, provider.SessionDay), rows).OI.PCR, official)
		if got.Reconciliation.Status != options.ReconciledNotApplicable {
			t.Errorf("weekly reconciliation = %s", got.Reconciliation.Status)
		}
		if !got.Ratio.Observed {
			t.Error("the weekly ratio was withdrawn for want of a figure that does not exist")
		}
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────────────

func assertLots(t *testing.T, what string, m institutional.ObservedMetric, want float64) {
	t.Helper()
	got, ok := m.Lots()
	if !ok {
		t.Fatalf("%s is absent (%s: %s), want %v", what, m.Status, m.Reason, want)
	}
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func assertRatio(t *testing.T, what string, r options.ObservedRatio, want float64) {
	t.Helper()
	got, ok := r.Value()
	if !ok {
		t.Fatalf("%s is absent (%s: %s), want %v", what, r.Status, r.Reason, want)
	}
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func mustValue(t *testing.T, p options.VolumePCR) float64 {
	t.Helper()
	v, ok := p.Value()
	if !ok {
		t.Fatalf("%s has no ratio: %s", p.Metric, p.Ratio.Reason)
	}
	return v
}

func hasWarning(ws []options.Warning, code string) bool {
	for _, w := range ws {
		if w.Code == code {
			return true
		}
	}
	return false
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func collectReasons(ex []options.ExcludedRows) []string {
	out := make([]string, 0, len(ex))
	for _, e := range ex {
		out = append(out, e.Reason)
	}
	return out
}

// assertFinite walks a value and fails on any float that is not a number.
func assertFinite(t *testing.T, path string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			t.Errorf("%s is %v", path, f)
		}
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			assertFinite(t, path, v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			assertFinite(t, path+"."+v.Type().Field(i).Name, v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			assertFinite(t, fmt.Sprintf("%s[%d]", path, i), v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			assertFinite(t, fmt.Sprintf("%s[%v]", path, k), v.MapIndex(k))
		}
	}
}
