package valuation

// Valuation MODEL SUITABILITY — whether trailing P/E is an appropriate primary anchor for
// this company right now.
//
// # The fourth layer, and why it cannot collapse into the other three
//
//	Availability      can the historical P/E distribution be computed?
//	Data Quality      is the evidence behind it sufficient?              (FU-9)
//	Dispersion        how wide is the distribution?                      (FU-9, descriptive)
//	Model Suitability is P/E the right lens for this company at all?     (here)
//
// The first three are all statements about the DATA. This one is a statement about the
// COMPANY, and the two can point in opposite directions: 3037 欣興 has 225 of 225 sessions and
// a median around 120x — flawless evidence — while 3055 蔚華科 has 225 archived sessions, not
// one published P/E, and a filed loss. Perfect data about a company P/E cannot describe, and
// no data about one it might.
//
// So suitability CONSUMES the FU-9 metrics as evidence and is never derived from the FU-9
// grade. There is no mapping table here, and the tests prove both of the interesting
// combinations are reachable: HIGH quality with WEAK suitability, LOW quality with
// CONDITIONAL suitability.
//
// # What it is not
//
//	UNSUITABLE  ≠  bad company
//	UNSUITABLE  ≠  overpriced
//	UNSUITABLE  ≠  HIGH_RISK
//	UNSUITABLE  ≠  SELL
//
// It says one thing: under current evidence, a P/E multiple is not the right primary anchor.
// Nothing here reaches the scanner, the entry assessment, or the target price.
//
// # The first principle: look at the denominator
//
//	PE = Price / Earnings
//
// The question is therefore NOT "is the P/E high", "is the distribution wide", or "is the
// implied upside large" — those are all properties of the numerator or of the output. It is
// whether the EARNINGS DENOMINATOR has enough economic stability to anchor anything. A high
// multiple can mean a depressed denominator, a cyclical trough, or a growth expectation, and
// this layer cannot tell those apart from the multiple alone. So it does not try: it looks at
// the earnings evidence directly, and a level or a width never decides a class.

// Suitability grades P/E as a primary valuation model.
type Suitability string

const (
	// SuitabilitySuitable — P/E is STRUCTURALLY USABLE under the observed positive-earnings
	// eligibility history: a positive basis is filed, and the exchange was able to compute a
	// P/E for every archived session in the window.
	//
	// Deliberately narrow. It does NOT claim the earnings denominator is stable, and FU11-v1
	// cannot establish that: the evidence is a sign-state history, so a company whose TTM EPS
	// fell 20 → 10 → 5 → 1 is SUITABLE throughout. Every SUITABLE verdict therefore carries
	// EARNINGS_MAGNITUDE_NOT_ESTABLISHED, so the limit travels with the conclusion instead of
	// living only in this comment.
	//
	// Establishing magnitude stability needs multi-period earnings, which needs a fundamental
	// archive with more than one filed period per stock. That is future work, not a threshold
	// change.
	SuitabilitySuitable Suitability = "SUITABLE"
	// SuitabilityConditional — P/E is usable as a reference, with a provable caveat about the
	// earnings basis or the observation window.
	SuitabilityConditional Suitability = "CONDITIONAL"
	// SuitabilityWeak — a P/E exists, but only because of a recent change in the earnings
	// denominator. It is arithmetically fine and economically thin as a long-run anchor.
	SuitabilityWeak Suitability = "WEAK"
	// SuitabilityUnsuitable — the evidence shows there is no usable positive earnings basis.
	SuitabilityUnsuitable Suitability = "UNSUITABLE"
	// SuitabilityInsufficientData — the evidence does not support a judgement either way.
	// Reached deliberately and often; guessing here is worse than declining.
	SuitabilityInsufficientData Suitability = "INSUFFICIENT_DATA"
)

// SuitabilityRuleVersion identifies the rules a verdict was produced under.
//
// HEURISTIC, NOT BACKTEST-FITTED — and there is very little to fit: the classification below
// turns on the STRUCTURE of the evidence (a sign, an ordering) rather than on cut-offs.
// Exactly one numeric threshold exists in this file, and it is derived from the reporting
// calendar rather than from outcomes. Bump this whenever a rule or a reason changes.
const SuitabilityRuleVersion = "FU11-v1"

// MinSuitabilityWindowSessions is the minimum observation horizon before a PATTERN claim is
// worth making — in either direction, unbroken or absent.
//
// Approximately one reporting cycle: Taiwanese companies file quarterly and ~21 trading
// sessions a month makes a quarter ≈ 60 sessions.
//
// It proves only that the window is LONG ENOUGH TO POTENTIALLY span one reporting cycle. It
// does not prove an earnings update actually occurred inside it — the filing dates that would
// establish that are not part of this input, and a window can straddle a cycle boundary
// without containing one. The weaker claim is the one the rules rely on, and the stronger one
// must not be written down anywhere as though it followed.
//
// HEURISTIC, NOT BACKTEST-FITTED.
//
// It is applied to WINDOW SESSIONS — how long this repo has been watching — and is therefore a
// different quantity from FU-9's QualityMediumMinSamples, which counts VALID SAMPLES. The two
// share an arithmetic origin and measure different things; neither is derived from the other.
const MinSuitabilityWindowSessions = 60

// SuitabilityReason is a canonical code. The natural-language wording lives at the
// presentation boundary, so a stored verdict stays machine-readable and a UI change cannot
// alter what was concluded.
type SuitabilityReason string

const (
	// ── deciding reasons ──
	// ReasonPositiveEligibilityPersistent is deliberately NOT called "stable earnings".
	// See the SUITABLE doc: this is a sign-state history, not a magnitude one.
	ReasonPositiveEligibilityPersistent SuitabilityReason = "POSITIVE_EARNINGS_ELIGIBILITY_PERSISTENT"
	// ReasonEarningsMagnitudeUnknown accompanies every SUITABLE verdict, permanently under
	// FU11-v1. It states the limit of the evidence on the row itself, so a verdict quoted
	// out of context still carries it.
	ReasonEarningsMagnitudeUnknown SuitabilityReason = "EARNINGS_MAGNITUDE_NOT_ESTABLISHED"
	ReasonPositiveEarningsBasis    SuitabilityReason = "POSITIVE_EARNINGS_BASIS_AVAILABLE"
	ReasonNoPositiveTrailing       SuitabilityReason = "NO_POSITIVE_TRAILING_EARNINGS"
	ReasonPEAvailableOnlyRecently  SuitabilityReason = "PE_AVAILABLE_ONLY_RECENTLY"
	ReasonPEAvailabilityIntermit   SuitabilityReason = "PE_AVAILABILITY_INTERMITTENT"
	ReasonEarningsSignChange       SuitabilityReason = "EARNINGS_SIGN_CHANGE"
	ReasonPENeverPublished         SuitabilityReason = "PE_NEVER_PUBLISHED_IN_WINDOW"
	ReasonEarningsBasisUnknown     SuitabilityReason = "CURRENT_EARNINGS_BASIS_UNESTABLISHED"
	ReasonInsufficientEarningsHist SuitabilityReason = "INSUFFICIENT_EARNINGS_HISTORY"
	ReasonNoArchivedSessions       SuitabilityReason = "NO_ARCHIVED_SESSIONS"
	ReasonInsufficientObservation  SuitabilityReason = "INSUFFICIENT_OBSERVATION_WINDOW"

	// ── caution reasons ──
	//
	// Reported, and structurally incapable of changing a class: they are appended after the
	// verdict is decided. Both describe the OUTPUT of a valuation rather than its denominator,
	// which is precisely why neither may decide anything.
	ReasonValuationRegimeWide    SuitabilityReason = "VALUATION_REGIME_WIDE"
	ReasonTargetSensitivityCheck SuitabilityReason = "TARGET_SENSITIVITY_REVIEW"
)

// AllSuitabilityReasons is every reason code this package can emit.
//
// Declared rather than derived, because Go cannot enumerate constants — and therefore kept
// honest from both ends: a presentation test asserts every member has wording, and an
// exhaustive sweep over ClassifySuitability asserts every reason it actually produces is a
// member. A new code added without updating this list fails the second; a code listed without
// wording fails the first.
//
// The alternative — a test that samples a handful of inputs — was tried and quietly covered
// 11 of 14, with two of the three gaps live on real stocks.
var AllSuitabilityReasons = []SuitabilityReason{
	ReasonPositiveEligibilityPersistent,
	ReasonEarningsMagnitudeUnknown,
	ReasonPositiveEarningsBasis,
	ReasonNoPositiveTrailing,
	ReasonPEAvailableOnlyRecently,
	ReasonPEAvailabilityIntermit,
	ReasonEarningsSignChange,
	ReasonPENeverPublished,
	ReasonEarningsBasisUnknown,
	ReasonInsufficientEarningsHist,
	ReasonNoArchivedSessions,
	ReasonInsufficientObservation,
	ReasonValuationRegimeWide,
	ReasonTargetSensitivityCheck,
}

// EarningsSign is what the most recent financial filing says about the earnings basis.
type EarningsSign string

const (
	EarningsPositive    EarningsSign = "POSITIVE"
	EarningsNonPositive EarningsSign = "NON_POSITIVE"
	// EarningsUnknown is the honest default. No filing archived, no EPS in the filing, or the
	// EPS and the net income disagree about the sign.
	EarningsUnknown EarningsSign = "UNKNOWN"
)

// PEPersistence is the SHAPE of P/E availability across the archived window.
//
// It carries most of the weight in this file, and it is deliberately STRUCTURAL: three of the
// four cases are decided by an ordering or an emptiness, with no threshold at all.
//
// # What this signal actually is, stated precisely
//
// The exchanges do not compute a P/E when the reference EPS is zero or negative. So the
// nil/non-nil pattern is a POSITIVE-EARNINGS ELIGIBILITY HISTORY — equivalently, a
// trailing-earnings SIGN-STATE proxy. That is the only earnings history this repo has, since
// the fundamental archive holds a single filed period per stock.
//
// It is NOT an earnings-MAGNITUDE history, and the difference is not academic:
//
//	TTM EPS   20 → 10 → 5 → 1
//
// stays PERSISTENT throughout. A denominator collapsing by 95% is invisible to this signal,
// because at every point it remained positive. Nothing in this file may therefore be described
// as showing stable, healthy or improving earnings; it shows only that the company stayed on
// the profitable side of zero for as long as this repo was watching.
//
// # And it is bounded by what was ARCHIVED
//
// A session this repo never fetched leaves no record at all — a stock absent from a
// whole-market response is archived with no Ratios, not with a nil P/E — so an acquisition
// gap shortens the series rather than punching a hole in it. That is the correct behaviour:
// a missing fetch is a data-completeness problem and must never read as a financial fact.
//
// The cost is that PERSISTENT means "no ARCHIVED session lacked a P/E", not "no session
// lacked one". A loss stretch inside an unfetched gap is invisible. This inherits FU-9's
// archive-completeness caveat and cannot be resolved without a trading calendar.
type PEPersistence string

const (
	// PEPersistent — every archived session in the window carried a P/E. The denominator was
	// positive throughout, as far as this repo observed.
	PEPersistent PEPersistence = "PERSISTENT"
	// PERecentOnly — every session WITHOUT a P/E precedes every session with one: a single
	// loss-to-profit transition. The distribution describes the period since that transition,
	// however many samples it contains.
	PERecentOnly PEPersistence = "RECENT_ONLY"
	// PEIntermittent — sessions with and without a P/E interleave. The denominator crossed
	// zero more than once, so the distribution blends distinct earnings regimes.
	PEIntermittent PEPersistence = "INTERMITTENT"
	// PENever — sessions WERE archived, and none of them carried a P/E.
	PENever PEPersistence = "NEVER"
	// PEUnknown — nothing was archived, so there is no pattern to read.
	//
	// Distinct from NEVER, and the distinction is load-bearing. NEVER is an OBSERVATION: this
	// repo watched the exchange across N sessions and it published no ratio on any of them.
	// UNKNOWN is the absence of one, and reporting it as NEVER would state that an exchange
	// declined to publish something it was never asked for.
	PEUnknown PEPersistence = "UNKNOWN"
)

// SuitabilityInput is the evidence a verdict is computed from.
type SuitabilityInput struct {
	// Earnings is the sign of the latest FILED period. Never inferred from the absence of a
	// P/E — see the UNSUITABLE rule.
	Earnings EarningsSign
	// EarningsPeriod and EarningsSource label where that sign came from, so a verdict can be
	// traced to a filing rather than to this package.
	EarningsPeriod string
	EarningsSource string

	// Persistence is the P/E availability shape; WindowSessions is how many sessions it was
	// observed over.
	Persistence    PEPersistence
	WindowSessions int

	// RelativeIQR is the distribution's width ÷ its median, and BaseUpsidePct is the base
	// case's implied upside. Both are inputs to CAUTION reasons only. Neither can change a
	// class, and the tests hold that line.
	RelativeIQR   *float64
	BaseUpsidePct *float64
}

// Caution thresholds. These attach a note; they never decide a verdict.
const (
	// WideRegimeRelativeIQR — the middle half of the observations spans more than half the
	// median. 6274 台燿 sits near 0.91 on a flawless archive, which is what a real re-rating
	// looks like and is exactly why this is a note rather than a downgrade.
	WideRegimeRelativeIQR = 0.50
	// SensitiveTargetUpsidePct — a base case implying the price should double. 1815 富喬 has
	// been there. It says the target is sensitive to the multiple, not that the model is
	// wrong.
	SensitiveTargetUpsidePct = 100.0
)

// SuitabilityVerdict is a classification with the canonical reasons behind it.
type SuitabilityVerdict struct {
	Suitability Suitability         `json:"suitability"`
	RuleVersion string              `json:"rule_version"`
	Reasons     []SuitabilityReason `json:"reasons,omitempty"`

	// The evidence the verdict was reached from, carried so it can be checked without
	// re-deriving it.
	Earnings       EarningsSign  `json:"earnings_sign"`
	EarningsPeriod string        `json:"earnings_period,omitempty"`
	EarningsSource string        `json:"earnings_source,omitempty"`
	Persistence    PEPersistence `json:"pe_persistence"`
	WindowSessions int           `json:"window_sessions"`
}

// ClassifySuitability decides whether trailing P/E is an appropriate primary anchor.
//
// Deterministic and total: no clock, no I/O, no model. The rules are evaluated in the order
// written, first match wins, and the order is part of the contract.
func ClassifySuitability(in SuitabilityInput) SuitabilityVerdict {
	v := SuitabilityVerdict{
		RuleVersion:    SuitabilityRuleVersion,
		Earnings:       in.Earnings,
		EarningsPeriod: in.EarningsPeriod,
		EarningsSource: in.EarningsSource,
		Persistence:    in.Persistence,
		WindowSessions: in.WindowSessions,
	}
	// Normalise the INPUT, not just the reported copy.
	//
	// This read `v.Earnings` — the output — while every branch below tests `in.Earnings`. A
	// zero-valued sign therefore matched no branch, fell through to the default, and produced
	// SUITABLE carrying POSITIVE_EARNINGS_BASIS_AVAILABLE: a filed positive basis that does
	// not exist, on a verdict simultaneously reporting the sign as UNKNOWN.
	if in.Earnings == "" {
		in.Earnings = EarningsUnknown
	}
	v.Earnings = in.Earnings

	switch {
	// 0. Nothing archived. Checked FIRST, because every rule below reads a PATTERN across
	// archived sessions and an empty archive has none.
	//
	// Without this guard, a stock with zero archived sessions and a filed loss reached
	// UNSUITABLE carrying PE_NEVER_PUBLISHED_IN_WINDOW — a claim that the exchange published
	// no ratio, about an exchange this repo never asked. That is the same error the rule
	// below exists to prevent, arrived at from the other side: there, silence was read as a
	// loss; here, an absence of observation would be read as silence. Neither is evidence.
	//
	// Not hypothetical. 3037 欣興 spent this very work item holding a valuation archive and no
	// fundamental one, and the reverse is equally reachable for any newly added stock.
	case in.WindowSessions <= 0 || in.Persistence == PEUnknown || in.Persistence == "":
		v.Suitability = SuitabilityInsufficientData
		v.Persistence = PEUnknown
		v.Reasons = []SuitabilityReason{ReasonNoArchivedSessions}
		if in.Earnings == EarningsUnknown {
			v.Reasons = append(v.Reasons, ReasonEarningsBasisUnknown)
		}

	// 0b. The observation floor for a NEVER pattern.
	//
	// UNSUITABLE is the only verdict that asserts a company-level fact, so it is the only one
	// that needs a floor on how much observation stands behind it. Three archived sessions
	// with no published P/E is not a year of silence, and letting it reach the same conclusion
	// as 3055 蔚華科's 225 would make the two indistinguishable in the stored record.
	//
	// The filed earnings evidence does NOT bypass this floor. It is genuinely independent, so
	// bypassing would be defensible — but it would be a DIFFERENT rule, concluding from a
	// filing rather than from a pattern, and quietly folding it into NEVER would leave the
	// stored verdict unable to say which evidence produced it. If that rule is ever wanted it
	// gets its own name, its own reason code and its own row here.
	case in.Persistence == PENever && in.WindowSessions < MinSuitabilityWindowSessions:
		v.Suitability = SuitabilityInsufficientData
		v.Reasons = []SuitabilityReason{ReasonInsufficientObservation}
		if in.Earnings == EarningsNonPositive {
			// Said plainly, because it is the evidence a reader will ask about: the filing is
			// known and is not what is missing.
			v.Reasons = append(v.Reasons, ReasonNoPositiveTrailing)
		}

	// 1. No P/E across a window long enough to mean something, AND a filing that shows the
	// earnings basis is not positive. TWO independent sources agreeing is what licenses the
	// conclusion.
	case in.Persistence == PENever && in.Earnings == EarningsNonPositive:
		v.Suitability = SuitabilityUnsuitable
		v.Reasons = []SuitabilityReason{ReasonNoPositiveTrailing, ReasonPENeverPublished}

	// 2. No P/E anywhere, but no filing that says why.
	//
	// This is the rule the 3055 case exists to protect. "The exchange published no P/E"
	// alone does NOT establish that a company is loss-making — it is consistent with a
	// suspension, a listing change, a dataset gap, or an archive that simply never covered
	// the stock. Concluding a loss from silence would be inventing a financial fact, so the
	// honest answer is that there is nothing to judge on.
	case in.Persistence == PENever:
		v.Suitability = SuitabilityInsufficientData
		v.Reasons = []SuitabilityReason{ReasonPENeverPublished}
		if in.Earnings == EarningsUnknown {
			v.Reasons = append(v.Reasons, ReasonEarningsBasisUnknown)
		}

	// 3. A single loss-to-profit transition: the P/E exists only because the denominator
	// recently turned positive. Arithmetically sound, economically thin as a long-run anchor
	// — the distribution describes one young regime, whatever its sample count.
	case in.Persistence == PERecentOnly:
		v.Suitability = SuitabilityWeak
		v.Reasons = []SuitabilityReason{ReasonPEAvailableOnlyRecently, ReasonEarningsSignChange}
		if in.WindowSessions < MinSuitabilityWindowSessions {
			v.Reasons = append(v.Reasons, ReasonInsufficientEarningsHist)
		}

	// 4. The denominator crossed zero more than once inside the window, so the distribution
	// blends regimes that are not comparable.
	case in.Persistence == PEIntermittent:
		v.Suitability = SuitabilityConditional
		v.Reasons = []SuitabilityReason{ReasonPEAvailabilityIntermit, ReasonEarningsSignChange}

	// 5. Unbroken P/E, but observed over less than one reporting cycle. The denominator may
	// never have been updated during the window, so persistence has not yet been demonstrated.
	case in.WindowSessions < MinSuitabilityWindowSessions:
		v.Suitability = SuitabilityConditional
		v.Reasons = []SuitabilityReason{ReasonInsufficientEarningsHist}
		if in.Earnings == EarningsPositive {
			v.Reasons = append(v.Reasons, ReasonPositiveEarningsBasis)
		}

	// 6. Unbroken P/E, and the latest filing shows the basis turning negative. The exchange's
	// trailing figure has not caught up with the filing yet; the anchor is deteriorating in a
	// way the distribution cannot show.
	case in.Earnings == EarningsNonPositive:
		v.Suitability = SuitabilityConditional
		v.Reasons = []SuitabilityReason{ReasonEarningsSignChange, ReasonNoPositiveTrailing}

	// 7. Unbroken P/E, but no filing to corroborate the basis.
	case in.Earnings == EarningsUnknown:
		v.Suitability = SuitabilityConditional
		v.Reasons = []SuitabilityReason{ReasonEarningsBasisUnknown}

	// 8. A filed positive basis, and an unbroken P/E over at least a reporting cycle.
	//
	// EARNINGS_MAGNITUDE_NOT_ESTABLISHED is unconditional here, not a caution. It is part of
	// what SUITABLE means under FU11-v1: structurally usable, magnitude unproven.
	default:
		v.Suitability = SuitabilitySuitable
		v.Reasons = []SuitabilityReason{
			ReasonPositiveEligibilityPersistent,
			ReasonPositiveEarningsBasis,
			ReasonEarningsMagnitudeUnknown,
		}
	}

	v.Reasons = append(v.Reasons, cautions(in)...)
	return v
}

// cautions are notes attached AFTER the verdict, never inputs to it.
//
// Both describe the output of a valuation rather than its denominator. A wide distribution is
// a re-rating the archive captured correctly; a large implied upside is what a low current
// multiple against that distribution arithmetically produces. Letting either decide
// suitability would be reasoning backwards from a result to the validity of the model that
// produced it — and would contradict FU-9, which deliberately kept dispersion out of the data
// quality grade for the same reason.
func cautions(in SuitabilityInput) []SuitabilityReason {
	var out []SuitabilityReason
	if in.RelativeIQR != nil && *in.RelativeIQR >= WideRegimeRelativeIQR {
		out = append(out, ReasonValuationRegimeWide)
	}
	if in.BaseUpsidePct != nil && abs(*in.BaseUpsidePct) >= SensitiveTargetUpsidePct {
		out = append(out, ReasonTargetSensitivityCheck)
	}
	return out
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// PEAvailabilityShape reads the persistence pattern off the archived sessions.
//
// The sessions arrive in ascending order from eligibleSessions, already bounded by asOf and
// the window and already reduced to one record per session, so this needs no dates and no
// arithmetic — only the order of the gaps.
func PEAvailabilityShape(sessions []sessionPE) PEPersistence {
	valid, invalid := 0, 0
	lastInvalid, firstValid := -1, -1
	for i, s := range sessions {
		if s.pe != nil {
			valid++
			if firstValid < 0 {
				firstValid = i
			}
			continue
		}
		invalid++
		lastInvalid = i
	}
	switch {
	case len(sessions) == 0:
		// No archive, not an exchange that published nothing. See PEUnknown.
		return PEUnknown
	case valid == 0:
		return PENever
	case invalid == 0:
		return PEPersistent
	case lastInvalid < firstValid:
		// Every gap precedes every observation: one transition, loss to profit.
		return PERecentOnly
	default:
		return PEIntermittent
	}
}
