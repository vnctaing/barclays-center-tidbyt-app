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
		if !state[e.Key].FirstSeen.IsZero() {
			t.Fatalf("baseline event %q should have zero first-seen", e.Title)
		}
	}

	later := now.Add(time.Hour)
	Observe(state, events, later)
	for _, e := range events[5:] {
		if !state[e.Key].FirstSeen.Equal(later) {
			t.Fatalf("new event %q should be first seen at %v, got %v", e.Title, later, state[e.Key].FirstSeen)
		}
	}
	for _, e := range events[:5] {
		if !state[e.Key].FirstSeen.IsZero() {
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

func TestPastEventsAreRemembered(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	events := loadFixture(t)
	sortByStart(events)
	game := events[0] // Nets vs. Pacers, 2026-10-25 6:00PM
	before := time.Date(2026, 10, 25, 10, 0, 0, 0, loc)
	state := SeenState{}
	Observe(state, events, before)

	// The venue drops the game from its listing once it is over.
	after := time.Date(2026, 10, 27, 9, 0, 0, 0, loc)
	rest := events[1:]
	Observe(state, rest, after)

	feed := BuildFeed(rest, state, after, loc)
	if len(feed.RecentPast) == 0 || feed.RecentPast[0].Key != game.Key {
		t.Fatalf("past game should be remembered, got %+v", feed.RecentPast)
	}
	if got := feed.RecentPast[0].Start.Format(time.RFC3339); got != "2026-10-25T18:00:00-04:00" {
		t.Fatalf("past start in venue time: %s", got)
	}
	for i := 1; i < len(feed.RecentPast); i++ {
		if feed.RecentPast[i].Start.After(feed.RecentPast[i-1].Start) {
			t.Fatal("past events must be newest first")
		}
	}
}

func TestEventRemovedBeforeItStartedIsForgotten(t *testing.T) {
	events := loadFixture(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	state := SeenState{}
	Observe(state, events, now)
	Observe(state, events[1:], now.Add(time.Hour))
	if _, ok := state[events[0].Key]; ok {
		t.Fatal("a cancelled upcoming event must not linger as a past event")
	}
}

func TestLegacyStateFileStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := `{"a|1": "2026-10-20T12:00:00Z", "b|2": "0001-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state["a|1"].FirstSeen.IsZero() || !state["b|2"].FirstSeen.IsZero() {
		t.Fatalf("legacy timestamps lost: %+v", state)
	}
	events := []Event{{Key: "a|1", Title: "A", Start: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}}
	Observe(state, events, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	if state["a|1"].Event.Title != "A" || state["a|1"].FirstSeen.IsZero() {
		t.Fatalf("legacy entry should gain its event and keep first-seen: %+v", state["a|1"])
	}
}

func TestShowThatStartedTodayAndLeftTheListingIsPast(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	events := loadFixture(t)
	sortByStart(events)
	game := events[0] // Nets vs. Pacers, 2026-10-25 6:00PM
	state := SeenState{}
	Observe(state, events, time.Date(2026, 10, 25, 10, 0, 0, 0, loc))

	evening := time.Date(2026, 10, 25, 20, 30, 0, 0, loc)
	rest := events[1:]
	Observe(state, rest, evening)
	feed := BuildFeed(rest, state, evening, loc)
	if len(feed.RecentPast) == 0 || feed.RecentPast[0].Key != game.Key {
		t.Fatalf("game played this evening should be past, got %+v", feed.RecentPast)
	}

	// While the venue still lists it, it is tonight's show and not past.
	still := BuildFeed(events, state, evening, loc)
	if len(still.Tonight) != 1 {
		t.Fatalf("listed game should be tonight, got %+v", still.Tonight)
	}
	for _, p := range still.RecentPast {
		if p.Key == game.Key {
			t.Fatal("a game still listed for today must not also be past")
		}
	}
}
