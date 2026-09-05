package healthcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// FU-11 end to end, and above all the proof that the four layers have NOT collapsed into one.
//
//	Availability      can the distribution be computed?
//	Data Quality      is the evidence sufficient?           (FU-9)
//	Dispersion        how wide is it?                       (FU-9, descriptive)
//	Model Suitability is P/E the right lens at all?         (FU-11)
//
// A mapping table between any two of these would produce verdicts that all look reasonable
// while adding nothing. The combination tests below are the ones that would catch it.

// suitService builds a service and returns the valuation and fundamental archive directories.
func suitService(t *testing.T, today string) (*Service, string, string) {
	t.Helper()
	s, root := fundService(t, today)
	return s, filepath.Join(root, "valuation"), filepath.Join(root, "fundamental")
}

// filedProfit / filedLoss write the most recent financial statement.
func filedProfit(t *testing.T, dir, observed string) {
	t.Helper()
	writeFund(t, dir, fundOpts{observed: observed, year: 2026, quarter: 2, eps: f64(4.82),
		revenue: 1_000_000, gross: 400_000, operating: 300_000, net: 250_000})
}

func filedLoss(t *testing.T, dir, observed string) {
	t.Helper()
	writeFund(t, dir, fundOpts{observed: observed, year: 2026, quarter: 2, eps: f64(-0.03),
		revenue: 1_000_000, gross: 100_000, operating: -5_000, net: -7_061})
}

func suitabilityOf(t *testing.T, s *Service, asOf string) SuitabilityBlock {
	t.Helper()
	return check(t, s, Request{Symbol: "2330", AsOf: asOf}).Valuation.ModelSuitability
}

func hasCode(b SuitabilityBlock, code valuation.SuitabilityReason) bool {
	for _, r := range b.Reasons {
		if r == string(code) {
			return true
		}
	}
	return false
}

// ── A. layer separation ───────────────────────────────────────────────────────────────

// Every interesting combination must be REACHABLE. If any of these cannot be produced, the
// two layers have become one under a different name.
func TestQualityAndSuitabilityAreIndependentlyReachable(t *testing.T) {
	for _, tc := range []struct {
		name string
		// sessions archived, of which the last `valid` carry a P/E
		sessions, valid int
		loss            bool
		wantQuality     valuation.Quality
		wantSuitability valuation.Suitability
	}{
		{"HIGH quality, SUITABLE model", 225, 225, false,
			valuation.QualityHigh, valuation.SuitabilitySuitable},
		// The one that proves most: excellent evidence, and a denominator that only turned
		// positive partway through the window.
		{"HIGH quality, WEAK model", 250, 190, false,
			valuation.QualityHigh, valuation.SuitabilityWeak},
		// Thin evidence, and nothing wrong with the model beyond how briefly it was watched.
		{"LOW quality, CONDITIONAL model", 25, 25, false,
			valuation.QualityLow, valuation.SuitabilityConditional},
		{"INSUFFICIENT quality, UNSUITABLE model", 225, 0, true,
			valuation.QualityInsufficient, valuation.SuitabilityUnsuitable},
		{"INSUFFICIENT quality, INSUFFICIENT_DATA model", 225, 0, false,
			valuation.QualityInsufficient, valuation.SuitabilityInsufficientData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, vdir, fdir := suitService(t, "2026-09-05")
			writeMixedSessions(t, vdir, tc.sessions, tc.valid, "2026-09-05")
			if tc.loss {
				filedLoss(t, fdir, "2026-09-04")
			} else if tc.valid > 0 {
				filedProfit(t, fdir, "2026-09-04")
			}

			h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})
			gotQ := h.Valuation.Historical.Quality.Quality
			gotS := h.Valuation.ModelSuitability.Suitability

			if gotQ != string(tc.wantQuality) || gotS != string(tc.wantSuitability) {
				t.Fatalf("quality/suitability = %s/%s, want %s/%s\n  valid %d of %d, %v",
					gotQ, gotS, tc.wantQuality, tc.wantSuitability,
					h.Valuation.Historical.Quality.ValidSamples,
					h.Valuation.Historical.Quality.WindowSessions,
					h.Valuation.ModelSuitability.Reasons)
			}
		})
	}
}

// The same data quality, two different verdicts. If quality determined suitability, this
// could not happen.
func TestOneQualityGradeYieldsDifferentVerdicts(t *testing.T) {
	stable, sdir, sfund := suitService(t, "2026-09-05")
	writeMixedSessions(t, sdir, 225, 225, "2026-09-05")
	filedProfit(t, sfund, "2026-09-04")

	turned, tdir, tfund := suitService(t, "2026-09-05")
	writeMixedSessions(t, tdir, 250, 190, "2026-09-05")
	filedProfit(t, tfund, "2026-09-04")

	a := check(t, stable, Request{Symbol: "2330", AsOf: "2026-09-05"})
	b := check(t, turned, Request{Symbol: "2330", AsOf: "2026-09-05"})

	if a.Valuation.Historical.Quality.Quality != b.Valuation.Historical.Quality.Quality {
		t.Fatalf("the fixtures differ in quality (%s vs %s); the test needs them equal",
			a.Valuation.Historical.Quality.Quality, b.Valuation.Historical.Quality.Quality)
	}
	if a.Valuation.ModelSuitability.Suitability == b.Valuation.ModelSuitability.Suitability {
		t.Fatalf("both graded %s at quality %s — suitability is tracking data quality "+
			"rather than the earnings denominator",
			a.Valuation.ModelSuitability.Suitability, a.Valuation.Historical.Quality.Quality)
	}
}

// ── D / E. the two shapes the spec names ──────────────────────────────────────────────

// 2337 旺宏: 26 usable P/E values in a 225-session window, all after every gap.
func TestSparseRecentPEIsRecognisedAsARecentEarningsRegime(t *testing.T) {
	s, vdir, fdir := suitService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 26, "2026-09-05")
	filedProfit(t, fdir, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})
	ms := h.Valuation.ModelSuitability

	if ms.Suitability != string(valuation.SuitabilityWeak) {
		t.Fatalf("suitability = %s, want WEAK (%v)", ms.Suitability, ms.Reasons)
	}
	if !hasCode(ms, valuation.ReasonPEAvailableOnlyRecently) {
		t.Fatalf("reasons = %v, want PE_AVAILABLE_ONLY_RECENTLY", ms.Reasons)
	}
	if ms.PEPersistence != string(valuation.PERecentOnly) {
		t.Fatalf("persistence = %s, want RECENT_ONLY", ms.PEPersistence)
	}
	// The target price is available, and stays available. That pairing is the point.
	if h.Target.Status != StatusAvailable {
		t.Fatalf("target = %s, want AVAILABLE beside a WEAK verdict", h.Target.Status)
	}
	if len(ms.Notes) == 0 {
		t.Fatal("a WEAK verdict with no wording gives a reader nothing to act on")
	}
}

// 3055 蔚華科 with its filing: 225 archived sessions, no P/E, and a statement showing a loss.
// Two independent sources agreeing is what licenses UNSUITABLE.
func TestNoPEPlusAFiledLossIsUnsuitable(t *testing.T) {
	s, vdir, fdir := suitService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 0, "2026-09-05")
	filedLoss(t, fdir, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})
	ms := h.Valuation.ModelSuitability

	if ms.Suitability != string(valuation.SuitabilityUnsuitable) {
		t.Fatalf("suitability = %s, want UNSUITABLE (%v)", ms.Suitability, ms.Reasons)
	}
	if !hasCode(ms, valuation.ReasonNoPositiveTrailing) {
		t.Fatalf("reasons = %v, want NO_POSITIVE_TRAILING_EARNINGS", ms.Reasons)
	}
	if ms.EarningsSign != string(valuation.EarningsNonPositive) {
		t.Fatalf("earnings sign = %s, want NON_POSITIVE", ms.EarningsSign)
	}
}

// The same archive with NO filing must not reach the same conclusion. This is the line
// between reading evidence and inventing it.
func TestNoPEWithoutAFilingIsInsufficientNotUnsuitable(t *testing.T) {
	s, vdir, _ := suitService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 0, "2026-09-05")

	ms := suitabilityOf(t, s, "2026-09-05")

	if ms.Suitability != string(valuation.SuitabilityInsufficientData) {
		t.Fatalf("suitability = %s, want INSUFFICIENT_DATA — with no financial statement, "+
			"an absent P/E is consistent with a suspension or a dataset gap, and calling it "+
			"a loss invents a fact (%v)", ms.Suitability, ms.Reasons)
	}
	if hasCode(ms, valuation.ReasonNoPositiveTrailing) {
		t.Fatalf("reasons = %v — a loss was claimed with no filing behind it", ms.Reasons)
	}
	if ms.EarningsSign != string(valuation.EarningsUnknown) {
		t.Fatalf("earnings sign = %s, want UNKNOWN", ms.EarningsSign)
	}
}

// A filing whose EPS and net income disagree about the sign means one of them was misread.
// A sign taken from a misparsed field is worse than no sign.
func TestADisagreeingFilingYieldsAnUnknownSign(t *testing.T) {
	s, vdir, fdir := suitService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 225, "2026-09-05")
	writeFund(t, fdir, fundOpts{observed: "2026-09-04", year: 2026, quarter: 2,
		eps: f64(4.82), revenue: 1_000_000, gross: 100_000, operating: -5_000, net: -7_061})

	ms := suitabilityOf(t, s, "2026-09-05")
	if ms.EarningsSign != string(valuation.EarningsUnknown) {
		t.Fatalf("earnings sign = %s, want UNKNOWN — a positive EPS with a negative net "+
			"income cannot both be right", ms.EarningsSign)
	}
}

// ── B / C. what must not decide a verdict, through the real service ───────────────────

// 3037 欣興: 225 of 225 sessions, a median far above any plausible "normal" multiple.
func TestAHighMedianPERatioStaysSuitable(t *testing.T) {
	s, vdir, fdir := suitService(t, "2026-09-05")
	writeHighPESessions(t, vdir, 225, 120, "2026-09-05")
	filedProfit(t, fdir, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})
	if h.Valuation.ModelSuitability.Suitability != string(valuation.SuitabilitySuitable) {
		t.Fatalf("suitability = %s with a median around 120x — FU-11 judges the earnings "+
			"denominator, and a multiple is a property of the numerator",
			h.Valuation.ModelSuitability.Suitability)
	}
}

// 6274 台燿: a re-rating captured correctly. It earns a caution, not a downgrade.
func TestAWideDistributionStaysSuitableAndOnlyEarnsACaution(t *testing.T) {
	s, vdir, fdir := suitService(t, "2026-09-05")
	writeWideSessions(t, vdir, 225, "2026-09-05")
	filedProfit(t, fdir, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})
	ms := h.Valuation.ModelSuitability

	if ms.Suitability != string(valuation.SuitabilitySuitable) {
		t.Fatalf("suitability = %s — a wide distribution is a re-rating, not a defect",
			ms.Suitability)
	}
	if !hasCode(ms, valuation.ReasonValuationRegimeWide) {
		t.Fatalf("reasons = %v, want the VALUATION_REGIME_WIDE caution", ms.Reasons)
	}
}

// ── F. target independence ────────────────────────────────────────────────────────────

// A WEAK verdict must leave the target price, its rule, its sample count and every scenario
// exactly as they were.
func TestAWeakVerdictLeavesTheTargetPriceIntact(t *testing.T) {
	weak, wdir, wfund := suitService(t, "2026-09-05")
	writeMixedSessions(t, wdir, 225, 26, "2026-09-05")
	filedProfit(t, wfund, "2026-09-04")
	w := check(t, weak, Request{Symbol: "2330", AsOf: "2026-09-05"})

	if w.Valuation.ModelSuitability.Suitability != string(valuation.SuitabilityWeak) {
		t.Fatalf("fixture is not WEAK: %s", w.Valuation.ModelSuitability.Suitability)
	}
	if w.Target.Status != StatusAvailable {
		t.Fatalf("target = %s (%s), want AVAILABLE", w.Target.Status, w.Target.Reason)
	}
	if w.Target.Rule != valuation.RuleHistoricalPercentile {
		t.Fatalf("target rule = %s", w.Target.Rule)
	}
	if w.Target.SampleCount != 26 {
		t.Fatalf("target sample count = %d, want 26", w.Target.SampleCount)
	}
	for _, sc := range w.Target.Scenarios {
		if sc.Status != StatusAvailable {
			t.Fatalf("scenario %s = %s", sc.Name, sc.Status)
		}
	}
}

// The strongest form: the SAME distribution under two different verdicts must produce
// byte-identical target arithmetic. Only the filing differs between these two fixtures.
func TestTheVerdictNeverAltersTheTargetArithmetic(t *testing.T) {
	a, adir, afund := suitService(t, "2026-09-05")
	writeMixedSessions(t, adir, 225, 225, "2026-09-05")
	filedProfit(t, afund, "2026-09-04")
	x := check(t, a, Request{Symbol: "2330", AsOf: "2026-09-05"})

	b, bdir, bfund := suitService(t, "2026-09-05")
	writeMixedSessions(t, bdir, 225, 225, "2026-09-05")
	filedLoss(t, bfund, "2026-09-04") // same P/E history, a filing that turned negative
	y := check(t, b, Request{Symbol: "2330", AsOf: "2026-09-05"})

	if x.Valuation.ModelSuitability.Suitability == y.Valuation.ModelSuitability.Suitability {
		t.Fatalf("both verdicts are %s; the fixtures must differ for this to mean anything",
			x.Valuation.ModelSuitability.Suitability)
	}
	if x.Target.Status != y.Target.Status || x.Target.Rule != y.Target.Rule ||
		x.Target.EPS.Display != y.Target.EPS.Display ||
		x.Target.MarginOfSafety.Display != y.Target.MarginOfSafety.Display {
		t.Fatalf("the target moved with the verdict:\n  %s: %s %s eps %s mos %s\n"+
			"  %s: %s %s eps %s mos %s",
			x.Valuation.ModelSuitability.Suitability, x.Target.Status, x.Target.Rule,
			x.Target.EPS.Display, x.Target.MarginOfSafety.Display,
			y.Valuation.ModelSuitability.Suitability, y.Target.Status, y.Target.Rule,
			y.Target.EPS.Display, y.Target.MarginOfSafety.Display)
	}
	// And the FU-9 layer is untouched too.
	if x.Valuation.Historical.Quality.Quality != y.Valuation.Historical.Quality.Quality {
		t.Fatalf("the data quality grade moved with the verdict: %s vs %s",
			x.Valuation.Historical.Quality.Quality, y.Valuation.Historical.Quality.Quality)
	}
}

// ── versioning and persistence ────────────────────────────────────────────────────────

func TestVerdictsArePersistedWithTheirVersionAndReasonCodes(t *testing.T) {
	s, vdir, fdir := suitService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 26, "2026-09-05")
	filedProfit(t, fdir, "2026-09-04")
	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-05"})

	got := map[string]string{}
	for _, r := range evidenceRows(h) {
		if r.TextValue != nil {
			got[r.Category+"/"+r.Key] = *r.TextValue
		}
	}
	if got["valuation/model_suitability"] != string(valuation.SuitabilityWeak) {
		t.Errorf("model_suitability = %q", got["valuation/model_suitability"])
	}
	if got["valuation/model_suitability_rule_version"] != valuation.SuitabilityRuleVersion {
		t.Errorf("rule version = %q, want %q",
			got["valuation/model_suitability_rule_version"], valuation.SuitabilityRuleVersion)
	}
	// The canonical CODES, not the wording — a stored conclusion has to stay
	// machine-readable after the prose is reworded.
	codes := got["valuation/model_suitability_reasons"]
	if !strings.Contains(codes, string(valuation.ReasonPEAvailableOnlyRecently)) {
		t.Errorf("reasons = %q, want the canonical code", codes)
	}
	if got["valuation/earnings_sign"] == "" || got["valuation/pe_persistence"] == "" {
		t.Errorf("the two evidence axes were not persisted: %q / %q",
			got["valuation/earnings_sign"], got["valuation/pe_persistence"])
	}
}

func TestEveryVerdictReachesTheBlockWithItsRuleVersion(t *testing.T) {
	s, vdir, _ := suitService(t, "2026-09-05")
	writeMixedSessions(t, vdir, 225, 225, "2026-09-05")

	ms := suitabilityOf(t, s, "2026-09-05")
	if ms.RuleVersion != valuation.SuitabilityRuleVersion {
		t.Fatalf("rule version = %q", ms.RuleVersion)
	}
	if !strings.Contains(ms.Summary, ms.RuleVersion) {
		t.Fatalf("the summary does not name the rule version: %q", ms.Summary)
	}
}

// ── the rendered wording is part of the contract ──────────────────────────────────────
//
// The reason CODE was renamed away from STABLE_POSITIVE_EARNINGS because the evidence is a
// sign-state history and cannot establish stability. The Chinese sentence a reader actually
// sees is where that claim would come back — nobody greps prose, and a note saying 盈餘穩定
// beside a correctly-named code is the overclaim restored where it does the most damage.
//
// A rendered note is not decoration. It is what the conclusion means to the person acting on
// it, and it is the only part of this layer most readers will ever see.
//
// # Driven by the reason table, not by sampled inputs
//
// The first version of this test swept eight hand-written inputs and its comment claimed to
// cover "every verdict this layer can produce". It covered 11 of 14 notes. The three it missed
// were the ones needing a caution input or a short NEVER window — and two of them,
// VALUATION_REGIME_WIDE and TARGET_SENSITIVITY_REVIEW, are on 6274 台燿 and 1815 富喬 in
// production right now. A guard that samples is a guard with holes exactly where the inputs
// were not imagined, and it is worse than none, because its comment invites trust.

// stabilityClaims is vocabulary asserting something about earnings MAGNITUDE or its
// steadiness. FU11-v1 observes only which side of zero the denominator was on.
var stabilityClaims = []string{"盈餘穩定", "穩定的盈餘", "獲利穩定", "穩定成長",
	"盈餘持續成長", "盈餘穩健", "stable earnings", "earnings are stable"}

// Every reason code has wording, and no wording claims stability. Exhaustive over the code
// table rather than over inputs.
func TestEveryReasonNoteIsWordedAndClaimsNoStability(t *testing.T) {
	for _, r := range valuation.AllSuitabilityReasons {
		note := reasonNote(r)
		if note == "" {
			t.Errorf("%s has no wording; it would reach a reader as a bare code", r)
			continue
		}
		for _, bad := range stabilityClaims {
			if strings.Contains(note, bad) {
				t.Errorf("%s renders %q, which claims earnings stability; the evidence is a "+
					"sign-state history and cannot establish it", r, note)
			}
		}
	}
}

// And the other direction, so the table cannot go stale: every reason the classifier actually
// produces must be in it. Swept exhaustively over the input space rather than sampled.
func TestEveryProducedReasonIsInTheDeclaredTable(t *testing.T) {
	declared := map[valuation.SuitabilityReason]bool{}
	for _, r := range valuation.AllSuitabilityReasons {
		declared[r] = true
	}
	wide, upside := 5.0, 500.0
	seen := map[valuation.SuitabilityReason]bool{}
	for _, sign := range []valuation.EarningsSign{valuation.EarningsPositive,
		valuation.EarningsNonPositive, valuation.EarningsUnknown, ""} {
		for _, p := range []valuation.PEPersistence{valuation.PEPersistent,
			valuation.PERecentOnly, valuation.PEIntermittent, valuation.PENever,
			valuation.PEUnknown, ""} {
			for _, w := range []int{0, 1, 30, 59, 60, 225} {
				for _, iqr := range []*float64{nil, &wide} {
					for _, up := range []*float64{nil, &upside} {
						v := valuation.ClassifySuitability(valuation.SuitabilityInput{
							Earnings: sign, Persistence: p, WindowSessions: w,
							RelativeIQR: iqr, BaseUpsidePct: up})
						for _, r := range v.Reasons {
							seen[r] = true
							if !declared[r] {
								t.Errorf("%s is produced but not in AllSuitabilityReasons, "+
									"so nothing checks its wording", r)
							}
						}
					}
				}
			}
		}
	}
	// The sweep must actually be reaching the whole table, or the check above is vacuous.
	for _, r := range valuation.AllSuitabilityReasons {
		if !seen[r] {
			t.Errorf("%s is declared but the exhaustive sweep never produced it; either it "+
				"is unreachable or the sweep is too narrow to be a check", r)
		}
	}
}

// The end-to-end form: the wording that actually reaches a rendered block, for every verdict
// the sweep can produce.
func TestRenderedNotesNeverClaimEarningsStability(t *testing.T) {
	wide, upside := 5.0, 500.0
	rendered := 0
	for _, sign := range []valuation.EarningsSign{valuation.EarningsPositive,
		valuation.EarningsNonPositive, valuation.EarningsUnknown} {
		for _, p := range []valuation.PEPersistence{valuation.PEPersistent,
			valuation.PERecentOnly, valuation.PEIntermittent, valuation.PENever,
			valuation.PEUnknown} {
			for _, w := range []int{0, 30, 225} {
				for _, iqr := range []*float64{nil, &wide} {
					for _, up := range []*float64{nil, &upside} {
						b := projectSuitability(valuation.ClassifySuitability(
							valuation.SuitabilityInput{Earnings: sign, Persistence: p,
								WindowSessions: w, RelativeIQR: iqr, BaseUpsidePct: up}))
						for _, txt := range append(append([]string{}, b.Notes...), b.Summary) {
							rendered++
							for _, bad := range stabilityClaims {
								if strings.Contains(txt, bad) {
									t.Fatalf("%s/%s renders %q", sign, p, txt)
								}
							}
						}
					}
				}
			}
		}
	}
	if rendered < 100 {
		t.Fatalf("only %d strings rendered; the sweep is too narrow to prove anything",
			rendered)
	}
}

// And the positive half: the SUITABLE verdict must SAY that the magnitude is unknown, in
// words, not only as a code.
func TestSuitableRendersTheMagnitudeCaveatInWords(t *testing.T) {
	b := projectSuitability(valuation.ClassifySuitability(valuation.SuitabilityInput{
		Earnings: valuation.EarningsPositive, Persistence: valuation.PEPersistent,
		WindowSessions: 225}))

	if b.Suitability != string(valuation.SuitabilitySuitable) {
		t.Fatalf("suitability = %s", b.Suitability)
	}
	joined := strings.Join(b.Notes, " / ")
	if !strings.Contains(joined, "金額") {
		t.Fatalf("the notes never mention the earnings MAGNITUDE limit, so a reader learns "+
			"it only from a reason code they will not look up: %s", joined)
	}
}
