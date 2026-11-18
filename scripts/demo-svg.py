#!/usr/bin/env python3
"""Generates site/assets/demo.svg: lockfile's value in one looping image.

An agent asks three yes/no questions about an invoice it must never read. The
plan goes to lockhub as a ticket, the human reads it and approves it in
`lox run`, the agent runs the ticket, the lockfile console fetches the file and
runs three signed, sandboxed plugins on it, and only their answers cross the
socket back to the agent: 1, 1, 1. Pure SVG + CSS keyframes on one clock, so it
plays on GitHub, on the site and in any browser.

No on-screen text is typed by hand, but this is not a recording of a live
session. Each screen is drawn by the code that draws it for real, run on the
demo's plan:

- the `lox run` terminal is scripts/demo-lox-run.json: the frames lockhub's
  own renderer wrote (internal/loxrun at 4d94ef1, lockkit v0.20.0), from a
  Session on a 92x25 colour terminal in that package's test harness (a hub
  queue in memory, a signed ticket), with the keys d then y, then the hub
  state "consumed". Two limits follow. The harness follows no lock console,
  so the header lacks the `locks … serving` line and the tabs line that a
  real `lox run` serving a lockfile console draws. And the ticket was built
  for the capture, as lockkit's console builds a plan's ticket (version 1,
  read_pii, lock lockfile, instance files), not sent by a running console.
  Its screen is what internal/backend (planScreen, hubScreens) makes of this
  plan with the official marketplace's manifests of the three plugins. The
  same note is in the JSON file's "note" key;
- the agent's commands are the CLI's (docs/usage.md, "The agent side"); the
  pending line is lockkit's mcpkit.PendingText, the result lines are
  cmd/lockfile resultLines, the handle has workspace.newHandle's shape;
- the sandbox line is docs/security-model.md's; the answers are those of the
  three plugins run on the file whose sha256 and size the ticket shows.

The agent's closing remark and the captions are the only prose.

    python3 scripts/demo-svg.py > site/assets/demo.svg
"""

import json
import pathlib

LOOP = 17.0
END = 16.4  # everything fades out here, then the loop restarts
# The Lock Family's terminal: warm carbon backgrounds (lockfamily assets/tokens.css,
# --term-bg) and its dim text; the accents stay as they were.
C = dict(crust="#1a1814", base="#221f1a", mantle="#2a2620", s0="#38332b", s1="#4a443a",
         ov="#8a8273", sub="#a8a090", text="#E2E8F0", blue="#60A5FA", deep="#3B82F6",
         teal="#5EEAD4", red="#F87171", green="#4ADE80", amber="#FBBF24")
FONT = "'JetBrains Mono','SF Mono',Menlo,Consolas,'DejaVu Sans Mono',monospace"
SANS = "Inter,'Segoe UI',system-ui,-apple-system,Helvetica,Arial,sans-serif"

LOX = json.loads((pathlib.Path(__file__).parent / "demo-lox-run.json").read_text(encoding="utf-8"))
TICKET = LOX["ticket"]
KEY = "invoices/jane.doe@example.com/2026-10.pdf"
HANDLE = "f_G9Qf4otbDr"

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


W, H = 1200, 710
body.append(f'<rect width="{W}" height="{H}" fill="{C["crust"]}"/>')

# --- The agent's account (left) --------------------------------------------------------------
AX, AY, AW, AH = 30, 56, 420, 554
AS, AL = 11.5, 19  # the agent terminal's font size and line height
body.append(label(AX, AY - 26, "YOUR AGENT · its own account"))
body.append(f'<rect x="{AX}" y="{AY}" width="{AW}" height="{AH}" rx="14" fill="{C["base"]}" '
            f'stroke="{C["s1"]}" stroke-width="1.5"/>')
body.append(text(AX + 18, AY + 30, "✻ Claude Code", size=13, col=C["amber"]))

T_PLAN, T_ASK, T_DETAILS, T_YES, T_RUN, T_DONE = 0.3, 2.4, 4.6, 7.6, 8.2, 12.3

cmd = [
    (0.0, [("> ", C["sub"]), ("lockfile plan \\", C["text"])]),
    (0.2, [("    --key ", C["text"]), (KEY + " \\", C["teal"])]),
    (0.4, [("    --plugin ", C["text"]), ("file.non-empty \\", C["blue"])]),
    (0.6, [("    --plugin ", C["text"]), ("pdf.parses \\", C["blue"])]),
    (0.8, [("    --plugin ", C["text"]), ("pdf.has-blank-page \\", C["blue"])]),
    (1.0, [("    --intent ", C["text"]), ('"invoice reported as blank"', C["sub"])]),
]
for i, (t, parts) in enumerate(cmd):
    body.append(spans(AX + 18, AY + 62 + i * AL, parts, cls=show(T_PLAN + t), size=AS))

# What `lockfile plan` prints: one line, wrapped here as a terminal wraps it.
pending = ["Pending human approval: ticket",
           f"{TICKET} on lockfile. Do not retry.",
           "End your turn; you will be told when it is decided."]
for i, s in enumerate(pending):
    body.append(text(AX + 18, AY + 186 + i * AL, s, cls=show(T_ASK + 0.1), size=AS, col=C["amber"]))

# The ticket is run once decided: exactly what `lockfile run TICKET` prints.
RY = AY + 262
body.append(spans(AX + 18, RY, [("> ", C["sub"]), ("lockfile run " + TICKET, C["text"])],
                  cls=show(T_RUN), size=AS))
result = [
    (0.0, [("handle ", C["sub"]), (HANDLE, C["text"])]),
    (0.2, [("file.non-empty@1.0.0: ", C["text"]), ("1", C["green"])]),
    (0.4, [("pdf.parses@1.0.0: ", C["text"]), ("1", C["green"])]),
    (0.6, [("pdf.has-blank-page@1.0.0: ", C["text"]), ("1", C["green"])]),
]
for i, (t, parts) in enumerate(result):
    body.append(spans(AX + 18, RY + (i + 1) * AL, parts, cls=show(T_DONE + t), size=AS))
body.append(text(AX + 18, RY + 116, "✻ It parses and has a blank page:", cls=show(13.4),
                 size=12.5, col=C["amber"]))
body.append(text(AX + 18, RY + 136, "  the bug is in rendering, not the upload.", cls=show(13.4),
                 size=12.5, col=C["amber"]))

# What never reaches the agent, struck out.
never = ["bytes", "name", "size", "type", "path"]
g = [f'<g class="{show(14.2)}">',
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

# --- The socket between the agent and the lock -----------------------------------------------
SX = 466
body.append(f'<line x1="{SX}" y1="{AY + 20}" x2="{SX}" y2="{AY + AH - 20}" stroke="{C["s1"]}" '
            f'stroke-width="2" stroke-dasharray="4 6"/>')
body.append(text(SX, AY - 10, "socket", size=12, col=C["ov"], weight=700, family=SANS,
                 anchor="middle"))

# --- lox run, your terminal (top right): lockhub's own frames --------------------------------
X0, X1 = 482, 1170
TY, TS, CW, LH = AY, 11.5, 6.9, 14.4
TH = 26 + LOX["rows"] * LH + 8
TXT = X0 + (X1 - X0 - LOX["cols"] * CW) / 2
body.append(f'<rect x="{X0}" y="{TY}" width="{X1 - X0}" height="{TH:.0f}" rx="14" '
            f'fill="{C["crust"]}" stroke="{C["deep"]}" stroke-width="2.5" '
            f'class="{flash(T_YES, C["green"], base=C["deep"], hold=0.6)}"/>')
body.append(f'<rect x="{X0 + 24}" y="{TY - 14}" width="200" height="28" rx="14" fill="{C["deep"]}"/>')
body.append(text(X0 + 124, TY + 5, "lox run · your terminal", size=13, col=C["crust"],
                 weight=700, family=SANS, anchor="middle"))

# The colours of lox run's SGR styles (lockhub internal/loxrun/style.go), on this palette.
FG = {"30": C["crust"], "31": C["red"], "32": C["green"], "33": C["amber"], "94": C["blue"]}
BG = {"42": C["green"]}
# Box-drawing characters as (left, right, up, down) half-strokes.
BOX = {"─": (1, 1, 0, 0), "│": (0, 0, 1, 1), "┌": (0, 1, 0, 1), "┐": (1, 0, 0, 1),
       "└": (0, 1, 1, 0), "┘": (1, 0, 1, 0)}


def terminal(lines, cls):
    out = [f'<g class="{cls}">']
    for row, segs in enumerate(lines):
        y = TY + 36 + row * LH
        col, parts = 0, []
        for sgr, s in segs:
            codes = sgr.split(";") if sgr else []
            x = TXT + col * CW
            col += len(s)
            if not s.strip() and not any(c in BG for c in codes):
                continue
            fill = C["sub"] if "2" in codes else C["text"]
            for c in codes:
                fill = FG.get(c, fill)
            # The frames and rules are drawn as lines, so they join whatever font is used.
            if set(s) <= set(BOX) | {" "}:
                cy, d = y - 10.5 + LH / 2, []
                if set(s) == {"─"}:  # a rule: one stroke
                    d.append(f"M{x:.1f} {cy:.1f}h{len(s) * CW:.1f}")
                    s = ""
                for i, ch in enumerate(s):
                    cx = x + (i + 0.5) * CW
                    le, ri, up, dn = BOX.get(ch, (0, 0, 0, 0))
                    if le or ri:
                        d.append(f"M{cx - le * CW / 2:.1f} {cy:.1f}h{(le + ri) * CW / 2:.1f}")
                    if up or dn:
                        d.append(f"M{cx:.1f} {cy - up * LH / 2:.1f}v{(up + dn) * LH / 2:.1f}")
                out.append(f'<path d="{"".join(d)}" stroke="{fill}" fill="none"/>')
                continue
            for c in codes:
                if c in BG:
                    out.append(f'<rect x="{x:.1f}" y="{y - 10.5:.1f}" width="{len(s) * CW:.1f}" '
                               f'height="{LH}" fill="{BG[c]}"/>')
            attrs = f'x="{x:.1f}" fill="{fill}"'
            if "1" in codes:
                attrs += ' font-weight="700"'
            if "3" in codes:
                attrs += ' font-style="italic"'
            parts.append(f'<tspan {attrs}>{esc(s)}</tspan>')
        if parts:
            out.append(f'<text y="{y:.1f}" font-family="{FONT}" font-size="{TS}" '
                       f'xml:space="preserve">{"".join(parts)}</text>')
    out.append("</g>")
    return "".join(out)


frames = [("idle", 0.05, T_ASK), ("question", T_ASK, T_DETAILS), ("details", T_DETAILS, T_YES),
          ("approved", T_YES, T_DONE), ("ran", T_DONE, END)]
for name, t0, t1 in frames:
    body.append(terminal(LOX["frames"][name], show(t0, t1, fade=0.05)))

# The keys you press, next to the terminal's name (lox run does not echo them).
for t, key, what in [(T_DETAILS, "d", "details"), (T_YES, "y", "approve")]:
    body.append(f'<g class="{show(t - 0.5, t + 0.5, fade=0.1)}">'
                f'<rect x="{X1 - 196}" y="{TY - 40}" width="196" height="24" rx="12" '
                f'fill="{C["green"]}"/>'
                + text(X1 - 98, TY - 23, f"you press {key}: {what}", size=12.5, col=C["crust"],
                       weight=700, family=SANS, anchor="middle") + "</g>")

# --- The lockfile console, in its own account (bottom right) ---------------------------------
Y0 = TY + TH + 24
Y1 = AY + AH
body.append(f'<rect x="{X0}" y="{Y0}" width="{X1 - X0}" height="{Y1 - Y0:.0f}" rx="14" '
            f'fill="{C["base"]}" stroke="{C["s1"]}" stroke-width="1.5" '
            f'class="{flash(T_RUN + 0.8, C["teal"], hold=0.5)}"/>')
body.append(f'<rect x="{X0 + 24}" y="{Y0 - 13}" width="290" height="26" rx="13" '
            f'fill="{C["s1"]}"/>')
body.append(text(X0 + 169, Y0 + 5, "lockfile console · its own account", size=13, col=C["text"],
                 weight=700, family=SANS, anchor="middle"))

QX, FY, FH = X0 + 16, Y0 + 22, Y1 - Y0 - 34
T_FETCH = T_RUN + 0.9
body.append(f'<rect x="{QX}" y="{FY:.0f}" width="200" height="{FH:.0f}" rx="10" fill="{C["mantle"]}" '
            f'stroke="{C["s1"]}" stroke-width="1.5" class="{flash(T_FETCH, C["teal"])}"/>')
body.append(label(QX + 14, FY + 22, "SOURCE"))
body.append(text(QX + 14, FY + 42, "local", size=12.5))
body.append(text(QX + 14, FY + 60, "/srv/lockfile/samples", size=11.5, col=C["sub"]))
body.append(text(QX + 14, FY + 78, "s3 · sftp · local", size=11, col=C["ov"], family=SANS))
# A document icon: the file, fetched for the run.
doc = (f'<path d="M0 0 H18 L26 8 V32 H0 Z" fill="{C["deep"]}"/>'
       f'<path d="M18 0 V8 H26 Z" fill="{C["blue"]}" opacity=".6"/>')
DX, DY = QX + 162, FY + 8
body.append(f'<g transform="translate({DX} {DY:.0f})">{doc}</g>')

SBX = QX + 216
SBW = X1 - 16 - SBX
body.append(f'<rect x="{SBX}" y="{FY:.0f}" width="{SBW}" height="{FH:.0f}" rx="10" fill="{C["mantle"]}" '
            f'stroke="{C["teal"]}" stroke-width="1.5" stroke-dasharray="6 5"/>')
body.append(label(SBX + 14, FY + 22, "SANDBOX", col=C["teal"]))
body.append(text(SBX + SBW - 14, FY + 22, "fd 3 read-only · no network · 3-byte stdout",
                 size=11, col=C["sub"], family=SANS, anchor="end"))
plugins = [("file.non-empty", T_FETCH + 1.0), ("pdf.parses", T_FETCH + 1.6),
           ("pdf.has-blank-page", T_FETCH + 2.2)]
PY = [FY + 44 + i * 21 for i in range(3)]
for (name, t), y in zip(plugins, PY):
    body.append(text(SBX + 14, y, name, size=12.5, col=C["blue"]))
    body.append(f'<g class="{show(t - 0.4, t, fade=0.1)}">'
                + text(SBX + SBW - 28, y, "…", size=13, col=C["ov"], anchor="middle") + "</g>")
    body.append(f'<g class="{show(t)}"><rect x="{SBX + SBW - 42}" y="{y - 14:.0f}" width="28" '
                f'height="18" rx="6" fill="{C["green"]}"/>'
                + text(SBX + SBW - 28, y, "1", size=12.5, col=C["crust"], weight=700,
                       anchor="middle") + "</g>")

# Packets: the plan goes out as a ticket, the run goes to the console, the file goes into
# the sandbox, three bits come out.
body.append(f'<circle r="7" fill="{C["blue"]}" class="{move(1.7, T_ASK - 0.1, AX + AW - 10, AY + 110, X0 + 10, TY + 80)}"/>')
body.append(f'<circle r="7" fill="{C["blue"]}" class="{move(T_RUN + 0.3, T_RUN + 0.8, AX + AW - 10, RY - 4, X0 + 10, FY + 40)}"/>')
body.append(f'<g class="{move(T_FETCH, T_FETCH + 0.6, DX + 13, DY + 16, SBX + 240, FY + 56)}">'
            f'<g transform="translate(-13 -16)">{doc}</g></g>')
for i in range(3):
    t0 = T_FETCH + 2.5 + i * 0.15
    bit = (f'<rect x="-11" y="-11" width="22" height="22" rx="6" fill="{C["green"]}"/>'
           + text(0, 5, "1", size=13, col=C["crust"], weight=700, anchor="middle"))
    body.append(f'<g class="{move(t0, t0 + 0.7, SBX + 10, PY[i] - 5, AX + AW - 20, RY + (i + 2) * AL - 5)}">'
                f'{bit}</g>')

# Captions.
caps = [(0.0, T_ASK, "Your agent asks yes/no questions about a file."),
        (T_ASK, T_RUN, "You read the plan and approve it in lox run."),
        (T_RUN, T_FETCH + 2.5, "Signed plugins run sandboxed on the file."),
        (T_FETCH + 2.5, END + 0.2, "The agent only ever sees 1s and 0s.")]
for t0, t1, s in caps:
    body.append(text(W / 2, H - 44, s, cls=show(t0 + 0.05, t1 - 0.2), size=26, weight=650,
                     family=SANS, anchor="middle"))

print(f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" '
      f'role="img" aria-label="lockfile: an AI agent asks three yes/no questions about an invoice '
      f'and is told the plan is pending; you read the plan, its personal data masked, and approve '
      f'it in lox run; the agent runs the ticket, signed plugins run sandboxed on the file, '
      f'and only their answers, 1, 1 and 1, reach the agent.">')
print("<title>lockfile: your agent asks about the file, it never reads it</title>")
# Without motion: the still is the approval card, details open.
print("<style>" + "\n".join(css) +
      "\n@media (prefers-reduced-motion: reduce){*{animation-delay:-6s!important;"
      "animation-play-state:paused!important}}</style>")
print("\n".join(body))
print("</svg>")
