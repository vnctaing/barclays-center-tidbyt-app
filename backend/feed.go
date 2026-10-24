package main

import (
	"encoding/json"
	"os"
	"sort"
	"time"
)

const (
	upcomingLimit = 10
	recentLimit   = 10
	recentWindow  = 14 * 24 * time.Hour
	pruneAfter    = 48 * time.Hour
)

// Added is an event with the time this service first saw it on the listing.
type Added struct {
	Event
	AddedAt time.Time `json:"added_at"`
}

// Feed is the JSON document the Tidbyt app reads.
type Feed struct {
	UpdatedAt     time.Time `json:"updated_at"`
	Timezone      string    `json:"timezone"`
	Stale         bool      `json:"stale"`
	LastError     string    `json:"last_error,omitempty"`
	Tonight       []Event   `json:"tonight"`
	Upcoming      []Event   `json:"upcoming"`
	RecentlyAdded []Added   `json:"recently_added"`
}

// SeenState maps event key to the time it was first seen. A zero time means
// the event was already on the listing when tracking began, so it is not "new".
type SeenState map[string]time.Time

func LoadState(path string) (SeenState, error) {
	state := SeenState{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state, nil
}

func SaveState(path string, state SeenState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Observe records newly listed events and prunes state that is no longer needed.
// On the very first run every listed event is a baseline (zero time), so the
// app does not report the whole existing listing as "recently added".
// It reports whether the state changed.
func Observe(state SeenState, events []Event, now time.Time) bool {
	baseline := len(state) == 0
	changed := false
	listed := map[string]bool{}
	for _, e := range events {
		listed[e.Key] = true
		if _, ok := state[e.Key]; !ok {
			first := now
			if baseline {
				first = time.Time{}
			}
			state[e.Key] = first
			changed = true
		}
	}
	for key, firstSeen := range state {
		if listed[key] {
			continue
		}
		if firstSeen.IsZero() || now.Sub(firstSeen) > recentWindow {
			delete(state, key)
			changed = true
		}
	}
	return changed
}

// BuildFeed derives the three lists the app shows from the latest listing.
func BuildFeed(events []Event, state SeenState, now time.Time, loc *time.Location) Feed {
	// The app reads the hour straight from the string, so every time must be in venue time.
	localized := make([]Event, len(events))
	for i, e := range events {
		e.Start = e.Start.In(loc)
		localized[i] = e
	}
	events = localized

	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	tomorrow := today.AddDate(0, 0, 1)

	feed := Feed{UpdatedAt: now, Timezone: loc.String()}
	for _, e := range events {
		start := e.Start
		switch {
		case !start.Before(today) && start.Before(tomorrow):
			feed.Tonight = append(feed.Tonight, e)
		case !start.Before(tomorrow):
			feed.Upcoming = append(feed.Upcoming, e)
		}
	}
	sortByStart(feed.Tonight)
	sortByStart(feed.Upcoming)
	if len(feed.Upcoming) > upcomingLimit {
		feed.Upcoming = feed.Upcoming[:upcomingLimit]
	}

	byKey := map[string]Event{}
	for _, e := range events {
		byKey[e.Key] = e
	}
	for key, firstSeen := range state {
		e, listed := byKey[key]
		if !listed || firstSeen.IsZero() || now.Sub(firstSeen) > recentWindow {
			continue
		}
		feed.RecentlyAdded = append(feed.RecentlyAdded, Added{Event: e, AddedAt: firstSeen})
	}
	sort.Slice(feed.RecentlyAdded, func(i, j int) bool {
		return feed.RecentlyAdded[i].AddedAt.After(feed.RecentlyAdded[j].AddedAt)
	})
	if len(feed.RecentlyAdded) > recentLimit {
		feed.RecentlyAdded = feed.RecentlyAdded[:recentLimit]
	}
	if feed.Tonight == nil {
		feed.Tonight = []Event{}
	}
	if feed.Upcoming == nil {
		feed.Upcoming = []Event{}
	}
	if feed.RecentlyAdded == nil {
		feed.RecentlyAdded = []Added{}
	}
	return feed
}

func sortByStart(events []Event) {
	sort.Slice(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
}
