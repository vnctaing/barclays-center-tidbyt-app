load("http.star", "http")
load("math.star", "math")
load("render.star", "render")
load("schema.star", "schema")
load("time.star", "time")

DEFAULT_BACKEND = "https://barclays-tidbyt.fly.dev/api/events"
CACHE_SECONDS = 1800

BG = "#000000"
HEADER_BG = "#14213d"
GOLD = "#fca311"
WHITE = "#ffffff"
GREY = "#9aa0a6"
DIM = "#80868b"

# Monospace fonts, so text width is exactly chars * advance and we know when to scroll.
TITLE_FONT = "5x8"
TITLE_ADVANCE = 5
SMALL_FONT = "tom-thumb"
SMALL_ADVANCE = 4

FRAME_DELAY_MS = 50
PAUSE_FRAMES = 24  # hold at the start and at the end of a scroll, 1.2s
MIN_SCREEN_FRAMES = 70
MAX_SCREEN_FRAMES = 260
HEADER_HEIGHT = 7
ROW_HEIGHT = 8

def fetch_feed(url):
    resp = http.get(url, ttl_seconds = CACHE_SECONDS)
    if resp.status_code != 200:
        return None
    return resp.json()

DATE_FORMAT = "2006-01-02"

# Whole calendar days from today (in the venue's timezone) to the event's date; negative when past.
def days_from_today(start, tz):
    today = time.parse_time(time.now().in_location(tz).format(DATE_FORMAT), format = DATE_FORMAT)
    day = time.parse_time(start[:10], format = DATE_FORMAT)
    return int(math.round((day - today).hours / 24))

def relative_label(days):
    if days < 0:
        return "YEST" if days == -1 else "%dD AGO" % -days
    if days == 0:
        return "TODAY"
    if days == 1:
        return "TMRW"
    if days < 14:
        return "IN %dD" % days
    if days < 63:
        return "IN %dW" % (days // 7)
    return "IN %dM" % (days // 30)

def title_of(event):
    return event["title"].upper()

# Pixels to shift left at frame k: hold, scroll, hold. A row scrolls speed pixels per 4 frames,
# 4 being 20px/s, the pace the upcoming rows use.
def scroll_offset(k, overflow, speed):
    if overflow <= 0 or k < PAUSE_FRAMES:
        return 0
    return min((k - PAUSE_FRAMES) * speed // 4, overflow)

def frames_needed(overflow, speed):
    if overflow <= 0:
        return 0
    return PAUSE_FRAMES + (overflow * 4 + speed - 1) // speed + PAUSE_FRAMES

# A row is a dict: text, font, advance, color, height, and optionally label (fixed on the left, with label_width).
def row_overflow(row):
    left = row.get("label_width", 0) + 1
    return row["advance"] * len(row["text"]) - (64 - left - 1)

def row_widget(row, k):
    label_width = row.get("label_width", 0)
    left = label_width + 1
    offset = scroll_offset(k, row_overflow(row), row.get("speed", 4))
    text = render.Padding(
        pad = (left - offset, 1, 0, 0),
        child = render.Text(row["text"], font = row["font"], color = row["color"]),
    )
    layers = [render.Row(expanded = True, main_align = "start", cross_align = "center", children = [text])]
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
    row["label_width"] = 4 * len(label) + 2  # raised to the widest label on the screen by align_labels
    row["label_color"] = label_color
    return row

# Upcoming: date in gold, title in white.
def upcoming_row(event, tz):
    return labeled(small_row(title_of(event), WHITE), relative_label(days_from_today(event["start"], tz)), GOLD)

# Past: everything dimmed.
def past_row(event, tz):
    return labeled(small_row(title_of(event), DIM), relative_label(days_from_today(event["start"], tz)), DIM)

# Today: a highlighted band, gold when there is a show and navy when there is not.
def today_row(event):
    if event:
        row = small_row(title_of(event), BG)
        row["bg"] = GOLD
        row["speed"] = 3  # 15px/s: the show is the one row worth reading slowly
        return labeled(row, "TODAY", BG)
    row = small_row("NO SHOW", GREY)
    row["bg"] = HEADER_BG
    return labeled(row, "TODAY", GOLD)

# A screen is {header, right, rows}.
def screen_frames(screen):
    count = MIN_SCREEN_FRAMES
    for row in screen["rows"]:
        needed = frames_needed(row_overflow(row), row.get("speed", 4))
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

# Titles start at the same x on every row, so every label gets the widest label's width.
def align_labels(rows):
    width = max([row["label_width"] for row in rows])
    for row in rows:
        row["label_width"] = width

# One screen read top to bottom as a timeline: the last past event (dimmed), today (a
# highlighted band), then what is coming. Four rows fill the 32 pixels exactly.
def home_screen(feed):
    tonight = feed["tonight"][0] if feed["tonight"] else None
    past = feed.get("recent_past", [])[:1]
    upcoming = feed["upcoming"]
    tz = feed["timezone"]
    rows = [past_row(e, tz) for e in past] + [today_row(tonight)]
    rows = rows + [upcoming_row(e, tz) for e in upcoming[:4 - len(rows)]]
    align_labels(rows)
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
