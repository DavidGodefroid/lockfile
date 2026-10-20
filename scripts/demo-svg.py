#!/usr/bin/env python3
"""Generates site/assets/demo.svg: lockfile's value in one looping image.

An agent asks three yes/no questions about an invoice it must never read. The
human approves the plan in the lockfile console, the console fetches the file
and runs three signed, sandboxed plugins on it, and only their answers cross
the socket back to the agent: 1, 1, 1. Pure SVG + CSS keyframes on one 14 s
clock, so it plays on GitHub, on the site and in any browser.

Every command, flag and output line is the CLI's real text (cmd/lockfile,
internal/backend/screen.go, the marketplace manifests); the agent's closing
remark is the only prose.

    python3 scripts/demo-svg.py > site/assets/demo.svg
"""

LOOP = 14.0
END = 13.4  # everything fades out here, then the loop restarts
C = dict(crust="#0B1120", base="#0F172A", mantle="#131C2E", s0="#1E293B", s1="#334155",
         ov="#64748B", sub="#94A3B8", text="#E2E8F0", blue="#60A5FA", deep="#3B82F6",
         teal="#5EEAD4", red="#F87171", green="#4ADE80", amber="#FBBF24")
FONT = "'JetBrains Mono','SF Mono',Menlo,Consolas,'DejaVu Sans Mono',monospace"
SANS = "Inter,'Segoe UI',system-ui,-apple-system,Helvetica,Arial,sans-serif"

css, body = [], []
n = 0


def p(t):
    return f"{t / LOOP * 100:.2f}%"


def anim(frames):
    """Registers keyframes [(time_s, 'css decls'), ...] and returns a class name."""
    global n
    n += 1
    name = f"k{n}"
    rules = " ".join(f"{p(t)}{{{d}}}" for t, d in frames)
    css.append(f"@keyframes {name}{{{rules}}}")
    css.append(f".{name}{{animation:{name} {LOOP}s linear infinite}}")
    return name


def show(t_in, t_out=END, fade=0.2):
    """Opacity window: hidden, visible from t_in to t_out."""
    return anim([(0, "opacity:0"), (t_in, "opacity:0"), (t_in + fade, "opacity:1"),
                 (t_out, "opacity:1"), (t_out + fade, "opacity:0"), (LOOP, "opacity:0")])


def flash(t, col, prop="stroke", base=C["s1"], hold=1.6):
    return anim([(0, f"{prop}:{base}"), (t, f"{prop}:{base}"), (t + 0.15, f"{prop}:{col}"),
                 (t + hold, f"{prop}:{col}"), (t + hold + 0.6, f"{prop}:{base}"),
                 (LOOP, f"{prop}:{base}")])


def move(t0, t1, x0, y0, x1, y1):
    return anim([(0, f"transform:translate({x0}px,{y0}px);opacity:0"),
                 (t0, f"transform:translate({x0}px,{y0}px);opacity:0"),
                 (t0 + 0.05, f"transform:translate({x0}px,{y0}px);opacity:1"),
                 (t1, f"transform:translate({x1}px,{y1}px);opacity:1"),
                 (t1 + 0.15, f"transform:translate({x1}px,{y1}px);opacity:0"),
                 (LOOP, f"transform:translate({x1}px,{y1}px);opacity:0")])


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def text(x, y, s, cls="", size=13, col=C["text"], weight=400, family=FONT, anchor="start"):
    return (f'<text x="{x}" y="{y}" class="{cls}" font-family="{family}" font-size="{size}" '
            f'fill="{col}" font-weight="{weight}" text-anchor="{anchor}" '
            f'xml:space="preserve">{esc(s)}</text>')


def spans(x, y, parts, cls="", size=13):
    """One line in several colours: parts = [(text, colour), ...]."""
    inner = "".join(f'<tspan fill="{col}">{esc(s)}</tspan>' for s, col in parts)
    return (f'<text x="{x}" y="{y}" class="{cls}" font-family="{FONT}" font-size="{size}" '
            f'xml:space="preserve">{inner}</text>')


def label(x, y, s, col=C["ov"]):
    return text(x, y, s, size=13, col=col, weight=700, family=SANS)


W, H = 1200, 640
body.append(f'<rect width="{W}" height="{H}" rx="24" fill="{C["crust"]}"/>')

# --- The agent's account (left) --------------------------------------------------------------
AX, AY, AW, AH = 30, 96, 450, 444
body.append(label(AX, 70, "YOUR AGENT · its own account"))
body.append(f'<rect x="{AX}" y="{AY}" width="{AW}" height="{AH}" rx="14" fill="{C["base"]}" '
            f'stroke="{C["s1"]}" stroke-width="1.5"/>')
body.append(text(AX + 18, AY + 30, "✻ Claude Code", size=13, col=C["amber"]))

cmd = [
    (0.3, [("> ", C["sub"]), ("lockfile plan \\", C["text"])]),
    (0.5, [("    --key ", C["text"]), ("invoices/2026/10/abc123.pdf \\", C["teal"])]),
    (0.7, [("    --plugin ", C["text"]), ("file.non-empty \\", C["blue"])]),
    (0.9, [("    --plugin ", C["text"]), ("pdf.parses \\", C["blue"])]),
    (1.1, [("    --plugin ", C["text"]), ("pdf.has-blank-page \\", C["blue"])]),
    (1.3, [("    --intent ", C["text"]), ('"invoice reported as blank" \\', C["sub"])]),
    (1.5, [("    --run", C["text"])]),
]
for i, (t, parts) in enumerate(cmd):
    body.append(spans(AX + 18, AY + 66 + i * 22, parts, cls=show(t)))

# The result: exactly what `lockfile plan --run` prints, one line per answer.
RY = AY + 238
result = [
    (8.5, [("handle ", C["sub"]), ("f_8Kq2xR", C["text"])]),
    (8.7, [("file.non-empty@1.0.0: ", C["text"]), ("1", C["green"])]),
    (8.9, [("pdf.parses@1.0.0: ", C["text"]), ("1", C["green"])]),
    (9.1, [("pdf.has-blank-page@1.0.0: ", C["text"]), ("1", C["green"])]),
]
for i, (t, parts) in enumerate(result):
    body.append(spans(AX + 18, RY + i * 22, parts, cls=show(t)))
body.append(text(AX + 18, RY + 104, "✻ It parses and has a blank page:", cls=show(9.8),
                 col=C["amber"]))
body.append(text(AX + 18, RY + 126, "  the bug is in rendering, not the upload.", cls=show(9.8),
                 col=C["amber"]))

# What never reaches the agent, struck out.
never = ["bytes", "name", "size", "type", "path"]
g = [f'<g class="{show(10.6)}">',
     text(AX + 18, AY + AH - 46, "never sent to the agent:", size=12, col=C["ov"], family=SANS)]
x = AX + 18
for s in never:
    w = 10 + len(s) * 8
    g.append(f'<rect x="{x}" y="{AY + AH - 34}" width="{w}" height="22" rx="11" '
             f'fill="none" stroke="{C["red"]}" stroke-width="1.2"/>')
    g.append(text(x + w / 2, AY + AH - 19, s, size=12, col=C["red"], anchor="middle"))
    g.append(f'<line x1="{x + 4}" y1="{AY + AH - 23}" x2="{x + w - 4}" y2="{AY + AH - 23}" '
             f'stroke="{C["red"]}" stroke-width="1.4"/>')
    x += w + 8
g.append("</g>")
body.append("".join(g))

# --- The socket between the two accounts -----------------------------------------------------
SX = 510
body.append(f'<line x1="{SX}" y1="{AY + 20}" x2="{SX}" y2="{AY + AH - 20}" stroke="{C["s1"]}" '
            f'stroke-width="2" stroke-dasharray="4 6"/>')
body.append(text(SX, AY + 6, "socket", size=12, col=C["ov"], weight=700, family=SANS,
                 anchor="middle"))

# --- The lockfile account (right) ------------------------------------------------------------
X0, Y0, X1, Y1 = 540, 96, 1170, 540
wall = flash(4.6, C["teal"], base=C["deep"], hold=0.5)
body.append(f'<rect x="{X0}" y="{Y0}" width="{X1 - X0}" height="{Y1 - Y0}" rx="20" '
            f'fill="{C["base"]}" stroke="{C["deep"]}" stroke-width="3" class="{wall}"/>')
body.append(f'<rect x="{X0 + 24}" y="{Y0 - 14}" width="290" height="28" rx="14" '
            f'fill="{C["deep"]}"/>')
body.append(text(X0 + 169, Y0 + 5, "lockfile account · your console", size=13, col=C["crust"],
                 weight=700, family=SANS, anchor="middle"))

# The approval screen, as the console draws it.
QX, QY = X0 + 20, Y0 + 32
body.append(f'<rect x="{QX}" y="{QY}" width="{X1 - X0 - 40}" height="240" rx="10" '
            f'fill="{C["crust"]}" stroke="{C["s1"]}" stroke-width="1.5"/>')
B = C["s1"]
screen = [
    [("╭─ ", B), ("SAMPLES · file plan · 3 answers", C["text"]), (" ─────", B)],
    [("│ ", B), ("requested by uid 1000 (alice) · pid 48211 · exe /usr/local/bin/lockfile", C["ov"])],
    [("│ ", B), ("agent: invoice reported as blank", C["sub"])],
    [("│ ", B), ("key    ", C["sub"]), ("invoices/2026/10/abc123.pdf", C["teal"])],
    [("│ ", B), ("file.non-empty@1.0.0  (official)", C["blue"])],
    [("│ ", B), ("  ? Does the file contain at least one byte?   1 = size > 0   0 = the…", C["text"])],
    [("│ ", B), ("pdf.parses@1.0.0  (official)", C["blue"])],
    [("│ ", B), ("  ? Does the file parse as a PDF without errors?   1 = pdfcpu reads…", C["text"])],
    [("│ ", B), ("pdf.has-blank-page@1.0.0  (official)", C["blue"])],
    [("│ ", B), ("  ? Does the PDF have at least one page that paints nothing visible?…", C["text"])],
    [("│ ", B), ("this plan reveals at most 3 answers about the file", C["amber"])],
]
for i, parts in enumerate(screen):
    body.append(spans(QX + 14, QY + 26 + i * 18, parts, cls=show(2.5 + i * 0.07), size=11.5))
body.append(spans(QX + 14, QY + 26 + 11 * 18 + 4,
                  [("╰─ ", B), ("Approve? [y/N] ", C["text"])], cls=show(3.4), size=11.5))
body.append(text(QX + 14 + 18 * 6.9, QY + 26 + 11 * 18 + 4, "y", cls=show(4.6), size=11.5,
                 col=C["green"], weight=700))
body.append(f'<g class="{show(4.7, 6.2)}"><rect x="{X1 - 150}" y="{QY + 212}" width="110" '
            f'height="22" rx="12" fill="{C["green"]}"/>'
            + text(X1 - 95, QY + 227, "approved", size=12, col=C["crust"], weight=700,
                   family=SANS, anchor="middle") + "</g>")

# The source and the sandbox.
FY = Y0 + 284
body.append(f'<rect x="{QX}" y="{FY}" width="170" height="140" rx="12" fill="{C["mantle"]}" '
            f'stroke="{C["s1"]}" stroke-width="1.5" class="{flash(5.0, C["teal"])}"/>')
body.append(label(QX + 16, FY + 26, "SOURCE"))
body.append(text(QX + 16, FY + 52, "local", size=13))
body.append(text(QX + 16, FY + 72, "/srv/samples", size=12, col=C["sub"]))
body.append(text(QX + 16, FY + 92, "s3 · sftp · local", size=11, col=C["ov"], family=SANS))
# A document icon: the file, fetched once.
doc = (f'<path d="M0 0 H18 L26 8 V32 H0 Z" fill="{C["deep"]}"/>'
       f'<path d="M18 0 V8 H26 Z" fill="{C["blue"]}" opacity=".6"/>')
body.append(f'<g transform="translate({QX + 126} {FY + 92})">{doc}</g>')

SBX, SBW = QX + 196, X1 - X0 - 236
body.append(f'<rect x="{SBX}" y="{FY}" width="{SBW}" height="140" rx="12" fill="{C["mantle"]}" '
            f'stroke="{C["teal"]}" stroke-width="1.5" stroke-dasharray="6 5"/>')
body.append(label(SBX + 16, FY + 26, "SANDBOX", col=C["teal"]))
body.append(text(SBX + SBW - 16, FY + 26, "fd 3 read-only · no network · 3-byte stdout",
                 size=11, col=C["sub"], family=SANS, anchor="end"))
plugins = [("file.non-empty", 5.9), ("pdf.parses", 6.5), ("pdf.has-blank-page", 7.1)]
for i, (name, t) in enumerate(plugins):
    y = FY + 56 + i * 28
    body.append(text(SBX + 16, y, name, size=13, col=C["blue"]))
    body.append(f'<g class="{show(t - 0.4, t, fade=0.1)}">'
                + text(SBX + SBW - 30, y, "…", size=13, col=C["ov"], anchor="middle") + "</g>")
    body.append(f'<g class="{show(t)}"><rect x="{SBX + SBW - 44}" y="{y - 15}" width="28" '
                f'height="20" rx="6" fill="{C["green"]}"/>'
                + text(SBX + SBW - 30, y, "1", size=13, col=C["crust"], weight=700,
                       anchor="middle") + "</g>")

# Packets: the plan goes in, the file goes into the sandbox, three bits come out.
body.append(f'<circle r="7" fill="{C["blue"]}" class="{move(1.8, 2.4, AX + AW - 10, AY + 150, X0 + 10, QY + 60)}"/>')
body.append(f'<g class="{move(5.0, 5.6, QX + 126, FY + 92, SBX + 30, FY + 92)}">'
            f'<g transform="translate(-13 -16)">{doc}</g></g>')
for i in range(3):
    t0 = 7.7 + i * 0.15
    bit = (f'<rect x="-11" y="-11" width="22" height="22" rx="6" fill="{C["green"]}"/>'
           + text(0, 5, "1", size=13, col=C["crust"], weight=700, anchor="middle"))
    body.append(f'<g class="{move(t0, t0 + 0.7, SBX + 10, FY + 56 + i * 28 - 5, AX + AW - 20, RY + 22 + i * 22 - 5)}">'
                f'{bit}</g>')

# Captions.
caps = [(0.0, 2.4, "Your agent asks yes/no questions about a file."),
        (2.4, 5.0, "You approve every plan, in a console it cannot drive."),
        (5.0, 7.6, "Signed plugins run sandboxed on the file."),
        (7.6, END + 0.2, "The agent only ever sees 1s and 0s.")]
for t0, t1, s in caps:
    body.append(text(W / 2, 598, s, cls=show(t0 + 0.05, t1 - 0.2), size=26, weight=650,
                     family=SANS, anchor="middle"))

print(f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" '
      f'role="img" aria-label="lockfile: an AI agent asks three yes/no questions about an invoice; '
      f'you approve the plan in the lockfile console, signed plugins run sandboxed on the file, '
      f'and only their answers, 1, 1 and 1, reach the agent.">')
print("<title>lockfile: your AI asks about the file, it never reads it</title>")
print("<style>" + "\n".join(css) +
      "\n@media (prefers-reduced-motion: reduce){*{animation-delay:-10s!important;"
      "animation-play-state:paused!important}}</style>")
print("\n".join(body))
print("</svg>")
