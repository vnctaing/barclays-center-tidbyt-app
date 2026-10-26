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
	pastLimit     = 10
	pastRetention = 365 * 24 * time.Hour
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
	RecentPast    []Event   `json:"recent_past"`
}

// Seen is what the service remembers about an event. FirstSeen is zero when
// the event was already on the listing when tracking began, so it is not "new".
// Event is the last listing data seen for it, kept so the event can still be
// shown after the venue drops it from the listing.
type Seen struct {
	FirstSeen time.Time `json:"first_seen"`
	Event     Event     `json:"event"`
}

// UnmarshalJSON also reads the old format, a bare first-seen timestamp.
func (s *Seen) UnmarshalJSON(data []byte) error {
	var legacy time.Time
	if err := json.Unmarshal(data, &legacy); err == nil {
		*s = Seen{FirstSeen: legacy}
		return nil
	}
	type plain Seen
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*s = Seen(p)
	return nil
}

// SeenState maps event key to what was remembered about it.
type SeenState map[string]Seen

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

// Observe records listed events and prunes state that is no longer needed.
// On the very first run every listed event is a baseline (zero time), so the
// app does not report the whole existing listing as "recently added".
// An event that leaves the listing after its start time has passed is kept
// as a past event; one that leaves before it started was removed, so it is dropped.
// It reports whether the state changed.
func Observe(state SeenState, events []Event, now time.Time) bool {
	baseline := len(state) == 0
	changed := false
	listed := map[string]bool{}
	for _, e := range events {
		listed[e.Key] = true
		seen, ok := state[e.Key]
		if !ok {
			seen.FirstSeen = now
			if baseline {
				seen.FirstSeen = time.Time{}
			}
		}
		if !ok || !sameEvent(seen.Event, e) {
			seen.Event = e
			state[e.Key] = seen
			changed = true
		}
	}
	for key, seen := range state {
		if listed[key] {
			continue
		}
		start := seen.Event.Start
		if start.IsZero() || start.After(now) || now.Sub(start) > pastRetention {
			delete(state, key)
			changed = true
		}
	}
	return changed
}

func sameEvent(a, b Event) bool {
	return a.Key == b.Key && a.Title == b.Title && a.Subtitle == b.Subtitle &&
		a.Start.Equal(b.Start) && a.URL == b.URL && a.TicketURL == b.TicketURL
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

	tonight := map[string]bool{}
	for _, e := range feed.Tonight {
		tonight[e.Key] = true
	}
	byKey := map[string]Event{}
	for _, e := range events {
		byKey[e.Key] = e
	}
	for key, seen := range state {
		e, listed := byKey[key]
		if listed && !seen.FirstSeen.IsZero() && now.Sub(seen.FirstSeen) <= recentWindow {
			feed.RecentlyAdded = append(feed.RecentlyAdded, Added{Event: e, AddedAt: seen.FirstSeen})
		}
		if past := seen.Event; !past.Start.IsZero() && past.Start.Before(now) && !tonight[key] {
			past.Start = past.Start.In(loc)
			feed.RecentPast = append(feed.RecentPast, past)
		}
	}
	sort.Slice(feed.RecentPast, func(i, j int) bool {
		return feed.RecentPast[i].Start.After(feed.RecentPast[j].Start)
	})
	if len(feed.RecentPast) > pastLimit {
		feed.RecentPast = feed.RecentPast[:pastLimit]
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
	if feed.RecentPast == nil {
		feed.RecentPast = []Event{}
	}
	return feed
}

func sortByStart(events []Event) {
	sort.Slice(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
}
