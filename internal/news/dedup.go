package news

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Event is a set of RawNewsItems that describe the same real-world event (e.g. one
// 股癌 episode), merged across sources. Strength/direction are computed ONCE per event
// downstream, so multiple sources can never double-count.
type Event struct {
	EventID     string        // "gooaye-ep-677" or "fp-<hash>" for non-episodic items
	Sources     []NewsSource  // one per contributing raw item
	PublishedAt time.Time     // earliest source publish time
	Items       []RawNewsItem // contributing raw items (richest content first)
}

// PrimaryContent returns the longest available content across the event's sources
// (SocialWorkerDaily full notes are preferred over sparse best-effort items).
func (e Event) PrimaryContent() string {
	best := ""
	for _, it := range e.Items {
		if len(it.Content) > len(best) {
			best = it.Content
		}
	}
	if best == "" {
		// fall back to the richest title.
		for _, it := range e.Items {
			if len(it.Title) > len(best) {
				best = it.Title
			}
		}
	}
	return best
}

// Title returns a representative title (from the richest-content item).
func (e Event) Title() string {
	best := RawNewsItem{}
	for _, it := range e.Items {
		if len(it.Content) >= len(best.Content) {
			best = it
		}
	}
	return best.Title
}

var boilerplateRe = regexp.MustCompile(`(?i)[\s　]|筆記|心得|重點|股癌|gooaye|podcast|ep\s*[0-9]+`)

// Dedup groups raw items into events. Primary key = normalized EpisodeID; items with no
// episode id fall back to a content fingerprint (normalized title + publish day). URLs
// are never used as a key (they always differ across sources). The result is sorted by
// PublishedAt desc then EventID for determinism.
func Dedup(items []RawNewsItem) []Event {
	byKey := map[string]*Event{}
	var order []string
	for _, it := range items {
		key := it.EpisodeID
		if key == "" {
			key = "fp-" + fingerprint(it)
		}
		ev, ok := byKey[key]
		if !ok {
			ev = &Event{EventID: key, PublishedAt: it.PublishedAt}
			byKey[key] = ev
			order = append(order, key)
		}
		ev.Items = append(ev.Items, it)
		ev.Sources = appendSource(ev.Sources, NewsSource{Provider: it.Source, URL: it.URL})
		if !it.PublishedAt.IsZero() && (ev.PublishedAt.IsZero() || it.PublishedAt.Before(ev.PublishedAt)) {
			ev.PublishedAt = it.PublishedAt
		}
	}
	events := make([]Event, 0, len(order))
	for _, k := range order {
		ev := byKey[k]
		// richest content first for stable PrimaryContent/Title.
		sort.SliceStable(ev.Items, func(i, j int) bool {
			return len(ev.Items[i].Content) > len(ev.Items[j].Content)
		})
		events = append(events, *ev)
	}
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].PublishedAt.Equal(events[j].PublishedAt) {
			return events[i].PublishedAt.After(events[j].PublishedAt)
		}
		return events[i].EventID < events[j].EventID
	})
	return events
}

// TODO(news): fingerprint hardening for non-episode sources — two distinct same-day
// articles whose titles normalize to the same string would merge. Add a content hash
// and/or restrict cross-source merges to matching episode ids. Latent today (SWD always
// carries an episode id; TWETQ yields none), so deferred.

// fingerprint is a source-independent key for non-episodic items: normalized title +
// publish day. Strips episode/boilerplate words so trivially-different titles collapse.
func fingerprint(it RawNewsItem) string {
	norm := strings.ToLower(it.Title)
	norm = boilerplateRe.ReplaceAllString(norm, "")
	day := ""
	if !it.PublishedAt.IsZero() {
		day = it.PublishedAt.UTC().Format("2006-01-02")
	}
	sum := sha1.Sum([]byte(norm + "|" + day))
	return hex.EncodeToString(sum[:8])
}

func appendSource(list []NewsSource, s NewsSource) []NewsSource {
	for _, e := range list {
		if e.Provider == s.Provider && e.URL == s.URL {
			return list
		}
	}
	return append(list, s)
}
