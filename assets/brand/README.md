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
| `social.svg`, `social.png` | the 1200×630 link preview (`og:image`, GitHub social preview); copied to the docs site (served from team.klarlabs.de) as its social card |
| `app-icon-macos.svg` | macOS icon on the Big Sur grid; `apps/menubar/AppIcon.icns` is built from it |
| `favicon.svg` | heavier ring, no dot, for 16–32 px |

Colours: electric lime `#C6F432` on ink `#0E1220`; on a light ground the ring
and any lime text or thin stroke use dark lime `#4D7C0F` (lime fills carry ink
text, never white); amber `#F59E0B` for the token. Letters: Geist SemiBold, tracking −0.035 em, outlined (see
`THIRD_PARTY_NOTICES.md`). The ring's stroke is 112 units against the o's
104–133, its diameter the mean of the o's width and height.

Redraw everything with `python make.py <Geist[wght].ttf> .` (needs
fonttools); rebuild the macOS icon with `iconutil` from `app-icon-macos.svg`
rendered at 16–1024 px; render `social.png` (1200×630) and `wordmark.png` (`-h 112`) with `rsvg-convert`.
