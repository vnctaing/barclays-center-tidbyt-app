package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata"
)

const (
	venueTimezone = "America/New_York"
	scrapeTimeout = 25 * time.Second
)

// server has no background timers. It scrapes only when a request arrives and
// the last attempt is older than the interval, so an idle sprite does no work.
type server struct {
	mu          sync.Mutex
	events      []Event
	state       SeenState
	statePath   string
	loc         *time.Location
	client      *http.Client
	interval    time.Duration
	lastAttempt time.Time
	lastErr     string
}

// refreshIfDue must be called with s.mu held.
func (s *server) refreshIfDue(now time.Time) {
	if !s.lastAttempt.IsZero() && now.Sub(s.lastAttempt) < s.interval {
		return
	}
	s.lastAttempt = now
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()

	events, err := FetchListing(ctx, s.client)
	if err != nil {
		// Keep the last good events. Only the error is recorded.
		log.Printf("scrape failed, keeping last good data: %v", err)
		s.lastErr = err.Error()
		return
	}
	s.lastErr = ""
	if Observe(s.state, events, now) {
		if err := SaveState(s.statePath, s.state); err != nil {
			log.Printf("saving state: %v", err)
		}
	}
	s.events = events
	log.Printf("scraped %d events", len(events))
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	now := time.Now()
	s.refreshIfDue(now)
	feed := BuildFeed(s.events, s.state, now, s.loc)
	feed.Stale = s.lastErr != ""
	feed.LastError = s.lastErr
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "max-age=300")
	if err := json.NewEncoder(w).Encode(feed); err != nil {
		log.Printf("encoding feed: %v", err)
	}
}

// exitWhenIdle cancels the run context after idle has passed with no request
// in flight. On Fly the machine then stops and bills nothing until the next
// request starts it again.
func exitWhenIdle(ctx context.Context, stop context.CancelFunc, idle time.Duration, next http.Handler) http.Handler {
	var inFlight atomic.Int64
	var lastActive atomic.Int64
	lastActive.Store(time.Now().UnixNano())
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				quiet := time.Since(time.Unix(0, lastActive.Load()))
				if inFlight.Load() == 0 && quiet > idle {
					log.Printf("idle for %s, exiting", idle)
					stop()
					return
				}
			}
		}
	}()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		defer func() {
			lastActive.Store(time.Now().UnixNano())
			inFlight.Add(-1)
		}()
		next.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	interval, err := time.ParseDuration(envOr("SCRAPE_INTERVAL", "30m"))
	if err != nil {
		log.Fatalf("SCRAPE_INTERVAL: %v", err)
	}
	dataDir := envOr("DATA_DIR", "./data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	statePath := filepath.Join(dataDir, "state.json")
	state, err := LoadState(statePath)
	if err != nil {
		// A bad state file must not crash-loop the machine. Start over; the
		// next scrape becomes the new baseline.
		log.Printf("ignoring unreadable state file: %v", err)
		state = SeenState{}
	}
	loc, err := time.LoadLocation(venueTimezone)
	if err != nil {
		log.Fatal(err)
	}

	s := &server{
		state:     state,
		statePath: statePath,
		loc:       loc,
		client:    &http.Client{Timeout: scrapeTimeout},
		interval:  interval,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	idle, err := time.ParseDuration(envOr("IDLE_EXIT", "0"))
	if err != nil {
		log.Fatalf("IDLE_EXIT: %v", err)
	}

	addr := envOr("ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	handler := http.Handler(mux)
	if idle > 0 {
		handler = exitWhenIdle(ctx, stop, idle, mux)
	}
	srv.Handler = handler
	log.Printf("listening on %s, refreshing at most every %s on request", addr, interval)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
