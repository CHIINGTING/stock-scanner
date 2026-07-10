package news

import (
	"testing"
	"time"
)

func TestAgeInDays(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 30, 0, 0, time.UTC)
	cases := []struct {
		name string
		pub  time.Time
		want int
	}{
		{"same-instant", now, 0},
		{"12h-ago-floors-to-0", now.Add(-12 * time.Hour), 0},
		{"1-day", now.Add(-24 * time.Hour), 1},
		{"3-days-and-a-bit", now.Add(-80 * time.Hour), 3},
		{"future-clamped", now.Add(48 * time.Hour), 0},
		{"zero-time", time.Time{}, 0},
	}
	for _, c := range cases {
		if got := AgeInDays(c.pub, now); got != c.want {
			t.Errorf("%s: AgeInDays=%d want %d", c.name, got, c.want)
		}
	}
}

func TestBucketBoundaries(t *testing.T) {
	// Default config: FRESH≤2, ACTIVE≤5, DECAYING≤10, MaxAgeDays=10.
	cfg := NewsConfig{}
	cases := []struct {
		age  int
		want AgeBucket
	}{
		{0, AgeFresh},
		{2, AgeFresh},
		{3, AgeActive},
		{5, AgeActive},
		{6, AgeDecaying},
		{10, AgeDecaying},
		{11, AgeExpired},
		{40, AgeExpired},
	}
	for _, c := range cases {
		if got := Bucket(c.age, cfg); got != c.want {
			t.Errorf("age=%d: Bucket=%s want %s", c.age, got, c.want)
		}
	}
}

func TestBucketRespectsCustomConfig(t *testing.T) {
	cfg := NewsConfig{
		MaxAgeDays: 4,
		Age:        AgeConfig{FreshDays: 1, ActiveDays: 2, DecayingDays: 4},
	}
	// With MaxAgeDays=4, age 5 must be EXPIRED even though DecayingDays would allow it.
	if got := Bucket(5, cfg); got != AgeExpired {
		t.Errorf("age=5 custom: got %s want EXPIRED", got)
	}
	if got := Bucket(1, cfg); got != AgeFresh {
		t.Errorf("age=1 custom: got %s want FRESH", got)
	}
	if got := Bucket(2, cfg); got != AgeActive {
		t.Errorf("age=2 custom: got %s want ACTIVE", got)
	}
}

func TestDefaultedFillsZeroKnobs(t *testing.T) {
	d := NewsConfig{}.Defaulted()
	if d.MaxAgeDays != 10 || d.Age.FreshDays != 2 || d.Age.ActiveDays != 5 || d.Age.DecayingDays != 10 {
		t.Fatalf("unexpected defaults: %+v", d)
	}
	if d.TimeoutSeconds != 15 {
		t.Errorf("TimeoutSeconds default = %d want 15", d.TimeoutSeconds)
	}
}
