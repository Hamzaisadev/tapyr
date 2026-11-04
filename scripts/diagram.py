"""Draws the diagrams of the README in the style of tapyr.eu, each in a light
and a dark version: the overview (provider APIs, Tapyr, outputs) and How it
works. The text is converted to paths with the fonts of the web UI, so that
no font is embedded or loaded.

    python3 -m venv .venv && .venv/bin/pip install fonttools brotli
    (cd ui && npm ci)
    .venv/bin/python scripts/diagram.py ui/node_modules/@fontsource-variable docs/assets
"""
import re
import sys

from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

ui = sys.argv[1]  # ui/node_modules/@fontsource-variable
out = sys.argv[2]

SANS = f"{ui}/bricolage-grotesque/files/bricolage-grotesque-latin-wght-normal.woff2"
MONO = f"{ui}/jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2"
_fonts = {}


def font(path, wght):
    """An instance of a variable font at a weight, with its kerning pairs."""
    if (path, wght) not in _fonts:
        f = instancer.instantiateVariableFont(TTFont(path), {"wght": wght})
        _fonts[(path, wght)] = (f, kerning(f))
    return _fonts[(path, wght)]


def kerning(f):
    """Pair kerning from the GPOS kern feature (format 1 and 2 pair adjustments)."""
    pairs, classes = {}, []
    if "GPOS" not in f:
        return pairs, classes
    gpos = f["GPOS"].table
    for fr in gpos.FeatureList.FeatureRecord:
        if fr.FeatureTag != "kern":
            continue
        for li in fr.Feature.LookupListIndex:
            lookup = gpos.LookupList.Lookup[li]
            subs = lookup.SubTable
            if lookup.LookupType == 9:
                subs = [s.ExtSubTable for s in subs]
            elif lookup.LookupType != 2:
                continue
            for st in subs:
                if st.Format == 1:
                    for i, first in enumerate(st.Coverage.glyphs):
                        for pvr in st.PairSet[i].PairValueRecord:
                            v = getattr(pvr.Value1, "XAdvance", 0) if pvr.Value1 else 0
                            if v:
                                pairs.setdefault((first, pvr.SecondGlyph), v)
                elif st.Format == 2:
                    classes.append(st)
    return pairs, classes


def kern(kd, a, b):
    pairs, classes = kd
    if (a, b) in pairs:
        return pairs[(a, b)]
    for st in classes:
        if a not in st.Coverage.glyphs:
            continue
        rec = st.Class1Record[st.ClassDef1.classDefs.get(a, 0)].Class2Record[st.ClassDef2.classDefs.get(b, 0)]
        v = getattr(rec.Value1, "XAdvance", 0) if rec.Value1 else 0
        if v:
            return v
    return 0


def rounded(d):
    """Rounds the coordinates of a path: the files stay small and stable."""
    return re.sub(r"-?\d+\.\d+", lambda m: f"{float(m.group(0)):.1f}".rstrip("0").rstrip("."), d)


def text(path, wght, size, x, y, s, fill, anchor="start", spacing=0.0):
    """A text as a path; spacing is the letter spacing in em."""
    f, kd = font(path, wght)
    cmap, gs = f.getBestCmap(), f.getGlyphSet()
    scale = size / f["head"].unitsPerEm
    glyphs = [cmap[ord(ch)] for ch in s]
    advances, cursor, prev = [], 0.0, None
    for g in glyphs:
        if prev:
            cursor += kern(kd, prev, g) + spacing * f["head"].unitsPerEm
        advances.append(cursor)
        cursor += f["hmtx"][g][0]
        prev = g
    if anchor == "middle":
        x -= cursor * scale / 2
    pen = SVGPathPen(gs)
    for g, at in zip(glyphs, advances):
        gs[g].draw(TransformPen(pen, (scale, 0, 0, -scale, x + at * scale, y)))
    return f'<path fill="{fill}" d="{rounded(pen.getCommands())}"/>'


def mix(a, b, t):
    """color-mix(in srgb, a t, b)."""
    ca = [int(a[i:i + 2], 16) for i in (1, 3, 5)]
    cb = [int(b[i:i + 2], 16) for i in (1, 3, 5)]
    return "#" + "".join(f"{round(x * t + y * (1 - t)):02x}" for x, y in zip(ca, cb))


def write(name, theme, width, height, title, body):
    pad = 32
    svg = [
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{-pad} {-pad} {width + 2 * pad} {height + 2 * pad}" '
        f'width="{width + 2 * pad}" height="{height + 2 * pad}" role="img" aria-labelledby="title">',
        f'<title id="title">{title}</title>',
        f'<rect x="{-pad}" y="{-pad}" width="{width + 2 * pad}" height="{height + 2 * pad}" rx="28" fill="{themes[theme]["surface"]}"/>',
        *body,
        "</svg>",
    ]
    with open(f"{out}/{name}-{theme}.svg", "w") as fh:
        fh.write("\n".join(svg) + "\n")


themes = {
    "light": dict(bg="#f2f8fb", surface="#ffffff", ink="#0f2a3f", ink2="#44606f", line="#d3e4ec",
                  cyan="#3aa9d6", cyan2="#2389b4", sun="#ffc845",
                  body="#48b3dc", body2="#2f93bd", saddle="#fffdf8", tline="#0f2a3f", brace="#0f2a3f"),
    "dark": dict(bg="#0b1824", surface="#102335", ink="#e4f1f7", ink2="#9db7c4", line="#1f3a50",
                 cyan="#5cc3ea", cyan2="#3aa9d6", sun="#ffd166",
                 body="#48b3dc", body2="#2f93bd", saddle="#f6f2e9", tline="#04101a", brace="#cfe9f5"),
}

# ---------------------------------------------------------------- overview

ICONS = {
    "sdwan": '<g fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M6 6.5l4.3 4.2M6 17.5l4.3-4.2M14.2 12H18"/><circle cx="12" cy="12" r="2.4"/><circle cx="4.6" cy="5.2" r="2"/><circle cx="4.6" cy="18.8" r="2"/><circle cx="20" cy="12" r="2"/></g>',
    "switch": '<g fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="2.5" y="8" width="19" height="8" rx="2"/><path d="M6 11v2M9 11v2M12 11v2M15 11v2"/></g><circle cx="18.5" cy="12" r="1" fill="currentColor"/>',
    "wifi": '<g fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4.5 10.5a10.5 10.5 0 0 1 15 0M7.5 13.5a6.3 6.3 0 0 1 9 0M10.5 16.5a2.1 2.1 0 0 1 3 0"/></g><circle cx="12" cy="19.2" r="1.2" fill="currentColor"/>',
    "router": '<g fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="12" width="18" height="7" rx="2"/><path d="M7 12 5.5 5M17 12l1.5-7"/></g><g fill="currentColor"><circle cx="7" cy="15.5" r="1"/><circle cx="10" cy="15.5" r="1"/></g>',
    "braces": '<g fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 4C7 4 7 5 7 7v2c0 1.5-.8 3-2.5 3C6.2 12 7 13.5 7 15v2c0 2 0 3 2 3M15 4c2 0 2 1 2 3v2c0 1.5.8 3 2.5 3-1.7 0-2.5 1.5-2.5 3v2c0 2 0 3-2 3"/><path d="M12 9.5v5M9.5 12h5"/></g>',
    "plus": '<g fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 7v10M7 12h10"/><circle cx="12" cy="12" r="9" stroke-dasharray="3 3"/></g>',
}
# Simple Icons (CC0); the logos are trademarks of their owners.
LOGOS = {
    "prometheus": '<path fill="#E6522C" d="M12 0C5.373 0 0 5.372 0 12c0 6.627 5.373 12 12 12s12-5.373 12-12c0-6.628-5.373-12-12-12zm0 22.46c-1.885 0-3.414-1.26-3.414-2.814h6.828c0 1.553-1.528 2.813-3.414 2.813zm5.64-3.745H6.36v-2.046h11.28v2.046zm-.04-3.098H6.391c-.037-.043-.075-.086-.111-.13-1.155-1.401-1.427-2.133-1.69-2.879-.005-.025 1.4.287 2.395.511 0 0 .513.119 1.262.255-.72-.843-1.147-1.915-1.147-3.01 0-2.406 1.845-4.508 1.18-6.207.648.053 1.34 1.367 1.387 3.422.689-.951.977-2.69.977-3.755 0-1.103.727-2.385 1.454-2.429-.648 1.069.168 1.984.894 4.256.272.854.237 2.29.447 3.201.07-1.892.395-4.652 1.595-5.605-.529 1.2.079 2.702.494 3.424.671 1.164 1.078 2.047 1.078 3.716a4.642 4.642 0 01-1.11 2.996c.792-.149 1.34-.283 1.34-.283l2.573-.502s-.374 1.538-1.81 3.019z"/>',
    "opentelemetry": '<path fill="#1f2a44" d="M12.6974 13.1173c-1.0224 1.0224-1.0224 2.68 0 3.7024 1.0224 1.0224 2.68 1.0224 3.7024 0 1.0224-1.0223 1.0224-2.68 0-3.7024-1.0223-1.0223-2.68-1.0223-3.7024 0zm2.7677 2.7701c-.5063.5063-1.3267.5063-1.833 0s-.5063-1.3266 0-1.833c.5063-.5062 1.3267-.5062 1.833 0 .5063.504.5063 1.3267 0 1.833zM16.356.2355l-1.6041 1.6042c-.314.314-.314.83 0 1.144L21.015 9.247c.314.314.83.314 1.144 0l1.6042-1.6041c.314-.314.314-.83 0-1.144L17.4976.2354c-.314-.314-.8276-.314-1.1416 0zM5.1173 20.734c.2848-.2848.2848-.7497 0-1.0345l-.8155-.8155c-.2848-.2848-.7497-.2848-1.0345 0l-1.6845 1.6845-.0024.0024-.4625-.4625c-.2556-.2556-.6718-.2556-.925 0-.2556.2556-.2556.6718 0 .925l2.775 2.775c.2556.2556.6718.2556.925 0 .2532-.2556.2556-.6718 0-.925l-.4625-.4625.0024-.0024zm8.4856-15.893-3.5637 3.5637c-.3164.3164-.3164.8374 0 1.1538l2.2006 2.2005c1.5554-1.1197 3.7365-.981 5.1361.4187l1.7819-1.7818c.3164-.3165.3164-.8374 0-1.1538l-4.401-4.401c-.3165-.319-.8374-.319-1.1539 0zm-2.2881 7.8455-1.2999-1.2999c-.3043-.3043-.8033-.3043-1.1076 0l-4.5836 4.586c-.3042.3043-.3042.8033 0 1.1076l2.5973 2.5973c.3043.3043.8033.3043 1.1076 0l2.9478-2.9527c-.6231-1.2877-.5112-2.8431.3384-4.0383z"/>',
}
TAPIR_BODY = ("M28 42 C36 28 66 24 94 27 C104 28 111 31 116 35 C121 33 128 33 134 37 C140 41 144 46 148 52 "
              "C152 57 155 62 154 68 C153 72 148 73 147 69 C146 65 142 63 138 64 C135 67 131 69 127 68 "
              "C123 70 119 73 117 77 C115 82 110 84 104 84 L46 84 C32 84 22 74 22 62 C22 52 24 46 28 42 Z")


def tapir(c):
    """The mascot of tapyr.eu, sniffing out data: 186 x 100."""
    return f"""<defs><clipPath id="body"><path d="{TAPIR_BODY}"/></clipPath></defs>
<g stroke="{c['tline']}" stroke-width="3" stroke-linejoin="round" stroke-linecap="round">
<rect x="106" y="72" width="11" height="23" rx="5" fill="{c['body2']}"/>
<rect x="44" y="72" width="11" height="23" rx="5" fill="{c['body2']}"/>
<ellipse cx="120" cy="31" rx="6" ry="9.5" transform="rotate(14 120 31)" fill="{c['body']}"/>
<path d="{TAPIR_BODY}" fill="{c['body']}"/>
<path clip-path="url(#body)" d="M70 16 C62 40 60 64 64 92 L0 92 L0 16 Z" fill="{c['saddle']}" stroke="none"/>
<path d="{TAPIR_BODY}" fill="none"/>
<rect x="96" y="74" width="12" height="24" rx="5.5" fill="{c['body']}"/>
<rect x="33" y="74" width="12" height="24" rx="5.5" fill="{c['body']}"/>
</g>
<ellipse cx="120.2" cy="31.6" rx="3.6" ry="6.6" transform="rotate(14 120.2 31.6)" fill="none" stroke="{c['saddle']}" stroke-width="2"/>
<ellipse cx="151" cy="69.5" rx="1.1" ry="1.7" fill="{c['tline']}" opacity=".55"/>
<ellipse cx="133" cy="46" rx="3.6" ry="3.6" fill="#ffffff"/>
<ellipse cx="134" cy="46" rx="2.2" ry="2.2" fill="{c['tline']}"/>
<g fill="none" stroke="{c['brace']}" stroke-width="2.8" stroke-linecap="round" stroke-linejoin="round">
<path d="M156 9 C152 9 152 11 152 14 L152 18 C152 20 151 21 149 21 C151 21 152 22 152 24 L152 28 C152 31 152 33 156 33"/>
<path d="M176 9 C180 9 180 11 180 14 L180 18 C180 20 181 21 183 21 C181 21 180 22 180 24 L180 28 C180 31 180 33 176 33"/>
</g>
<g fill="{c['sun']}"><circle cx="160.5" cy="21" r="2.4"/><circle cx="166" cy="21" r="2.4"/><circle cx="171.5" cy="21" r="2.4"/></g>"""


def bezier(x0, y0, x1, y1, t):
    """A point of the wire from (x0, y0) to (x1, y1), drawn as on the website."""
    mx = (x0 + x1) / 2
    pts = [(x0, y0), (mx, y0), (mx, y1), (x1, y1)]
    u = 1 - t
    w = [u ** 3, 3 * u * u * t, 3 * u * t * t, t ** 3]
    return sum(p[0] * k for p, k in zip(pts, w)), sum(p[1] * k for p, k in zip(pts, w))


sources = [  # y, icon, title, subtitle, yours
    (20, "sdwan", "SD-WAN", "branch appliances", False),
    (92, "switch", "SD-LAN", "cloud-managed switches", False),
    (164, "wifi", "Wi-Fi", "access point controllers", False),
    (236, "router", "4G / 5G routers", "cellular gateways", False),
    (308, "braces", "Any SD-* API", "your definition next", True),
]
outputs = [  # y, logo or icon, title, subtitle, yours
    (92, "prometheus", "Prometheus", "exposition, remote write", False),
    (164, "opentelemetry", "OTLP", "any OTLP backend", False),
    (236, "plus", "Other formats?", "open to proposals", True),
]
overview_title = ("Cloud APIs of SD-WAN, SD-LAN, Wi-Fi, 4G and 5G routers and any other platform feed Tapyr, "
                  "which polls, paginates and retries, normalizes labels and units, and spends the API quota on "
                  "a budget, then exposes or pushes the metrics as Prometheus and OTLP.")


def node(c, x, y, kind, icon, t, s):
    out = []
    if kind == "yours":
        out.append(f'<rect x="{x}" y="{y}" width="248" height="56" rx="16" fill="none" stroke="{c["ink2"]}" stroke-width="2" stroke-dasharray="6 6"/>')
    elif kind == "out":
        out.append(f'<rect x="{x}" y="{y}" width="248" height="56" rx="16" fill="{mix(c["cyan"], c["surface"], .22)}" '
                   f'stroke="{c["cyan"]}" stroke-opacity=".6" stroke-width="2"/>')
    else:
        out.append(f'<rect x="{x}" y="{y}" width="248" height="56" rx="16" fill="{c["bg"]}" stroke="{c["line"]}" stroke-width="2"/>')
    if icon in LOGOS:
        out.append(f'<rect x="{x + 14}" y="{y + 10}" width="36" height="36" rx="10" fill="#ffffff" stroke="{c["line"]}"/>')
        out.append(f'<g transform="translate({x + 20} {y + 16}) scale(1)">{LOGOS[icon]}</g>')
    else:
        bg = c["surface"] if kind == "out" else mix(c["cyan"], c["surface"], .18)
        out.append(f'<rect x="{x + 14}" y="{y + 10}" width="36" height="36" rx="10" fill="{bg}"/>')
        out.append(f'<g transform="translate({x + 20} {y + 16})" color="{c["cyan2"]}">{ICONS[icon]}</g>')
    out.append(text(SANS, 700, 17, x + 62, y + 25, t, c["ink2"] if kind == "yours" else c["ink"]))
    out.append(text(MONO, 500, 12, x + 62, y + 44, s, c["ink2"]))
    return out


for theme, c in themes.items():
    body = []
    for i, (y, *_, yours) in enumerate(sources):
        hub_y = 132 + 30 * i
        dash = ' stroke-dasharray="5 6"' if yours else ""
        body.append(f'<path d="M250 {y + 28} C330 {y + 28} 330 {hub_y} 410 {hub_y}" fill="none" stroke="{c["ink2"]}" '
                    f'stroke-opacity=".45" stroke-width="2"{dash}/>')
    for i, (y, *_, yours) in enumerate(outputs):
        hub_y = 162 + 30 * i
        style = f'stroke="{c["ink2"]}" stroke-opacity=".45" stroke-dasharray="5 6"' if yours else f'stroke="{c["cyan2"]}" stroke-opacity=".8"'
        body.append(f'<path d="M690 {hub_y} C770 {hub_y} 770 {y + 28} 850 {y + 28}" fill="none" {style} stroke-width="2"/>')
    # The website animates data along the wires; a dot on each one here.
    for i, (y, *_, yours) in enumerate(sources):
        if not yours:
            px, py = bezier(250, y + 28, 410, 132 + 30 * i, (0.35, 0.55, 0.7, 0.45)[i])
            body.append(f'<circle cx="{px:.1f}" cy="{py:.1f}" r="4.5" fill="{c["sun"]}"/>')
    for i, (y, *_, yours) in enumerate(outputs):
        if not yours:
            px, py = bezier(690, 162 + 30 * i, 850, y + 28, (0.6, 0.4)[i])
            body.append(f'<circle cx="{px:.1f}" cy="{py:.1f}" r="5" fill="{c["cyan"]}"/>')
    for y, icon, t, s, yours in sources:
        body += node(c, 2, y, "yours" if yours else "", icon, t, s)
    for y, icon, t, s, yours in outputs:
        body += node(c, 850, y, "yours" if yours else "out", icon, t, s)
    body.append(f'<rect x="410" y="104" width="280" height="176" rx="28" fill="{mix(c["cyan"], c["surface"], .12)}" '
                f'stroke="{c["ink"]}" stroke-width="2.5"/>')
    body.append(text(SANS, 780, 24, 550, 150, "tapyr", c["ink"], anchor="middle", spacing=-0.02))
    for i, line in enumerate(["poll, paginate, retry", "normalize labels, units", "spend quota on budget"]):
        body.append(f'<circle cx="440" cy="{180 + 32 * i}" r="4" fill="{c["cyan2"]}"/>')
        body.append(text(MONO, 500, 12.5, 454, 184 + 32 * i, line, c["ink"]))
    body.append(f'<g transform="translate(472 17) scale(.89)">{tapir(c)}</g>')
    write("overview", theme, 1100, 368, overview_title, body)

# ------------------------------------------------------------ how it works

boxes = [  # x, y, w, h, kind, title, subtitle
    (0, 160, 192, 84, "", "Definition", "YAML + CEL per API"),
    (227, 160, 192, 84, "key", "Budget engine", "plans every call"),
    (454, 160, 192, 84, "", "Collector", "poll, paginate, retry"),
    (681, 160, 192, 84, "", "Mapping", "CEL to metrics"),
    (908, 160, 192, 84, "", "Outputs", "Prometheus, OTLP"),
    (454, 20, 192, 76, "side", "Vendor API", "or built-in simulator"),
    (0, 300, 419, 76, "side", "tapyr plan", "projects calls per quota, before you deploy"),
    (454, 300, 419, 76, "side", "Self-metrics and web UI", "calls, 429s, quota left, job freshness"),
]
arrows = [  # d, soft
    ("M196 202 H221", False), ("M423 202 H448", False), ("M650 202 H675", False), ("M877 202 H902", False),
    ("M536 100 V154", True), ("M564 156 V102", True), ("M323 248 V294", True), ("M550 248 V294", True),
    ("M877 338 H1004 V250", True),
]
how_title = ("A provider definition feeds the budget engine, which plans every call of the collector. "
             "The collector calls the vendor API or the built-in simulator, the CEL mapping turns the answers "
             "into metrics, and the outputs deliver them as Prometheus or OTLP. tapyr plan projects the calls "
             "per quota before you deploy, and Tapyr exposes its own metrics and a read-only web UI.")

for theme, c in themes.items():
    body = [f'<defs><marker id="head" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto">'
            f'<path d="M0 0 L10 5 L0 10 Z" fill="{c["ink"]}"/></marker></defs>']
    for x, y, w, h, kind, t, s in boxes:
        if kind == "key":
            box = f'fill="{c["ink"]}" stroke="{c["ink"]}" stroke-width="2"'
            tc = sc = c["bg"]
        elif kind == "side":
            box = f'fill="none" stroke="{c["ink2"]}" stroke-width="1.5" stroke-dasharray="6 6"'
            tc, sc = c["ink"], c["ink2"]
        else:
            box = f'fill="{c["bg"]}" stroke="{c["line"]}" stroke-width="2"'
            tc, sc = c["ink"], c["ink2"]
        ty, sy = y + (36 if h == 84 else 32), y + (60 if h == 84 else 56)
        body.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="18" {box}/>')
        body.append(text(SANS, 700, 17, x + 20, ty, t, tc))
        body.append(text(MONO, 500, 12, x + 20, sy, s, sc))
    for d, soft in arrows:
        stroke = f'stroke="{c["ink2"]}" stroke-dasharray="4 5"' if soft else f'stroke="{c["ink"]}"'
        body.append(f'<path d="{d}" {stroke} stroke-width="2" fill="none" marker-end="url(#head)"/>')
    write("how-it-works", theme, 1100, 380, how_title, body)
