# MongoJSON Frontend UI Design Guidelines

## Purpose

This document defines the visual style, layout rules, and implementation
requirements for the MongoJSON frontend. Treat it as the default design brief
for every frontend change. The product is a desktop-first developer/data tool,
so the UI should feel like a calm iOS-style workbench: light, precise, dense,
and easy to scan.

## Visual Style

- Use a light iOS productivity style. The base background stays near `#f5f7fb`,
  with white or translucent glass panels and subtle blue/teal wash only.
- Do not add a dark mode, OLED style, heavy gradients, decorative blobs, bokeh,
  or marketing-style hero sections.
- Primary actions use iOS blue `#007aff`; reserve teal, orange, purple, and pink
  for semantic emphasis, chart series, status differentiation, and small accents.
- Prefer calm density over large editorial spacing. This is a repeated-use tool,
  not a product landing page.
- Use real tool content, data previews, editor surfaces, tables, and charts as
  the primary visuals. Avoid placeholder illustrations and decorative cards.

## Tokens And Color Rules

Use the global tokens in `frontend/src/index.css` first:

| Role | Token | Value |
| --- | --- | --- |
| App background | `--bg` | `#f5f7fb` |
| Surface | `--surface` | `#ffffff` |
| Raised surface | `--surface-raised` | `#f8fbff` |
| Glass surface | `--surface-glass` | `rgba(255, 255, 255, 0.74)` |
| Strong glass | `--surface-glass-strong` | `rgba(255, 255, 255, 0.88)` |
| Border | `--border` | `#dfe6f0` |
| Strong border | `--border-strong` | `#c6d2e2` |
| Text | `--text` | `#243044` |
| Strong text | `--text-strong` | `#111827` |
| Muted text | `--text-muted` | `#6f7d91` |
| Accent | `--accent` | `#007aff` |
| Accent strong | `--accent-strong` | `#005ecb` |
| Focus ring | `--focus-ring` | `rgba(0, 122, 255, 0.28)` |

Rules:

- Do not introduce a second palette unless a new token is added deliberately.
- Do not use dark slate/black surfaces as a theme.
- Semantic colors must communicate state consistently: green success, amber/orange
  warning, red/pink error, blue active/primary, teal/purple/pink secondary series.
- Text contrast must stay readable on light and glass surfaces. Muted text should
  still be scannable, not decorative.

## Layout Requirements

- Keep the app as a workbench: sidebar navigation plus main tool surface.
- Sidebar behavior follows `frontend/src/App.css`: expanded around `228px`,
  collapsed around `72px`, glass background, active nav highlight with a soft
  blue pill and left accent strip.
- Main content must fill available height without breaking editor panes. Use
  `min-width: 0`, `min-height: 0`, and explicit grid/flex constraints on nested
  panels.
- Use full-width work areas and constrained inner content only where the workflow
  needs it. Do not build landing-page sections inside the app.
- Cards may be used for repeated items, modals, summary tiles, and framed tools.
  Do not put cards inside cards.
- Default radius is `8px`. Use `10-12px` only when an established component needs
  more breathing room. Avoid large `20px+` rounded panels unless the whole design
  system changes.
- Fixed-format UI elements such as grids, chart frames, icon buttons, editor
  panes, and floating cards must have stable dimensions so hover, labels, and
  dynamic content do not shift layout.

## Component Requirements

- Panels use thin borders, soft shadows, glass or white surfaces, and clear
  headers. Panel headers should be compact and functional.
- Buttons use consistent heights, visible hover/focus states, and semantic color.
  Icon-only buttons must have labels via `aria-label` or title.
- Mode switches use segmented controls, not loose text links.
- Status values use compact chips with state color and readable text.
- Forms and selects must have labels, focus rings, and stable widths.
- Tables should be dense but readable, with sticky headers when useful and a
  subtle hover row state.
- Floating cards follow MemoDocs behavior: user-selected color, persisted color,
  stable ordering, and no color changes based on list index.
- Avoid inline styles for layout or spacing. Use CSS classes unless a dynamic CSS
  variable is necessary for per-item color or measurement.

## Editors, Data Views, And Charts

- Monaco and Vditor are high-risk surfaces. Do not globally override editor
  internals unless the selector is scoped and verified.
- Preserve editor cursor, selection, line height, scroll behavior, and code font.
- MemoDocs remains light-only. Do not restore dark editor/content/code theme
  entries unless the product direction changes.
- Visualization pages should use multi-color series, light grid lines, readable
  axes, and hover affordances. Do not rely on a single `--accent` color for all
  chart data.
- JSON/Mongo tool pages should prioritize compareability, parsing feedback,
  copy/export actions, and clear error states over decorative layout.

## Typography And Icons

- Use the existing system font stack in `--font-sans`.
- Use `--font-mono` only for code, JSON, Mongo expressions, file IDs, and other
  literal technical values.
- Do not scale font size with viewport width. Use responsive layout, not
  viewport-based type.
- Letter spacing should remain `0` unless a component has a clear technical need.
- Use consistent SVG icons. Prefer the project’s existing icon style or lucide
  icons if adding a library is already justified.
- Do not use emoji as UI icons.

## Interaction And Accessibility

- All interactive controls need pointer cursor, hover feedback, visible keyboard
  focus, and disabled/loading states where applicable.
- Transitions should be subtle and fast, usually `150-200ms`; avoid layout-shifting
  scale animations.
- Respect `prefers-reduced-motion` for nonessential animation.
- Color must not be the only state indicator. Pair color with text, icon shape,
  border, or placement.
- Every input, select, textarea, icon-only button, and drag handle needs an
  accessible label.
- Validate at `375px`, `768px`, `1024px`, and `1440px`; there should be no
  horizontal page scroll or overlapping toolbar text.

## Implementation Checklist

- Preserve existing routes and workflows unless the task explicitly changes them.
- Start from global tokens and existing component patterns before adding new CSS.
- Keep CSS grouped by component and avoid unrelated style churn.
- Before finishing UI work, run `npm run lint` and `npm run build`.
- For runtime-sensitive surfaces such as MemoDocs, Monaco, Vditor, charts, or
  responsive layout, verify in browser screenshots or DOM checks when practical.
- Leave generated screenshots, archives, and local artifacts out of commits unless
  explicitly requested.
