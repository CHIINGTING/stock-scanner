package main

import "testing"

func TestResolveDates(t *testing.T) {
	cases := []struct {
		name           string
		date, from, to string
		want           []string
		wantErr        bool
	}{
		{name: "single date", date: "2026-08-25", want: []string{"2026-08-25"}},
		{
			// 2026-08-21 is a Friday; the weekend in between is dropped up front because
			// neither exchange ever publishes on one.
			name: "range skips weekends",
			from: "2026-08-21", to: "2026-08-25",
			want: []string{"2026-08-21", "2026-08-24", "2026-08-25"},
		},
		{name: "single-day range", from: "2026-08-25", to: "2026-08-25", want: []string{"2026-08-25"}},
		{name: "date and range are exclusive", date: "2026-08-25", from: "2026-08-01", wantErr: true},
		{name: "from without to", from: "2026-08-01", wantErr: true},
		{name: "to without from", to: "2026-08-01", wantErr: true},
		{name: "reversed range", from: "2026-08-25", to: "2026-08-01", wantErr: true},
		{name: "weekend-only range", from: "2026-08-22", to: "2026-08-23", wantErr: true},
		{name: "bad date format", date: "20260825", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveDates(tc.date, tc.from, tc.to)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// With no flags at all the command runs "today" — the after-close daily use.
func TestResolveDatesDefaultsToToday(t *testing.T) {
	got, err := resolveDates("", "", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
}
