# Brand

Dotlane's identity, chosen 2026-10-30: the mark **"period in the bar"** in **Ink & Green**. The
files are in [`assets/brand/`](../assets/brand/), drawn by `go run ./scripts/brandgen`. Change the
numbers there, not the files, so the SVGs, PNGs, `.ico`, `.icns` and the daemon's pages stay
identical.

## The mark

<img src="../assets/brand/mark.svg" width="96" alt="Dotlane mark on ink"> <img src="../assets/brand/mark-paper.svg" width="96" alt="Dotlane mark on paper">

An address bar, reduced to its outline, with one green dot at its end: the period after the name,
the `.` in `myapp.traix`, and a status light that says the address is live. One shape, one
colour, nothing else.

**Construction** (64-unit grid, `scripts/brandgen`):

| Part | Regular (above 32 px) | Small (32 px and below) |
| --- | --- | --- |
| Tile | 64 × 64, corner radius 15 | radius 14 |
| Rim (hairline inside the tile edge) | 1 unit | 2.5 units |
| Bar outline | x 11.5, y 23.5, 41 × 17, fully rounded, stroke 3 | x 9, y 21.5, 46 × 21, stroke 5 |
| Dot | centre (43.5, 32), radius 3.75 | radius 5 |

The small cut keeps the bar at least a pixel wide at 16 px; favicons and 16/32 px icon slots use
it. Everything else uses the regular cut.

**Clear space:** a quarter of the tile's width on every side. **Minimum size:** 16 px with the tile,
12 px for the glyph alone.

**Variants:**

| File | Use |
| --- | --- |
| `mark.svg` | Default: ink tile. App icons, social images, the README |
| `mark-paper.svg` | Paper tile, for light layouts that want a light icon |
| `mark-small.svg` | The small cut, for 16–32 px |
| `favicon.svg` | Small cut that follows the browser's theme: paper in light mode, ink in dark |
| `glyph.svg` | Bar and dot without the tile, next to text; the bar takes the text colour (`currentColor`) |
| `favicon.ico` | 16, 32, 48 px, for browsers and tools that want `.ico` |
| `dotlane.icns` | macOS icon, every slot from 16 px to 1024 px, on Apple's icon grid (tile 824/1024) |
| `png/` | `mark-<size>.png` at 16–1024 px, `mark-paper-<size>.png`, `apple-touch-icon.png` (180 px) |

The daemon runs as a launchd agent, and macOS shows custom icons only for app bundles. So the
`.icns` is used once Dotlane ships as one (packages in Phase 2, the menu-bar app in Phase 5); until
then, Login Items shows the generic executable icon.

## Colour

One neutral ground and one green. The green is the only colour in the identity; everything else is
ink, paper and greys.

| Name | Hex | Role |
| --- | --- | --- |
| Ink | `#0f172a` | Tile; text and the bar on paper |
| Snow | `#f8fafc` | The bar on ink; text on dark |
| Green | `#22c55e` | The dot on ink and other dark surfaces |
| Deep green | `#16a34a` | The dot on paper and other light surfaces |
| Paper | `#ffffff` | Light tile and page cards |
| Ink rim | `#6f747f` | Hairline that keeps the ink tile visible on dark docks |
| Paper rim | `#e2e8f0` | Hairline for the paper tile on white |
| Slate 600 / 400 | `#475569` / `#94a3b8` | Secondary text, light / dark |

Measured contrast (WCAG):

- Ink tile: 17.9:1 on white; 1.1:1 on a dark dock (`#1e1e1e`), which is what the rim is for.
- Bar on its tile: 17:1 (snow on ink) and 17.9:1 (ink on paper).
- Dot on its tile: 7.8:1 (green on ink) and 3.3:1 (deep green on paper), above the 3:1 needed
  for graphics.

**Rules:**

- The green marks *the dot*: the mark, a live status, one highlight per view. It is never body
  text on white (`#22c55e` is 2.3:1 there); links and text in the brand colour use ink, or deep
  green where 3:1 is enough (large text, icons).
- Do not recolour the mark, put the dot elsewhere, add a second colour, outline the dot or set the
  mark on a busy photo.
- In the CLI, the ✓ of a passed step is a *status* green (`#1a7f37` on light terminals, `#3fb950`
  on dark, `internal/ui`), not the brand green, so success and the logo never compete. Warnings
  are amber, failures red.
- Amber `#d97706` (light) / `#f59e0b` (dark) is the only other hue, and only for "not
  answering" states, like the 502 page's dot.

## Type

| Role | Typeface | Fallback |
| --- | --- | --- |
| Wordmark and headings | Inter Tight, 600, letter-spacing −0.02 to −0.03 em | Inter, system-ui |
| Text | Inter, 400–600 | system-ui, -apple-system, Segoe UI, Roboto |
| Code and addresses | JetBrains Mono | ui-monospace, SF Mono, Menlo, Consolas |

The wordmark is **dotlane**, always lowercase, set in Inter Tight 600 at −0.03 em, beside the mark
with a gap of a third of the mark's height. The product name in running text is "Dotlane".

Pages the daemon serves use only the system fonts (they work offline); the website loads Inter
Tight, Inter and JetBrains Mono from Google Fonts and falls back to the same system stacks.

## Dotlane's own pages

When a name has no site, or a site's dev server is down, Dotlane answers with its own page instead
of a browser error ([`how-it-works.md`](how-it-works.md)). Both use one layout
(`internal/proxy/page.html`):

- the glyph and wordmark at the top;
- an address bar holding the requested URL, its dot showing the state: grey for "no site", amber
  for "not answering";
- the status (`404 · no site`, `502 · not answering`), a heading, the fix as a command;
- a footer saying the page is Dotlane's, not the app's.

They follow the system's light or dark mode, carry the adaptive favicon as a `data:` URI and load
nothing from the network.

## Voice

Plain and short, like the CLI: say what happened, then what to do, with the command to run. No
exclamation marks, no "Oops". Name things the way the user sees them (`https://myapp.traix`,
`localhost:5173`), never internals.
