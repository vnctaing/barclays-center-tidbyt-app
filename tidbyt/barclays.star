load("http.star", "http")
load("render.star", "render")
load("schema.star", "schema")

DEFAULT_BACKEND = "https://barclays-tidbyt.fly.dev/api/events"
CACHE_SECONDS = 1800

BG = "#000000"
HEADER_BG = "#14213d"
GOLD = "#fca311"
WHITE = "#ffffff"
GREY = "#9aa0a6"
DIM = "#5f6368"

# Monospace fonts, so text width is exactly chars * advance and we know when to scroll.
TITLE_FONT = "5x8"
TITLE_ADVANCE = 5
SMALL_FONT = "tom-thumb"
SMALL_ADVANCE = 4

FRAME_DELAY_MS = 50
PAUSE_FRAMES = 16  # hold at the start and at the end of a scroll
MIN_SCREEN_FRAMES = 70
MAX_SCREEN_FRAMES = 200
HEADER_HEIGHT = 7
ROW_HEIGHT = 8

def fetch_feed(url):
    resp = http.get(url, ttl_seconds = CACHE_SECONDS)
    if resp.status_code != 200:
        return None
    return resp.json()

# start looks like "2026-10-25T18:00:00-04:00", already in the venue's local time.
def clock(start):
    hour = int(start[11:13])
    suffix = "A" if hour < 12 else "P"
    hour12 = hour % 12
    if hour12 == 0:
        hour12 = 12
    return "%d:%s%s" % (hour12, start[14:16], suffix)

def short_date(start):
    return "%s/%s" % (start[5:7], start[8:10])

def title_of(event):
    return event["title"].upper()

# Pixels to shift left at frame k: hold, scroll to the end, hold.
def scroll_offset(k, overflow):
    if overflow <= 0 or k < PAUSE_FRAMES:
        return 0
    speed = 1 if overflow <= 100 else 2
    moved = (k - PAUSE_FRAMES) * speed
    return overflow if moved > overflow else moved

def frames_needed(overflow):
    if overflow <= 0:
        return 0
    speed = 1 if overflow <= 100 else 2
    return PAUSE_FRAMES + (overflow + speed - 1) // speed + PAUSE_FRAMES

# A row is a dict: text, font, advance, color, height, and optionally label (fixed on the left, with label_width).
def row_overflow(row):
    left = row.get("label_width", 0) + 1
    return row["advance"] * len(row["text"]) - (64 - left - 1)

def row_widget(row, k):
    label_width = row.get("label_width", 0)
    left = label_width + 1
    offset = scroll_offset(k, row_overflow(row))
    text = render.Padding(
        pad = (left - offset, 0, 0, 0),
        child = render.Text(row["text"], font = row["font"], color = row["color"]),
    )
    layers = [render.Row(expanded = True, main_align = "start", children = [text])]
    if label_width > 0:
        # Opaque cover so the title slides under the label instead of over it.
        layers.append(render.Box(
            width = label_width,
            height = row["height"],
            color = row.get("bg", BG),
            child = render.Row(expanded = True, main_align = "start", children = [
                render.Padding(pad = (1, 0, 0, 0), child = render.Text(row["label"], font = SMALL_FONT, color = row.get("label_color", GOLD))),
            ]),
        ))
    return render.Box(width = 64, height = row["height"], color = row.get("bg", BG), child = render.Stack(children = layers))

def header_widget(label, right):
    children = [render.Padding(pad = (1, 0, 0, 0), child = render.Text(label, font = SMALL_FONT, color = GOLD))]
    if right:
        children.append(render.Padding(pad = (0, 0, 1, 0), child = render.Text(right, font = SMALL_FONT, color = WHITE)))
    return render.Box(
        width = 64,
        height = HEADER_HEIGHT,
        color = HEADER_BG,
        child = render.Row(expanded = True, main_align = "space_between", cross_align = "center", children = children),
    )

def small_row(text, color = GREY):
    return {"text": text, "font": SMALL_FONT, "advance": SMALL_ADVANCE, "color": color, "height": ROW_HEIGHT}

def labeled(row, label, label_color):
    row["label"] = label
    row["label_width"] = 21
    row["label_color"] = label_color
    return row

# Upcoming: date in gold, title in white.
def upcoming_row(event):
    return labeled(small_row(title_of(event), WHITE), short_date(event["start"]), GOLD)

# Past: everything dimmed.
def past_row(event):
    return labeled(small_row(title_of(event), DIM), short_date(event["start"]), DIM)

# Today: a highlighted band, gold when there is a show and navy when there is not.
def today_row(event):
    if event:
        row = small_row(clock(event["start"]) + " " + title_of(event), BG)
        row["bg"] = GOLD
        return labeled(row, "TODAY", BG)
    row = small_row("NO SHOW", GREY)
    row["bg"] = HEADER_BG
    return labeled(row, "TODAY", GOLD)

# A screen is {header, right, rows}.
def screen_frames(screen):
    count = MIN_SCREEN_FRAMES
    for row in screen["rows"]:
        needed = frames_needed(row_overflow(row))
        if needed > count:
            count = needed
    if count > MAX_SCREEN_FRAMES:
        count = MAX_SCREEN_FRAMES
    frames = []
    for k in range(count):
        body = [row_widget(row, k) for row in screen["rows"]]
        top = []
        if screen["header"]:
            top = [header_widget(screen["header"], screen["right"]), render.Box(width = 64, height = 1)]
        frames.append(render.Column(children = top + body))
    return frames

# One screen read top to bottom as a timeline: the last past event (dimmed), today (a
# highlighted band), then what is coming. Four rows fill the 32 pixels exactly.
def home_screen(feed):
    tonight = feed["tonight"][0] if feed["tonight"] else None
    past = feed.get("recent_past", [])[:1]
    upcoming = feed["upcoming"]
    rows = [past_row(e) for e in past] + [today_row(tonight)]
    rows = rows + [upcoming_row(e) for e in upcoming[:4 - len(rows)]]
    return {"header": "", "right": "", "rows": rows}

def error_screen():
    return {"header": "BARCLAYS CENTER", "right": "", "rows": [small_row("BACKEND DOWN")]}

def main(config):
    feed = fetch_feed(config.str("backend", DEFAULT_BACKEND))
    if feed == None:
        screens = [error_screen()]
    else:
        screens = [home_screen(feed)]

    frames = []
    for screen in screens:
        frames = frames + screen_frames(screen)
    return render.Root(
        delay = FRAME_DELAY_MS,
        show_full_animation = True,
        child = render.Animation(children = frames),
    )

def get_schema():
    return schema.Schema(
        version = "1",
        fields = [
            schema.Text(
                id = "backend",
                name = "Backend URL",
                desc = "Where the Barclays Center feed is served",
                icon = "link",
                default = DEFAULT_BACKEND,
            ),
        ],
    )
