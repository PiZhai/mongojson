# Design: Project-wide Minimum Grid Layout

## Architecture

- `layout-frame` is the local scroll boundary and an inline-size container.
- `layout-min-grid` expresses a page grid with explicit track minimums and an aggregate minimum inline size.
- `layout-auto-grid` is reserved for cards, summaries and form fields that may reflow inside their component.
- `layout-cell` gives editors and panels a safe shrink boundary; `layout-toolbar` owns control wrapping.
- App Shell keeps its side grid except on portrait phones. Content width is measured in CSS pixels and never inferred from device type or pixel ratio.

## Workspace Contracts

| Workspace | Tracks | Aggregate minimum |
| --- | --- | ---: |
| Inspect | 720 + 280 + 14 | 1014px |
| JSON | 420 + 420 + 14 | 854px |
| MongoDB JSON | 420 + 420 + 14 | 854px |
| Visualization | 420 + 360 + 14 | 794px |
| Memo | 660 + 380–400 + 20 | 1080px |
| Music | 260–300 + 620 + 14 | 894px |
| Watch Party | 640 + 340 + 16 | 996px |

## Responsive Contract

Outside portrait phone mode, aggregate minimums are never removed: a narrow workspace scrolls horizontally without moving its rail below the primary region. In portrait phone mode, page grids become one column, Memo hides its outline, and shell navigation becomes vertical. The 400px component query compacts Memo card controls without changing the page grid.

## Verification

The static validator rejects unregistered width queries and drift between JSON size tokens and CSS variables. Playwright geometry tests assert track minimums, ordering, local overflow, portrait stacking and Memo toolbar containment across every route.
