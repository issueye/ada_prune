"""Generates the application icon: a rounded teal-to-cyan tile with a white "A".

Run from the repository root:

    python tools/logo/gen.py

Output: resources/icon.png (512x512 RGBA).
"""

from __future__ import annotations

import os
from pathlib import Path

from PIL import Image, ImageDraw

SIZE = 512
MARGIN = 24
RADIUS = 112
TOP = (13, 148, 136)      # teal-600
BOTTOM = (8, 145, 178)    # cyan-600


def gradient_tile(size: int) -> Image.Image:
    """Vertical gradient clipped to a rounded square."""
    tile = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    gradient = Image.new("RGBA", (size, size))
    for y in range(size):
        # ease the blend a little so the middle does not look flat
        t = y / (size - 1)
        t = t * t * (3 - 2 * t)
        color = tuple(int(TOP[i] + (BOTTOM[i] - TOP[i]) * t) for i in range(3))
        for x in range(size):
            gradient.putpixel((x, y), color + (255,))

    mask = Image.new("L", (size, size), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        (MARGIN, MARGIN, size - MARGIN - 1, size - MARGIN - 1),
        radius=RADIUS,
        fill=255,
    )
    tile.paste(gradient, (0, 0), mask)
    return tile


def draw_mark(tile: Image.Image) -> None:
    """Draws a geometric "A": two legs and a crossbar, softly rounded."""
    draw = ImageDraw.Draw(tile)
    white = (255, 255, 255, 245)

    cx = SIZE / 2
    top_y = 150
    base_y = 372
    half_width = 108
    stroke = 34

    # Two legs of the A.
    draw.line([(cx, top_y), (cx - half_width, base_y)], fill=white, width=stroke)
    draw.line([(cx, top_y), (cx + half_width, base_y)], fill=white, width=stroke)
    # Crossbar.
    draw.line(
        [(cx - half_width * 0.52, base_y - (base_y - top_y) * 0.34),
         (cx + half_width * 0.52, base_y - (base_y - top_y) * 0.34)],
        fill=white,
        width=stroke,
    )
    # Round the joints so the mark reads as one shape.
    for point in (
        (cx, top_y),
        (cx - half_width, base_y),
        (cx + half_width, base_y),
    ):
        r = stroke / 2
        draw.ellipse((point[0] - r, point[1] - r, point[0] + r, point[1] + r), fill=white)


def main() -> None:
    tile = gradient_tile(SIZE)
    draw_mark(tile)

    root = Path(__file__).resolve().parents[2]
    out = root / "resources" / "icon.png"
    os.makedirs(out.parent, exist_ok=True)
    tile.save(out)

    # A tray-sized copy keeps the same artwork at a legible size.
    tile.resize((128, 128), Image.LANCZOS).save(root / "resources" / "icon-small.png")
    print(f"wrote {out} ({out.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
