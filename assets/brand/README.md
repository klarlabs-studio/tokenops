# tokenops logo

The lowercase wordmark, with the o of "ops" drawn as the headroom ring: the
plan window around the token. The ring is the same gauge the menu bar app
draws live (filled clockwise from the top to the share left).

| File | Use |
|---|---|
| `wordmark.svg`, `wordmark-dark.svg`, `wordmark-mono.svg` | the logo, on light, on dark, single colour |
| `mark.svg`, `mark-dark.svg`, `mark-mono.svg` | the ring alone |
| `mark-currentcolor.svg` | the ring in the surrounding text colour (VS Code activity bar) |
| `app-icon.svg`, `app-icon-1024.png` | square icon, lime ring on ink (VS Code Marketplace; `integrations/vscode/resources/logo.png` is its 256 px render) |
| `wordmark.png` | the wordmark as a PNG, 112 px high, where SVG is not shown (npm) |
| `social.svg`, `social.png` | the 1200×630 link preview (`og:image`, GitHub social preview); copied to `web/docs/public/social.png` |
| `app-icon-macos.svg` | macOS icon on the Big Sur grid; `apps/menubar/AppIcon.icns` is built from it |
| `favicon.svg` | heavier ring, no dot, for 16–32 px |

Colours: electric lime `#C6F432` on ink `#0E1220`; on a light ground the ring
uses dark lime `#4D7C0F`, a mark rather than text (4.99:1 on white), and lime
text, links and thin strokes use the deeper `#3F6B0B` (6.32:1). Lime fills
carry ink text, never white (1.28:1); amber `#F59E0B` for the token. Letters: Geist SemiBold, tracking −0.035 em, outlined (see
`THIRD_PARTY_NOTICES.md`). The ring's stroke is 112 units against the o's
104–133, its diameter the mean of the o's width and height.

Redraw everything with `python make.py <Geist[wght].ttf> .` (needs
fonttools); rebuild the macOS icon with `iconutil` from `app-icon-macos.svg`
rendered at 16–1024 px; render `social.png` (1200×630) and `wordmark.png` (`-h 112`) with `rsvg-convert`.

## Design tokens

`tokens.json` is the one set of design tokens every TokenOps surface takes
its colours, type, spacing, radii, shadows and motion from. Edit it, then
run `go generate ./internal/tools/gen/designtokens`: that writes
`tokens.css` (CSS custom properties prefixed `--tok-`, light on `:root`,
dark under `prefers-color-scheme: dark`, and either mode pinned with
`data-theme="light"` or `"dark"` on the root element), the copy the menu
bar embeds (`apps/menubar/frontend/tokens.css`), and the table below.
`go test ./internal/tools/gen/designtokens` fails when a generated file is
stale or a colour falls below the contrast it names. Another surface copies
`tokens.css` as it is and maps its own names onto the `--tok-` properties.

Rules the tokens encode:

- Light mode is plain white, dark mode plain ink; no gradients or glows on
  the ground.
- A lime fill (`--tok-accent`) carries ink text (`--tok-on-accent`), never
  white.
- On a light ground, lime as text, a link, a focus ring, a meter or a thin
  stroke is `--tok-accent-text` (`#3F6B0B`); on ink it is the lime itself.
- `#4D7C0F` (`--tok-lime-mark`) is for the logo's ring and chart bars only.
- Amber marks warnings and the logo's token; red is over the limit.

<!-- begin generated: tokens -->
| Token | Light | Dark | Contrast | Use |
|---|---|---|---|---|
| `--tok-ink` | `#0E1220` | `#0E1220` |  | Dark ground; text on lime. |
| `--tok-lime` | `#C6F432` | `#C6F432` |  | Electric lime: fills that carry ink, and the accent on ink. |
| `--tok-lime-hover` | `#D4F75E` | `#D4F75E` |  | A lime fill under the pointer. |
| `--tok-lime-press` | `#B3E01C` | `#B3E01C` |  | A lime fill pressed. |
| `--tok-lime-dark` | `#3F6B0B` | `#3F6B0B` | 6.32:1 on `white` (min 5) | Lime as text, links, focus rings, bars and thin strokes on white. |
| `--tok-lime-mark` | `#4D7C0F` | `#4D7C0F` | 4.99:1 on `white` (min 3) | The logo's ring on a light ground and chart bars; never text (4.99:1). |
| `--tok-amber` | `#F59E0B` | `#F59E0B` |  | The token in the logo; warning fills and marks. |
| `--tok-red` | `#DC2626` | `#DC2626` |  | Danger fills and over-limit bars. |
| `--tok-white` | `#FFFFFF` | `#FFFFFF` |  | Light ground; vendor logo tiles. |
| `--tok-bg` | `#FFFFFF` | `#0E1220` |  | Page ground. |
| `--tok-surface` | `#FFFFFF` | `#161A28` |  | Cards and panels on the page. |
| `--tok-surface-2` | `#F7F8F9` | `#1B2030` |  | Quiet fills: chips, hovered rows, inline boxes. |
| `--tok-surface-3` | `#F0F1F3` | `#222838` |  | Stronger fills: code, table heads. |
| `--tok-sidebar` | `#FAFAFB` | `#111524` |  | Navigation column. |
| `--tok-border` | `#E4E6EA` | `#262C3C` |  | Hairlines and control borders. |
| `--tok-border-strong` | `#CFD3D9` | `#353C4F` |  | Borders that must read: inputs, hovered controls. |
| `--tok-track` | `rgba(22, 24, 29, 0.10)` | `rgba(255, 255, 255, 0.10)` |  | The empty part of a meter. |
| `--tok-overlay` | `rgba(255, 255, 255, 0.85)` | `rgba(14, 18, 32, 0.82)` |  | Behind a dialog. |
| `--tok-selection` | `rgba(198, 244, 50, 0.45)` | `rgba(198, 244, 50, 0.45)` |  | Selected text. |
| `--tok-text` | `#16181D` | `#F2F3F5` | light 17.75:1, dark 16.79:1 on `bg` (min 7) | Body text and headings. |
| `--tok-text-2` | `#3D4148` | `#C7CAD1` | light 10.25:1, dark 11.36:1 on `bg` (min 7) | Secondary text. |
| `--tok-muted` | `#555A64` | `#C7CAD1` | light 6.92:1, dark 11.36:1 on `bg` (min 4.5) | Labels, captions. |
| `--tok-faint` | `#686D77` | `#9DA2AC` | light 4.59:1, dark 5.73:1 on `surface-3` (min 4.5) | The quietest text that still reads, also on surface-3. |
| `--tok-accent` | `#C6F432` | `#C6F432` |  | Primary buttons, the selected tab, hovered menu rows. |
| `--tok-accent-hover` | `#D4F75E` | `#D4F75E` |  | A lime fill under the pointer. |
| `--tok-accent-press` | `#B3E01C` | `#B3E01C` |  | A lime fill pressed. |
| `--tok-on-accent` | `#0E1220` | `#0E1220` | 14.55:1 on `accent` (min 7) | Text and marks on a lime fill. |
| `--tok-accent-soft` | `#F2FDD2` | `rgba(198, 244, 50, 0.14)` |  | A tinted ground: info notes, the current nav item. |
| `--tok-accent-border` | `#D6F37A` | `rgba(198, 244, 50, 0.30)` |  | Border of a tinted ground. |
| `--tok-accent-text` | `#3F6B0B` | `#C6F432` | light 6.32:1, dark 14.55:1 on `bg` (min 5) | Links, lime text, focus rings, thin strokes, healthy meters. |
| `--tok-meter-warn` | `#F59E0B` | `#FBBF24` | light 2.14:1, dark 11.17:1 on `bg` (min 2) | A meter past 60%. |
| `--tok-meter-high` | `#DC2626` | `#F87171` | light 4.82:1, dark 6.74:1 on `bg` (min 3) | A meter past 80%; the even-pace tick. |
| `--tok-success` | `#127034` | `#5BD08A` | light 6.18:1, dark 9.62:1 on `bg` (min 4.5) | Success text. |
| `--tok-success-soft` | `#E8F6ED` | `rgba(34, 160, 90, 0.14)` |  | Success ground. |
| `--tok-success-border` | `#B9E2C7` | `rgba(91, 208, 138, 0.30)` |  | Success border. |
| `--tok-warn` | `#92400E` | `#FBC65A` | light 7.09:1, dark 11.83:1 on `bg` (min 4.5) | Warning text. |
| `--tok-warn-soft` | `#FEF5E4` | `rgba(245, 158, 11, 0.13)` |  | Warning ground. |
| `--tok-warn-border` | `#F6D28E` | `rgba(245, 158, 11, 0.32)` |  | Warning border. |
| `--tok-warn-accent` | `#F59E0B` | `#F59E0B` |  | Warning marks and bars. |
| `--tok-danger` | `#B91C1C` | `#F87171` | light 6.47:1, dark 6.74:1 on `bg` (min 4.5) | Error and danger text. |
| `--tok-danger-soft` | `#FDEEEE` | `rgba(220, 38, 38, 0.14)` |  | Danger ground. |
| `--tok-danger-border` | `#F5C2C2` | `rgba(248, 113, 113, 0.32)` |  | Danger border. |
| `--tok-danger-solid` | `#DC2626` | `#DC2626` |  | Destructive buttons. |
| `--tok-on-danger` | `#FFFFFF` | `#FFFFFF` | 4.82:1 on `danger-solid` (min 4.5) | Text on a destructive button. |
| `--tok-chart-bar` | `#4D7C0F` | `#C6F432` | light 4.99:1, dark 14.55:1 on `bg` (min 3) | Bars. |
| `--tok-chart-bar-hover` | `#3F6B0B` | `#D4F75E` |  | A hovered bar. |
| `--tok-chart-line` | `#3F6B0B` | `#C6F432` | light 6.32:1, dark 14.55:1 on `bg` (min 3) | Lines. |
| `--tok-chart-grid` | `#E8EAEE` | `#262C3C` |  | Gridlines. |
| `--tok-chart-text` | `#555A64` | `#A9AEB8` | light 6.92:1, dark 8.37:1 on `bg` (min 4.5) | Axis labels. |
| `--tok-chart-muted` | `#C8CCD3` | `#3A4154` |  | Series in the background. |
| `--tok-brand-bg` | `#0E1220` | `#0E1220` |  | Ground. |
| `--tok-brand-text` | `#F2F3F5` | `#F2F3F5` | 16.79:1 on `brand-bg` (min 7) | Text. |
| `--tok-brand-muted` | `#C7CAD1` | `#C7CAD1` | 11.36:1 on `brand-bg` (min 4.5) | Secondary text. |
| `--tok-brand-accent` | `#C6F432` | `#C6F432` | 14.55:1 on `brand-bg` (min 4.5) | Accent. |
<!-- end generated: tokens -->
