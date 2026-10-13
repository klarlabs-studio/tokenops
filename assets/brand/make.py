"""Draws the tokenops logo files: the lowercase wordmark outlined from Geist
SemiBold, with the o of "ops" replaced by the headroom ring sized to the
real o, and the ring alone as mark, app icon and favicon.

usage: python make.py <Geist[wght].ttf> <out-dir>   (needs fonttools)
Geist: https://github.com/google/fonts/tree/main/ofl/geist (SIL OFL 1.1)
"""
import math, os
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.transformPen import TransformPen

here = os.path.dirname(__file__)
import sys
font_path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(here, "Geist.ttf")
out = sys.argv[2] if len(sys.argv) > 2 else os.path.join(here, "out")
font = instantiateVariableFont(TTFont(font_path), {"wght": 600})
gs, cmap, hmtx = font.getGlyphSet(), font.getBestCmap(), font["hmtx"]
upm = font["head"].unitsPerEm
track = -0.035 * upm

def bounds(name):
    bp = BoundsPen(gs); gs[name].draw(bp); return bp.bounds

# The o's outer box and stroke: the ring takes exactly its place.
o = cmap[ord("o")]
ox0, oy0, ox1, oy1 = bounds(o)
# The o is thicker at its sides (133 units at SemiBold) than at top and
# bottom (104); an even ring matches it optically a little above the mean.
stroke = 112
print("o box", ox0, oy0, ox1, oy1, "stroke", stroke, "upm", upm)

def glyph_path(ch, x):
    sp = SVGPathPen(gs)
    gs[cmap[ord(ch)]].draw(TransformPen(sp, (1, 0, 0, -1, x, 0)))
    return sp.getCommands()

def arc(cx, cy, r, start_deg=-90, sweep=275):
    a0, a1 = math.radians(start_deg), math.radians(start_deg + sweep)
    p = lambda a: (cx + r * math.cos(a), cy + r * math.sin(a))
    (x0, y0), (x1, y1) = p(a0), p(a1)
    return f"M{x0:.2f} {y0:.2f}A{r:.2f} {r:.2f} 0 {1 if sweep > 180 else 0} 1 {x1:.2f} {y1:.2f}"

def ring(cx, cy, d, sw, arc_c, track_c, track_o, dot_c, dot=True):
    r = (d - sw) / 2
    out = (f'<circle cx="{cx:.2f}" cy="{cy:.2f}" r="{r:.2f}" fill="none" stroke="{track_c}" stroke-opacity="{track_o}" stroke-width="{sw:.2f}"/>'
           f'<path d="{arc(cx, cy, r)}" fill="none" stroke="{arc_c}" stroke-width="{sw:.2f}" stroke-linecap="round"/>')
    if dot:
        out += f'<circle cx="{cx:.2f}" cy="{cy:.2f}" r="{(d / 2 - sw) * 0.36:.2f}" fill="{dot_c}"/>'
    return out

def wordmark(text_c, arc_c, track_c, track_o, dot_c):
    x, parts, ring_svg = 0.0, [], ""
    for i, ch in enumerate("tokenops"):
        name = cmap[ord(ch)]
        if i == 5:  # the o of "ops"
            d = ((ox1 - ox0) + (oy1 - oy0)) / 2
            cx, cy = x + (ox0 + ox1) / 2, -(oy0 + oy1) / 2
            ring_svg = ring(cx, cy, d, stroke, arc_c, track_c, track_o, dot_c)
        else:
            parts.append(glyph_path(ch, x))
        x += hmtx[name][0] + (track if i < 7 else 0)
    # tight box: ascender of t/k to baseline (descender of p)
    top = -max(bounds(cmap[ord(c)])[3] for c in "tokenps")
    bottom = -min(bounds(cmap[ord(c)])[1] for c in "tokenps")
    left = bounds(cmap[ord("t")])[0]
    w, h = x - left - hmtx[cmap[ord("s")]][0] + bounds(cmap[ord("s")])[2], bottom - top
    vb = f"{left:.0f} {top:.0f} {w:.0f} {h:.0f}"
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vb}" role="img" aria-label="tokenops">'
            f'<path fill="{text_c}" d="{" ".join(parts)}"/>{ring_svg}</svg>\n'), (w, h)

COBALT, COBALT_DARK, AMBER, INK = "#2742F5", "#6E83FF", "#F59E0B", "#0E1220"
svg, size = wordmark(INK, COBALT, COBALT, "0.16", AMBER); open(f"{out}/wordmark.svg", "w").write(svg)
svg, _ = wordmark("#FFFFFF", COBALT_DARK, "#FFFFFF", "0.14", AMBER); open(f"{out}/wordmark-dark.svg", "w").write(svg)
svg, _ = wordmark("#000000", "#000000", "#000000", "0.25", "#000000"); open(f"{out}/wordmark-mono.svg", "w").write(svg)
print("wordmark", size)

# The mark alone: the same ring proportions (stroke / diameter of the o).
ratio = stroke / (((ox1 - ox0) + (oy1 - oy0)) / 2)
def mark(arc_c, track_c, track_o, dot_c, bg=None, pad=0, inset=0, weight=1.0, dot=True):
    d = 64 - 2 * pad
    body = ring(32, 32, d, d * ratio * weight, arc_c, track_c, track_o, dot_c, dot)
    side = 64 - 2 * inset
    rect = f'<rect x="{inset}" y="{inset}" width="{side}" height="{side}" rx="{side * 0.2257:.2f}" fill="{bg}"/>' if bg else ""
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" role="img" aria-label="tokenops">{rect}{body}</svg>\n'
open(f"{out}/mark.svg", "w").write(mark(COBALT, COBALT, "0.16", AMBER, pad=2))
open(f"{out}/mark-dark.svg", "w").write(mark(COBALT_DARK, "#FFFFFF", "0.14", AMBER, pad=2))
open(f"{out}/mark-mono.svg", "w").write(mark("#000000", "#000000", "0.25", "#000000", pad=2))
open(f"{out}/app-icon.svg", "w").write(mark("#FFFFFF", "#FFFFFF", "0.25", AMBER, bg=COBALT, pad=14))
# Favicon: heavier ring, no dot, so it holds at 16 px.
open(f"{out}/favicon.svg", "w").write(mark("#FFFFFF", "#FFFFFF", "0.3", AMBER, bg=COBALT, pad=12, weight=1.45, dot=False))
# macOS app icon: the Big Sur grid keeps the tile 824 of 1024 (inset 100).
open(f"{out}/app-icon-macos.svg", "w").write(mark("#FFFFFF", "#FFFFFF", "0.25", AMBER, bg=COBALT, pad=19.5, inset=6.25))
# Monochrome in the text colour, for icon slots that tint (VS Code).
open(f"{out}/mark-currentcolor.svg", "w").write(mark("currentColor", "currentColor", "0.3", "currentColor", pad=3, weight=1.3))
print("ratio", ratio)

# Social card (1200x630, og:image and the GitHub social preview): the
# wordmark on ink with the headline, every letter outlined from Geist so the
# PNG does not depend on the fonts installed where it is rendered.
regular = instantiateVariableFont(TTFont(font_path), {"wght": 400})

def text_path(f, text, x, y, size, tracking=-0.02):
    fgs, fcmap, fhmtx = f.getGlyphSet(), f.getBestCmap(), f["hmtx"]
    s = size / upm
    sp = SVGPathPen(fgs)
    for ch in text:
        name = fcmap[ord(ch)]
        fgs[name].draw(TransformPen(sp, (s, 0, 0, -s, x, y)))
        x += (fhmtx[name][0] + tracking * upm) * s
    return sp.getCommands()

wm = wordmark("#FFFFFF", COBALT_DARK, "#FFFFFF", "0.14", AMBER)[0]
vb = wm.split('viewBox="')[1].split('"')[0]
inner = wm.split('aria-label="tokenops">')[1].rsplit("</svg>", 1)[0]
lines = ["Know what’s left.", "Set what’s allowed.", "Let your agents work."]
head = "".join(f'<path fill="#FFFFFF" d="{text_path(font, t, 80, 270 + i * 92, 78)}"/>' for i, t in enumerate(lines))
sub = text_path(regular, "Guardrails for coding agents · local-first · ~90 AI plans and providers", 80, 560, 32, 0)
open(f"{out}/social.svg", "w").write(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630" width="1200" height="630" role="img" aria-label="tokenops">'
    f'<rect width="1200" height="630" fill="{INK}"/>'
    f'<svg x="80" y="64" width="420" height="84" viewBox="{vb}" preserveAspectRatio="xMinYMid meet">{inner}</svg>'
    f'{head}<path fill="#8C9CFF" d="{sub}"/></svg>\n')
