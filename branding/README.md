# FleetWarden branding

The default FleetWarden look: calm neutral greys, one blue accent, a dark
sidebar and the Inter typeface.

| File | Purpose |
| --- | --- |
| `tokens.json` | Source of truth for colours, radius and fonts (light and dark) |
| `theme.css` | The same tokens as CSS custom properties, imported by the web UI |
| `logo.svg` | Default logo and favicon |

## White-label

Every colour is a CSS custom property (`--fw-*`). In a later phase the
*Administration > Branding* page stores an organisation's own name, logo,
favicon and colours and injects them at runtime, so no rebuild is needed.
The same values are used for e-mails, PDF reports and the Windows tray pop-up.

## Guidelines

- Use `--fw-primary` for one main action per screen; everything else is neutral.
- Status colours (`success`, `warning`, `danger`) only carry status, never decoration.
- Navigation lives in the left sidebar, grouped by task.
