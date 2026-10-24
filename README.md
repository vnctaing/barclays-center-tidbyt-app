# Barclays Center on Tidbyt

A [Tidbyt](https://tidbyt.com) app that shows what is on at the Barclays Center: tonight's event, the next ones, and what was just added to the calendar. Long titles scroll horizontally.

![The three screens, captured mid-scroll](docs/screens.png)

![Animated demo](docs/demo.gif)

## How it works

```
barclayscenter.com/events/all
        │  scrape (Go, on request)
        ▼
Fly.io machine ── /api/events (JSON) ◄── pixlet render ◄── GitHub Actions (every 30 min)
                                                                  │ pixlet push
                                                                  ▼
                                                               Tidbyt
```

- `backend/` is a small Go service. It scrapes the events page when a request arrives and the last scrape is older than 20 minutes. It remembers when it first saw each event, which is how "recently added" works. The first scrape is a baseline, so nothing shows as new on day one.
- The Fly machine scales to zero. It exits 20 seconds after the last request, so you pay for roughly a minute of compute a day.
- `tidbyt/barclays.star` is the [Pixlet](https://github.com/tidbyt/pixlet) app. It fetches the feed and draws three screens: Tonight, Upcoming, and Just Added (or Coming Up when nothing is new).
- `.github/workflows/push.yml` renders the app and pushes it to the device every 30 minutes. A pushed image is static, so the schedule is what keeps the display fresh.

## Run it yourself

Backend:

```sh
cd backend
go test ./...
go run .                      # http://localhost:8080/api/events
```

App preview:

```sh
pixlet serve tidbyt/barclays.star        # uses the hosted backend by default
```

Deploy your own:

1. Pick a unique app name in `backend/fly.toml`, then `fly launch --no-deploy`, `fly volumes create data --size 1`, and `fly deploy` from `backend/`.
2. Point `DEFAULT_BACKEND` in `tidbyt/barclays.star` and the URL in the workflow at your app.
3. Add two repository secrets: `TIDBYT_API_KEY` and `TIDBYT_DEVICE_ID`.

Scheduled workflows are disabled by GitHub after 60 days without repository activity, so re-enable them if the display stops updating.

## Notes

Events come from the public Barclays Center website. This is an unofficial personal project, not affiliated with the venue.
