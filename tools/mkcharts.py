#!/usr/bin/env python3
"""Render the benchmark charts as static SVG, one file per theme.

GitHub strips scripts from SVG and ignores media queries inside it, so the theme
cannot be handled in-file: two files plus a <picture> in the README is the only
thing that actually works there.
"""
import pathlib

DATA = [
    dict(label="99 MB",   layer=108.3,  repush=4.2, dedup=96.1, chunks=(1, 29),
         ecr_push=5.3,  s3lo_push=1.8,  ecr_pull=1.46,  s3lo_pull=0.80),
    dict(label="499 MB",  layer=509.6,  repush=4.2, dedup=99.2, chunks=(1, 132),
         ecr_push=20.6, s3lo_push=9.1,  ecr_pull=4.81,  s3lo_pull=2.83),
    dict(label="999 MB",  layer=1014.0, repush=4.2, dedup=99.6, chunks=(1, 258),
         ecr_push=38.4, s3lo_push=23.3, ecr_pull=9.87,  s3lo_pull=8.11),
    dict(label="1795 MB", layer=1819.0, repush=4.2, dedup=99.8, chunks=(1, 429),
         ecr_push=58.9, s3lo_push=42.7, ecr_pull=15.65, s3lo_pull=15.14),
]

THEMES = {
    "light": dict(s3lo="#2a78d6", ecr="#eb6834", ink="#101312", ink2="#4d5350",
                  ink3="#767c78", grid="#e8eae8", axis="#d9dcd9", good="#0f7a4f"),
    "dark":  dict(s3lo="#3987e5", ecr="#d95926", ink="#f4f6f5", ink2="#b5bcb8",
                  ink3="#868d89", grid="#262a28", axis="#343836", good="#48b183"),
}

FONT = "ui-sans-serif,system-ui,-apple-system,Segoe UI,Roboto,sans-serif"
MONO = "ui-monospace,SFMono-Regular,Menlo,Consolas,monospace"

W, H = 760, 308
L, R, T, B = 62, 16, 34, 46
IW, IH = W - L - R, H - T - B


def head(t):
    # No background rect: the README's own surface shows through in both themes.
    return [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" '
            f'font-family="{FONT}" role="img">']


def grid(out, t, maxv, fmt):
    for i in range(5):
        y = T + IH - IH * i / 4
        out.append(f'<line x1="{L}" x2="{W-R}" y1="{y:.1f}" y2="{y:.1f}" '
                   f'stroke="{t["grid"]}" stroke-width="1"/>')
        out.append(f'<text x="{L-10}" y="{y+4:.1f}" text-anchor="end" font-size="11" '
                   f'font-family="{MONO}" fill="{t["ink3"]}">{fmt(maxv*i/4)}</text>')


def xlabels(out, t, gw):
    for i, d in enumerate(DATA):
        out.append(f'<text x="{L+gw*i+gw/2:.1f}" y="{H-18}" text-anchor="middle" '
                   f'font-size="12" fill="{t["ink2"]}">{d["label"]}</text>')
    out.append(f'<line x1="{L}" x2="{W-R}" y1="{T+IH}" y2="{T+IH}" '
               f'stroke="{t["axis"]}" stroke-width="1"/>')


def dedup_chart(theme):
    t = THEMES[theme]
    out = head(t)
    maxv = max(d["layer"] for d in DATA) * 1.16
    fmt = lambda v: f"{v:.0f}"
    out.append(f'<text x="{L}" y="20" font-size="13" font-weight="600" fill="{t["ink"]}">'
               f'Re-push after editing one file inside the image</text>')
    grid(out, t, maxv, fmt)
    gw = IW / len(DATA)
    bw = min(58, gw - 44)
    for i, d in enumerate(DATA):
        x = L + gw * i + (gw - bw) / 2
        hl = IH * d["layer"] / maxv
        hu = max(3.0, IH * d["repush"] / maxv)
        out.append(f'<rect x="{x:.1f}" y="{T+IH-hl:.1f}" width="{bw:.1f}" height="{hl:.1f}" '
                   f'rx="3" fill="{t["s3lo"]}" opacity="0.16"/>')
        out.append(f'<rect x="{x:.1f}" y="{T+IH-hu:.1f}" width="{bw:.1f}" height="{hu:.1f}" '
                   f'rx="3" fill="{t["s3lo"]}"/>')
        out.append(f'<text x="{x+bw/2:.1f}" y="{T+IH-hl-8:.1f}" text-anchor="middle" '
                   f'font-size="12" font-weight="650" font-family="{MONO}" '
                   f'fill="{t["good"]}">{d["dedup"]}%</text>')
        out.append(f'<text x="{x+bw/2:.1f}" y="{T+IH-hu-7:.1f}" text-anchor="middle" '
                   f'font-size="11" font-family="{MONO}" fill="{t["ink2"]}">{d["repush"]}</text>')
    xlabels(out, t, gw)
    out.append(f'<text x="{L}" y="{H-8}" font-size="11" fill="{t["ink3"]}">'
               f'MB uploaded (solid) against the layer being re-pushed (pale). '
               f'One chunk changes, whatever the layer weighs.</text>')
    out.append("</svg>")
    return "\n".join(out)


def versus_chart(theme, phase, title, caption):
    """Paired ECR/s3lo bars for one phase — push or pull read the same way."""
    t = THEMES[theme]
    ecr_key, s3lo_key = f"ecr_{phase}", f"s3lo_{phase}"
    out = head(t)
    maxv = max(max(d[ecr_key], d[s3lo_key]) for d in DATA) * 1.2
    fmt = lambda v: f"{v:.0f}s"
    out.append(f'<text x="{L}" y="20" font-size="13" font-weight="600" fill="{t["ink"]}">'
               f'{title}</text>')
    # Legend, so identity is never carried by colour alone.
    lx = W - R - 190
    out.append(f'<rect x="{lx}" y="12" width="10" height="10" rx="2" fill="{t["ecr"]}"/>')
    out.append(f'<text x="{lx+16}" y="21" font-size="11" fill="{t["ink2"]}">ECR</text>')
    out.append(f'<rect x="{lx+60}" y="12" width="10" height="10" rx="2" fill="{t["s3lo"]}"/>')
    out.append(f'<text x="{lx+76}" y="21" font-size="11" fill="{t["ink2"]}">s3lo</text>')
    grid(out, t, maxv, fmt)
    gw = IW / len(DATA)
    bw = min(34, (gw - 30) / 2)
    for i, d in enumerate(DATA):
        gx = L + gw * i + (gw - bw * 2 - 4) / 2
        for j, (key, col) in enumerate(((ecr_key, t["ecr"]), (s3lo_key, t["s3lo"]))):
            v = d[key]
            h = max(2.0, IH * v / maxv)
            x = gx + j * (bw + 4)
            out.append(f'<rect x="{x:.1f}" y="{T+IH-h:.1f}" width="{bw:.1f}" height="{h:.1f}" '
                       f'rx="3" fill="{col}"/>')
            out.append(f'<text x="{x+bw/2:.1f}" y="{T+IH-h-7:.1f}" text-anchor="middle" '
                       f'font-size="11" font-family="{MONO}" fill="{t["ink2"]}">{v}</text>')
    xlabels(out, t, gw)
    out.append(f'<text x="{L}" y="{H-8}" font-size="11" fill="{t["ink3"]}">{caption}</text>')
    out.append("</svg>")
    return "\n".join(out)


BED = "c6id.xlarge, us-east-1, real S3 and ECR."
dest = pathlib.Path(__file__).resolve().parent.parent / "assets"
dest.mkdir(parents=True, exist_ok=True)
for theme in ("light", "dark"):
    (dest / f"bench-dedup-{theme}.svg").write_text(dedup_chart(theme))
    (dest / f"bench-push-{theme}.svg").write_text(versus_chart(
        theme, "push", "First push of never-before-seen content",
        f"Seconds, lower is better. {BED}"))
    (dest / f"bench-pull-{theme}.svg").write_text(versus_chart(
        theme, "pull", "Cold pull into containerd",
        f"Seconds, lower is better, median of three. {BED}"))
print("wrote", *(p.name for p in sorted(dest.glob("bench-*.svg"))))
