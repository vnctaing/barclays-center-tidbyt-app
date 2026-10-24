package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func loadFixture(t *testing.T) []Event {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "events_all.html"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	events, err := ParseListing(f)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestParseListingFixture(t *testing.T) {
	events := loadFixture(t)
	if len(events) != 87 {
		t.Fatalf("want 87 events, got %d", len(events))
	}
	first := events[0]
	if first.Title != "Brooklyn Nets vs. Indiana Pacers" {
		t.Errorf("title: %q", first.Title)
	}
	if first.URL != "https://www.barclayscenter.com/events/detail/brooklyn-nets-vs-indiana-pacers-20261025" {
		t.Errorf("url: %q", first.URL)
	}
	if first.TicketURL == "" {
		t.Errorf("ticket url missing")
	}
	if got := first.Start.UTC().Format(time.RFC3339); got != "2026-10-25T22:00:00Z" {
		t.Errorf("start: %s", got)
	}
}

func TestSameSlugDifferentDatesAreDistinct(t *testing.T) {
	keys := map[string]bool{}
	for _, e := range loadFixture(t) {
		keys[e.Key] = true
	}
	if len(keys) != 87 {
		t.Fatalf("keys collapsed: %d unique of 87", len(keys))
	}
}

func TestObserveBaselineThenNew(t *testing.T) {
	events := loadFixture(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	state := SeenState{}

	Observe(state, events[:5], now)
	for _, e := range events[:5] {
		if !state[e.Key].IsZero() {
			t.Fatalf("baseline event %q should have zero first-seen", e.Title)
		}
	}

	later := now.Add(time.Hour)
	Observe(state, events, later)
	for _, e := range events[5:] {
		if !state[e.Key].Equal(later) {
			t.Fatalf("new event %q should be first seen at %v, got %v", e.Title, later, state[e.Key])
		}
	}
	for _, e := range events[:5] {
		if !state[e.Key].IsZero() {
			t.Fatalf("baseline event %q should stay baseline", e.Title)
		}
	}
}

func TestBuildFeedTonightAndRecentlyAdded(t *testing.T) {
	events := loadFixture(t)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// Morning of Oct 25 in New York: the Nets game at 6:00PM is tonight.
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, loc)
	state := SeenState{}
	Observe(state, events[:60], now.Add(-time.Hour))
	Observe(state, events, now.Add(-time.Hour)) // all the rest appear "new" an hour ago

	feed := BuildFeed(events, state, now, loc)
	if len(feed.Tonight) != 1 || feed.Tonight[0].Title != "Brooklyn Nets vs. Indiana Pacers" {
		t.Fatalf("tonight: %+v", feed.Tonight)
	}
	if len(feed.Upcoming) != upcomingLimit {
		t.Errorf("upcoming should be capped at %d, got %d", upcomingLimit, len(feed.Upcoming))
	}
	if len(feed.RecentlyAdded) != recentLimit {
		t.Errorf("recently added should be capped at %d, got %d", recentLimit, len(feed.RecentlyAdded))
	}
	for _, a := range feed.RecentlyAdded {
		if a.AddedAt.IsZero() {
			t.Errorf("baseline event leaked into recently added: %q", a.Title)
		}
	}
}

func TestBuildFeedNoTonight(t *testing.T) {
	events := loadFixture(t)
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, loc)
	feed := BuildFeed(events, SeenState{}, now, loc)
	if len(feed.Tonight) != 0 || feed.Tonight == nil {
		t.Fatalf("tonight should be empty non-nil slice, got %#v", feed.Tonight)
	}
}

func TestBuildFeedReturnsVenueTime(t *testing.T) {
	// The server may run in UTC. Output must still be in New York time.
	loc, _ := time.LoadLocation("America/New_York")
	events := loadFixture(t)
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, loc)
	feed := BuildFeed(events, SeenState{}, now, loc)
	if len(feed.Tonight) != 1 {
		t.Fatalf("want one event tonight, got %d", len(feed.Tonight))
	}
	if got := feed.Tonight[0].Start.Format(time.RFC3339); got != "2026-10-25T18:00:00-04:00" {
		t.Fatalf("tonight start: %s", got)
	}
}
