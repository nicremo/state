"""Compose iPad App Store frames in the same visual language as the iPhone set.

The device screenshot is pasted unchanged; only the backdrop, the device shell
and the headline are drawn here, so the interface still shows what the app does.
"""
import pathlib
from PIL import Image, ImageDraw, ImageFilter, ImageFont

SRC = pathlib.Path("/private/tmp/claude-501/-Users-nicremo-Documents-Codex-2026-08-11-gib-state/91ca112e-0c4d-46e3-a33f-30a0e56a9018/scratchpad/ipad")
OUT = pathlib.Path("/private/tmp/claude-501/-Users-nicremo-Documents-Codex-2026-08-11-gib-state/91ca112e-0c4d-46e3-a33f-30a0e56a9018/scratchpad/store-ipad")
W, H = 2064, 2752
BG = (22, 22, 27)
GLOW = (86, 66, 220)
FONT = "/System/Library/Fonts/SFNS.ttf"

COPY = {
    "en-US": {
        "01-today": ("Your agents remember", "Every reminder in one place"),
        "02-planned": ("Recurring work, handled", "Daily, weekly, monthly, yearly"),
        "03-activity": ("Every change is signed", "A complete audit history"),
        "04-settings": ("Connect every agent", "Codex, Claude Code, OpenCode"),
    },
    "de-DE": {
        "01-today": ("Deine Agenten erinnern sich", "Alle Erinnerungen an einem Ort"),
        "02-planned": ("Wiederkehrendes erledigt", "Täglich bis jährlich"),
        "03-activity": ("Jede Änderung signiert", "Vollständiger Verlauf"),
        "04-settings": ("Jeden Agenten verbinden", "Codex, Claude Code, OpenCode"),
    },
}


def font(size, weight):
    f = ImageFont.truetype(FONT, size)
    f.set_variation_by_name(weight)
    return f


def fit(draw, text, weight, max_width, start, minimum):
    """Largest size at which the line still fits the given width."""
    size = start
    while size > minimum:
        f = font(size, weight)
        if draw.textlength(text, font=f) <= max_width:
            return f
        size -= 4
    return font(minimum, weight)


def backdrop():
    base = Image.new("RGB", (W, H), BG)
    glow = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(glow)
    cx, cy, r = W // 2, int(H * 0.62), int(W * 0.46)
    d.ellipse((cx - r, cy - r, cx + r, cy + r), fill=GLOW)
    glow = glow.filter(ImageFilter.GaussianBlur(radius=int(W * 0.15)))
    return Image.blend(base, glow, 0.5)


def device(shot, width):
    scaled = shot.resize((width, round(shot.height * width / shot.width)), Image.LANCZOS)
    radius = int(width * 0.035)
    mask = Image.new("L", scaled.size, 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, scaled.width - 1, scaled.height - 1), radius, fill=255)
    bezel = 14
    shell = Image.new("RGBA", (scaled.width + bezel * 2, scaled.height + bezel * 2), (0, 0, 0, 0))
    ImageDraw.Draw(shell).rounded_rectangle(
        (0, 0, shell.width - 1, shell.height - 1), radius + bezel, fill=(46, 46, 52, 255)
    )
    shell.paste(scaled, (bezel, bezel), mask)
    return shell


def compose(src, dst, headline, subline):
    canvas = backdrop()
    draw = ImageDraw.Draw(canvas)

    head_font = fit(draw, headline, "Bold", int(W * 0.86), 132, 72)
    sub_font = fit(draw, subline, "Regular", int(W * 0.80), 76, 44)
    y = int(H * 0.075)
    draw.text((W / 2, y), headline, font=head_font, fill=(255, 255, 255), anchor="ma")
    y += head_font.size + int(H * 0.018)
    draw.text((W / 2, y), subline, font=sub_font, fill=(163, 163, 173), anchor="ma")

    shell = device(Image.open(src).convert("RGB"), int(W * 0.72))
    top = int(H * 0.235)
    shadow = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    ImageDraw.Draw(shadow).rounded_rectangle(
        ((W - shell.width) // 2, top + 26, (W + shell.width) // 2, top + shell.height + 26),
        int(W * 0.05), fill=(0, 0, 0, 150),
    )
    canvas = Image.alpha_composite(
        canvas.convert("RGBA"), shadow.filter(ImageFilter.GaussianBlur(40))
    )
    canvas.alpha_composite(shell, ((W - shell.width) // 2, top))
    canvas.convert("RGB").save(dst, "PNG")


OUT.mkdir(parents=True, exist_ok=True)
for locale, screens in COPY.items():
    (OUT / locale).mkdir(exist_ok=True)
    for name, (headline, subline) in screens.items():
        dst = OUT / locale / f"iPad Pro 13-inch-{name}.png"
        compose(SRC / locale / f"{name}.png", dst, headline, subline)
        print(locale, dst.name, Image.open(dst).size)
