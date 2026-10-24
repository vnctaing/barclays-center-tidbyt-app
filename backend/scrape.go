package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	eventsURL = "https://www.barclayscenter.com/events/all"
	userAgent = "barclays-tidbyt-bot/0.1 (personal project)"
)

// Event is one scheduled entry from the venue listing.
type Event struct {
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle,omitempty"`
	Start     time.Time `json:"start"`
	URL       string    `json:"url"`
	TicketURL string    `json:"ticket_url,omitempty"`
}

// ParseListing extracts events from the venue's /events/all HTML.
// Each event is keyed by slug plus start timestamp, because one slug can cover several dates.
func ParseListing(body io.Reader) ([]Event, error) {
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var events []Event
	doc.Find("div.entry").Each(func(_ int, s *goquery.Selection) {
		ts, err := strconv.ParseInt(s.AttrOr("data-timestamp", ""), 10, 64)
		if err != nil {
			return
		}
		link := s.Find("a.title-container")
		href := strings.TrimSpace(link.AttrOr("href", ""))
		title := collapse(s.Find("h3").First().Text())
		if href == "" || title == "" {
			return
		}
		start := time.Unix(ts, 0)
		key := fmt.Sprintf("%s|%d", path.Base(href), ts)
		if seen[key] {
			return
		}
		seen[key] = true
		events = append(events, Event{
			Key:       key,
			Title:     title,
			Subtitle:  collapse(s.Find("h4").First().Text()),
			Start:     start,
			URL:       href,
			TicketURL: strings.TrimSpace(s.Find("a.tickets").First().AttrOr("href", "")),
		})
	})
	return events, nil
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// FetchListing downloads the events page with an honest, identifying User-Agent.
func FetchListing(ctx context.Context, client *http.Client) ([]Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("events page returned %s", resp.Status)
	}
	events, err := ParseListing(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		// An empty parse usually means the markup changed or we got a block page.
		// Treat it as a failure so the last good data is kept.
		return nil, errors.New("no events parsed from events page")
	}
	return events, nil
}
