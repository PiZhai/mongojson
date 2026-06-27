# MongoJSON Frontend Design System

This is the source of truth for MongoJSON frontend UI decisions. New screens,
components, and style changes must follow the detailed requirements in
[`docs/frontend-ui-design-guidelines.md`](../../docs/frontend-ui-design-guidelines.md).

## Direction

- Build a light iOS-style productivity workbench for developer/data tooling.
- Keep workflows functional-first: `/tools/inspect`, JSON, MongoDB JSON,
  visualization, and MemoDocs should open directly into usable tool surfaces.
- Do not introduce a dark theme, landing-page hero, marketing layout, or
  decorative visual system unless the product direction explicitly changes.

## Core Tokens

- Background: `#f5f7fb` with very light blue/teal page wash only.
- Primary: iOS system blue `#007aff`; strong state `#005ecb`.
- Semantic accents: teal `#0f9f8f`, orange `#f97316`, purple `#7c3aed`,
  pink `#e54874`, success `#2f9e44`, warning `#c56a05`, error `#d92d4b`.
- Surfaces: white or translucent white glass, never dark glass.
- Radius: default `8px`; use `999px` only for chips, pills, and circular controls.
- Shadows: low, soft depth only; no heavy card stacks or dramatic glows.
- Typography: system sans for UI, mono only for code/data.

## Required Layout Behavior

- Desktop first, but usable at `375px`, `768px`, `1024px`, and `1440px`.
- App shell keeps a persistent sidebar/header workbench structure.
- Panels and editors must preserve stable dimensions and avoid horizontal scroll.
- Cards are for repeated items or true framed tools only; do not nest cards.

## Delivery Checklist

- [ ] Uses the shared CSS tokens instead of one-off colors.
- [ ] Stays in the light iOS workbench style.
- [ ] Preserves existing routes and tool workflows.
- [ ] Has visible hover, focus, disabled, loading, success, and error states.
- [ ] Has no emoji icons, dark theme controls, decorative orbs, or hero pages.
- [ ] Does not globally break Monaco, Vditor, or code/editor selection styling.
- [ ] Verifies `npm run lint`, `npm run build`, and responsive widths when UI changes.
