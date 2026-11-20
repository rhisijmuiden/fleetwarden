# IJmond360 house style

House style of IJmond360, the regional public broadcaster for the IJmond
(Velsen, Beverwijk, Heemskerk). Copied from the TV Station Manager project
(`branding/ijmond360`), which took it from [ijmond360.nl](https://ijmond360.nl).
The product name stays FleetWarden; only the look changes.

## Logo
- `logo.svg`: the app icon. Pink ring and white "360" on a purple rounded
  square. Used in the menu, on the sign-in page, in the Info dialog and as favicon.
- `favicon.svg`: the pink ring on purple, readable at 16 px.
- `wordmark-on-dark.svg` / `wordmark-on-light.svg`: the wordmark "ijmond 360",
  for documents and pages that have room for it.
- Keep clear space of at least the height of the "o" around the logo. Do not
  recolour or stretch it.
- The Windows tray icon keeps the FleetWarden shield, drawn in IJmond colours.

## Colours
| Token | Light | Dark | Use |
|---|---|---|---|
| Primary (IJmond Purple) | `#713C80` | `#9A5BAE` | Primary actions, links, focus |
| Primary hover | `#5A2E66` | `#B57FC6` | Hover and pressed states |
| Accent (IJmond Pink) | `#E92E7F` | `#F0559A` | One highlight per screen: the active menu item |
| Sidebar (Aubergine) | `#2A1430` | `#120A15` | Left menu |
| Success | `#15A06A` | `#1DB97A` | Healthy |
| Warning | `#D98A00` | `#F5A524` | Attention needed |
| Danger | `#DC2F45` | `#F0566A` | Destructive actions, errors |
| Info | `#1F8FC4` | `#2FA6DF` | Information (the site's light blue) |

In the dark theme the primary colour is lighter, because the brand purple has
too little contrast on a dark background. In the light theme success and
warning are a little deeper than on the website, so they stay readable as text
on white.

**Rule:** pink highlights one thing per screen. Use purple for everything else
that is interactive.

## Typography
- UI text: **Inter** (variable), the body font of ijmond360.nl.
- Page and section titles (`h1`, `h2`): **Epilogue** (variable), the heading
  font of ijmond360.nl.
- IDs, versions, logs, code: **JetBrains Mono**.
- All fonts are self-hosted through `@fontsource-variable`, so the server works
  offline.

## Shape and depth
- Radius: 4 (inputs), 8 (buttons), 12 (cards, panels), 16 (dialogs).
- Cards: surface colour, 1px border, soft shadow `--fw-shadow-card`.

## Layout
- Admin panels always have the menu on the **left**, collapsible.
- Bottom of the sidebar: Info button (version and updates) and collapse button.
- All text is available in English (default) and Dutch.
