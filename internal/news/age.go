package news

import "time"

// AgeInDays is the whole-day gap between publish and observation (floored, never
// negative). A signal observed the same calendar-window as publication is age 0.
func AgeInDays(publishedAt, now time.Time) int {
	if publishedAt.IsZero() || now.Before(publishedAt) {
		return 0
	}
	d := int(now.Sub(publishedAt).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// Bucket classifies a signal's lifecycle stage from its age in days against the
// centralized AgeConfig + MaxAgeDays. Boundaries are inclusive upper bounds:
//
//	age ≤ FreshDays          → FRESH
//	age ≤ ActiveDays         → ACTIVE
//	age ≤ DecayingDays / Max → DECAYING
//	otherwise                → EXPIRED
//
// EXPIRED signals are still retained (snapshot/history); they are only de-emphasized
// in the report. Nothing here is hard-coded — all thresholds come from cfg.
func Bucket(ageDays int, cfg NewsConfig) AgeBucket {
	c := cfg.Defaulted()
	switch {
	case ageDays <= c.Age.FreshDays:
		return AgeFresh
	case ageDays <= c.Age.ActiveDays:
		return AgeActive
	case ageDays <= c.Age.DecayingDays && ageDays <= c.MaxAgeDays:
		return AgeDecaying
	default:
		return AgeExpired
	}
}

// BucketAt is the convenience combiner used by the service/report layer.
func BucketAt(publishedAt, now time.Time, cfg NewsConfig) AgeBucket {
	return Bucket(AgeInDays(publishedAt, now), cfg)
}
