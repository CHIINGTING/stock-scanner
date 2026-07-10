package socialworkerdaily

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParsePostsFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "wp_posts.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	items, err := parsePosts(body)
	if err != nil {
		t.Fatalf("parsePosts: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one 股癌筆記 item")
	}
	// Every kept item must be a gooaye note with an episode id, a title, a URL and a
	// non-zero publish time.
	var sawEP677 bool
	for _, it := range items {
		if it.Source != name {
			t.Errorf("source = %q want %q", it.Source, name)
		}
		if it.EpisodeID == "" {
			t.Errorf("%q: empty EpisodeID", it.Title)
		}
		if it.PublishedAt.IsZero() {
			t.Errorf("%q: zero PublishedAt", it.Title)
		}
		if it.URL == "" {
			t.Errorf("%q: empty URL", it.Title)
		}
		if it.EpisodeID == "gooaye-ep-677" {
			sawEP677 = true
			if it.PublishedAt.Year() != 2026 {
				t.Errorf("EP677 year = %d want 2026", it.PublishedAt.Year())
			}
			if len(it.Content) < 50 {
				t.Errorf("EP677 content too short (%d chars) — HTML strip likely failed", len(it.Content))
			}
		}
	}
	if !sawEP677 {
		t.Error("fixture should contain gooaye-ep-677")
	}
}

func TestParseRSSFixtureFiltersNonGooaye(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "feed.xml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	items, err := parseRSS(body, 8)
	if err != nil {
		t.Fatalf("parseRSS: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one gooaye item from feed")
	}
	// The feed mixes social-work posts (成年晚期, 失智症) — those must be filtered out.
	for _, it := range items {
		if it.EpisodeID == "" {
			t.Errorf("non-gooaye item leaked through filter: %q", it.Title)
		}
	}
}

func TestParseWPTime(t *testing.T) {
	got := parseWPTime("2026-07-08T15:49:31")
	want := time.Date(2026, 7, 8, 15, 49, 31, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("parseWPTime = %v want %v", got, want)
	}
	if !parseWPTime("").IsZero() {
		t.Error("empty time should be zero")
	}
}
