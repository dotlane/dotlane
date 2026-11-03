#!/usr/bin/env python3
"""Writes docs/diagrams/branching-flow.svg, the branching flow of docs/branching.md, to stdout:

    python3 docs/diagrams/branching-flow.py > docs/diagrams/branching-flow.svg

Animated with CSS keyframes only, so it plays where GitHub shows it as an image (no scripts run
there); with reduced motion it shows the finished picture. The keyframe percentages come from
the stage times below, which is why it is generated rather than edited by hand.
"""
import sys

T = 26.0          # loop length, seconds
FADE = 0.5        # fade duration, seconds
END = T - 1.0     # everything fades out from here, then the loop restarts

STAGES = [0, 2.5, 5, 7.5, 10, 12.5, 15, 17.5, 20]
CAPTIONS = [
    "1. Each agent cuts its own branch from origin/main, in its own worktree.",
    "2. One commit per task. A user-visible change adds a fragment in changes/, never an edit to CHANGELOG.md.",
    "3. Marking the pull request ready runs the quick Linux tier only. Drafts and docs-only changes run nothing.",
    "4. gh pr merge --squash --auto puts it in the merge queue. Pull requests never rebase on each other.",
    "5. The queue builds the group on top of main and runs the full tier once, for every pull request in it.",
    "6. A pull request that breaks the group is taken out; the rest are tested again. Its author fixes it and requeues.",
    "7. The group lands as one squash commit per pull request. Main is not tested again: the queue tested that tree.",
    "8. A release candidate is a tag on main; its notes come from the changelog fragments.",
    "9. A \"Release v0.3.0\" pull request collects the fragments into CHANGELOG.md. Its tag runs release.yml.",
]

def pct(t):
    return f"{max(0.0, min(100.0, t / T * 100)):.2f}%"

css = []
n = 0

def anim(frames, prop_fn):
    """frames: list of (t, value) with holds between; returns a class name with its keyframes."""
    global n
    n += 1
    name = f"k{n}"
    body = " ".join(f"{pct(t)} {{ {prop_fn(v)} }}" for t, v in frames)
    css.append(f"@keyframes {name} {{ {body} }}")
    css.append(f".{name} {{ animation: {name} {T}s linear infinite; }}")
    return name

def show(start, until=END):
    """Visible from start until `until` (fading in and out)."""
    return anim([(0, 0), (start, 0), (start + FADE, 1), (until, 1), (until + FADE, 0), (T, 0)],
                lambda v: f"opacity: {v};")

def move(points):
    """points: list of (t, dx, dy, opacity), held between points."""
    return anim([(t, (x, y, o)) for t, x, y, o in points],
                lambda v: f"transform: translate({v[0]}px, {v[1]}px); opacity: {v[2]};")

s = STAGES
W, H = 1120, 640
MAIN_Y = 470
FORK_X = 120
LANES = [("feat/a", 130), ("feat/b", 200), ("fix/c", 270)]
COMMITS = [[320, 390], [300, 360, 420], [360]]
BADGE_X, BADGE_W = 490, 130
QX, QY, QW, QH = 680, 100, 300, 270
SLOT_X, SLOT_W, SLOT_H = 770, 120, 30
SLOTS = [145, 190, 235]
SQUASH = [800, 850]
RC_X, REL_X = 920, 1000

out = []
o = out.append

o(f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-labelledby="t d">')
o('  <title id="t">Dotlane: branches, pull requests, CI and releases</title>')
o('  <desc id="d">Three branches leave main, one per agent, each with a commit per task. Each pull request runs the quick '
  'Linux tier when marked ready, then joins the merge queue. The queue tests the group on top of main with the full macOS, '
  'Windows and Linux tier; a failing pull request is taken out and the rest are retested. The group lands on main as one '
  'squash commit per pull request. A release candidate is a tag on main, and a release pull request collects the changelog '
  'fragments before the release tag runs the release workflow.</desc>')
o('  <style>')
o('''    :root {
      --bg: #ffffff; --ink: #0f172a; --muted: #475569; --line: #64748b; --frame: #cbd5e1;
      --br: #7c3aed; --br-fill: #ede9fe;
      --ci: #0d9488; --ci-fill: #ccfbf1;
      --bad: #dc2626; --bad-fill: #fee2e2;
      --tag: #b45309; --tag-fill: #fef3c7;
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --bg: #0b1120; --ink: #e2e8f0; --muted: #94a3b8; --line: #94a3b8; --frame: #334155;
        --br: #a78bfa; --br-fill: #2e1f5e;
        --ci: #2dd4bf; --ci-fill: #0f3d3a;
        --bad: #f87171; --bad-fill: #4c1414;
        --tag: #fbbf24; --tag-fill: #422006;
      }
    }
    text { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; fill: var(--ink); }
    .title { font-size: 20px; font-weight: 700; }
    .sub { font-size: 13px; fill: var(--muted); }
    .h { font-size: 15px; font-weight: 700; }
    .s { font-size: 13px; fill: var(--muted); }
    .m { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; }
    .cap { font-size: 14px; }
    .main { stroke: var(--line); stroke-width: 3; }
    .branch { fill: none; stroke: var(--br); stroke-width: 2; }
    .commit { fill: var(--br-fill); stroke: var(--br); stroke-width: 2; }
    .mcommit { fill: var(--bg); stroke: var(--line); stroke-width: 2; }
    .pr { fill: var(--br-fill); stroke: var(--br); stroke-width: 1.6; }
    .ci { fill: var(--ci-fill); stroke: var(--ci); stroke-width: 1.6; }
    .bad { fill: var(--bad-fill); stroke: var(--bad); stroke-width: 1.6; }
    .tag { fill: var(--tag-fill); stroke: var(--tag); stroke-width: 1.6; }
    .frame { fill: none; stroke: var(--frame); stroke-width: 1.2; stroke-dasharray: 6 5; }
    .track { fill: var(--frame); }
    .bar { fill: var(--ci); transform-box: fill-box; transform-origin: left center; }
    .still { opacity: 0; }''')

body_start = len(out)
o('  </style>')
o(f'  <rect x="0" y="0" width="{W}" height="{H}" fill="var(--bg)"/>')
o('  <text class="title" x="40" y="44">Dotlane: branches, pull requests, CI and releases</text>')
o('  <text class="sub" x="40" y="66">Trunk-based on main with a merge queue (docs/branching.md). Purple: branches and pull requests. '
  'Green: CI. Amber: tags.</text>')

o(f'  <line class="main" x1="60" y1="{MAIN_Y}" x2="1060" y2="{MAIN_Y}"/>')
o(f'  <text class="h" x="60" y="{MAIN_Y - 16}">main</text>')
o(f'  <circle class="mcommit" cx="{FORK_X}" cy="{MAIN_Y}" r="8"/>')

# Branches draw themselves in; labels and commits follow.
draw = anim([(0, 1), (1.5, 0), (END, 0), (END + FADE, 1), (T, 1)], lambda v: f"stroke-dashoffset: {v};")
for (label, y), commits in zip(LANES, COMMITS):
    o(f'  <path class="branch {draw}" pathLength="1" stroke-dasharray="1" '
      f'd="M{FORK_X} {MAIN_Y - 8} Q{FORK_X} {y} {FORK_X + 80} {y} L470 {y}"/>')
    o(f'  <text class="m {show(s[0] + 0.8)}" x="{FORK_X + 90}" y="{y - 14}">{label}</text>')
    for x in commits:
        o(f'  <circle class="commit {show(s[1])}" cx="{x}" cy="{y}" r="8"/>')

# Quick tier on each pull request.
cls = show(s[2], s[3])
for _, y in LANES:
    o(f'  <g class="{cls} tr"><rect class="ci" x="{BADGE_X}" y="{y - 15}" width="{BADGE_W}" height="30" rx="6"/>'
      f'<text class="s" x="{BADGE_X + BADGE_W / 2}" y="{y + 5}" text-anchor="middle" style="fill: var(--ink)">quick CI passed</text></g>')

# The merge queue and its full tier.
cls = show(s[3], s[6])
o(f'  <g class="{cls} tr"><rect class="frame" x="{QX}" y="{QY}" width="{QW}" height="{QH}" rx="12"/>'
  f'<text class="h" x="{QX + QW / 2}" y="{QY + 28}" text-anchor="middle">merge queue</text></g>')
cls = show(s[4], s[6])
bar = anim([(0, 0), (s[4], 0), (s[4] + 2.2, 1), (T, 1)], lambda v: f"transform: scaleX({v});")
o(f'  <g class="{cls} tr"><text class="s" x="{QX + 24}" y="{QY + 214}">full tier</text>'
  f'<rect class="track" x="{QX + 100}" y="{QY + 204}" width="170" height="12" rx="6"/>'
  f'<rect class="bar {bar}" x="{QX + 100}" y="{QY + 204}" width="170" height="12" rx="6"/>'
  f'<text class="s" x="{QX + QW / 2}" y="{QY + 246}" text-anchor="middle">macOS, Windows, Linux, packages</text></g>')

# Pull requests travel from their branch into the queue, then onto main (or back out).
def token(label, lane_y, slot_y, fate, merge_x=None):
    sx, sy = BADGE_X + (BADGE_W - SLOT_W) / 2 - SLOT_X, (lane_y - SLOT_H / 2) - slot_y
    if fate == "merge":
        mx, my = merge_x - (SLOT_X + SLOT_W / 2), MAIN_Y - (slot_y + SLOT_H / 2)
        pts = [(0, sx, sy, 0), (s[3], sx, sy, 0), (s[3] + 0.3, sx, sy, 1), (s[3] + 1.3, 0, 0, 1),
               (s[6], 0, 0, 1), (s[6] + 1.2, mx, my, 0), (T, mx, my, 0)]
    else:
        pts = [(0, sx, sy, 0), (s[3], sx, sy, 0), (s[3] + 0.3, sx, sy, 1), (s[3] + 1.3, 0, 0, 1),
               (s[5], 0, 0, 1), (s[5] + 1.2, sx, sy, 1), (END, sx, sy, 1), (END + FADE, sx, sy, 0), (T, sx, sy, 0)]
    g = move(pts)
    o(f'  <g class="{g} tr"><rect class="pr" x="{SLOT_X}" y="{slot_y}" width="{SLOT_W}" height="{SLOT_H}" rx="6"/>'
      f'<text class="m" x="{SLOT_X + SLOT_W / 2}" y="{slot_y + 20}" text-anchor="middle">{label}</text>')
    if fate == "eject":
        red = show(s[5])
        o(f'    <g class="{red}"><rect class="bad" x="{SLOT_X}" y="{slot_y}" width="{SLOT_W}" height="{SLOT_H}" rx="6"/>'
          f'<text class="m" x="{SLOT_X + SLOT_W / 2}" y="{slot_y + 20}" text-anchor="middle" style="fill: var(--bad)">PR c failed</text></g>')
    o('  </g>')

token("PR a", LANES[0][1], SLOTS[0], "merge", SQUASH[0])
token("PR b", LANES[1][1], SLOTS[1], "merge", SQUASH[1])
token("PR c", LANES[2][1], SLOTS[2], "eject")

# Squash commits, then the tags.
cls = show(s[6] + 0.8)
o(f'  <g class="{cls}">' + "".join(f'<circle class="commit" cx="{x}" cy="{MAIN_Y}" r="8"/>' for x in SQUASH) +
  f'<text class="s" x="{sum(SQUASH) / 2}" y="{MAIN_Y + 30}" text-anchor="middle">a and b, squashed</text></g>')
cls = show(s[7])
o(f'  <g class="{cls}"><circle class="mcommit" cx="{RC_X}" cy="{MAIN_Y}" r="8"/>'
  f'<rect class="tag" x="{RC_X - 70}" y="{MAIN_Y + 46}" width="140" height="30" rx="6"/>'
  f'<text class="m" x="{RC_X}" y="{MAIN_Y + 66}" text-anchor="middle">v0.3.0-rc.1</text></g>')
cls = show(s[8])
o(f'  <g class="{cls}"><circle class="mcommit" cx="{REL_X}" cy="{MAIN_Y}" r="8"/>'
  f'<text class="s" x="{REL_X + 50}" y="{MAIN_Y - 18}" text-anchor="end">Release v0.3.0</text>'
  f'<rect class="tag" x="{REL_X - 50}" y="{MAIN_Y + 86}" width="100" height="30" rx="6"/>'
  f'<text class="m" x="{REL_X}" y="{MAIN_Y + 106}" text-anchor="middle">v0.3.0</text>'
  f'<text class="s" x="{REL_X - 64}" y="{MAIN_Y + 106}" text-anchor="end">release.yml publishes</text></g>')

# One caption per stage.
o(f'  <line x1="40" y1="{H - 56}" x2="{W - 40}" y2="{H - 56}" stroke="var(--frame)"/>')
for i, text in enumerate(CAPTIONS):
    until = (s[i + 1] if i + 1 < len(s) else END) - FADE
    o(f'  <text class="cap tr {show(s[i], until)}" x="40" y="{H - 26}">{text}</text>')
o(f'  <text class="cap still" x="40" y="{H - 26}">Branch from main, open a pull request, merge through the queue; '
  'releases are tags on main.</text>')
o('</svg>')

# Without motion: the finished picture, minus the parts that only exist mid-flight.
css.append('''@media (prefers-reduced-motion: reduce) {
      [class*="k"] { animation: none !important; }
      .tr { opacity: 0; }
      .still { opacity: 1; }
    }''')
out[body_start:body_start] = ["    " + c for c in css]
sys.stdout.write("\n".join(out) + "\n")
