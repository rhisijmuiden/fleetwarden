# FleetWarden house styles

Each folder in `branding/` is one complete house style. The web UI, e-mails,
PDF reports and the Windows tray pop-up are built with exactly one of them.

| Folder | House style |
| --- | --- |
| `ijmond360/` | IJmond360, taken from the TV Station Manager project and ijmond360.nl (**active**) |
| `fleetwarden/` | The original FleetWarden style: neutral greys, one blue accent |

## Where the active style is wired in

The active style is chosen once, in code. To switch, point these at another folder:

| File | What it takes from the style |
| --- | --- |
| `web/src/styles.css` | `theme.css` (all `--fw-*` tokens) |
| `web/src/branding.tsx` | `logo.svg` (default logo and favicon) |
| `web/src/pages/admin/Branding.tsx` | `tokens.json` (defaults shown on the Branding page) |
| `internal/mail/templates.go` | default accent colour and neutral colours of e-mails |
| `internal/reports/pdf.go` | colours of PDF reports |
| `packaging/windows/Tray/Theme.cs`, `Program.cs` | colours of the tray pop-up and tray icon |

## What a style folder contains

| File | Purpose |
| --- | --- |
| `tokens.json` | Source of truth for colours, radius, fonts and shadow (light and dark) |
| `theme.css` | The same tokens as CSS custom properties (`--fw-*`), imported by the web UI |
| `logo.svg` | Square logo, used in the menu, sign-in page, Info dialog and as favicon |
| `BRAND.md` | Guidelines (optional for the original style, which is described below) |

Every style defines the same `--fw-*` variables, so switching never needs UI changes.
UI code never hard-codes brand colours: use the Tailwind classes from
`web/src/styles.css` (`bg-primary`, `text-accent`, `bg-sidebar`, ...).

## White-label

Every colour is a CSS custom property. The *Administration > Branding* page
stores an organisation's own name, logo, favicon and colours and injects them
at runtime, so no rebuild is needed. Empty fields fall back to the active style.

## Guidelines (all styles)

- Use `--fw-primary` for one main action per screen; everything else is neutral.
- Status colours (`success`, `warning`, `danger`) only carry status, never decoration.
- Navigation lives in the left sidebar, grouped by task.
- All text is available in English (default) and Dutch.
