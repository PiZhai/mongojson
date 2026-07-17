# Requirements: Project-wide Minimum Grid Layout

## Goal

The complete frontend uses one responsive contract: page-level minimum grids preserve the relationship between major regions, while components protect their own minimum sizes and reflow only internally. Narrow desktop and landscape layouts scroll inside the active workspace. Only portrait viewports at or below 768 CSS pixels use a stacked mobile layout.

## Acceptance Criteria

1. App Shell keeps a 228px expanded or 72px collapsed sidebar on desktop and landscape viewports.
2. Inspect, JSON, MongoDB JSON, Visualization, Memo, Music and Watch Party retain their declared horizontal grids outside the portrait-phone condition.
3. A workspace narrower than its aggregate minimum width owns its horizontal scrollbar; the document root must not overflow.
4. No two major layout regions overlap, and editor, rail, card and toolbar minimum sizes are respected.
5. Memo keeps outline, editor and 380–400px card rail side by side from a 1080px content width; card controls remain contained.
6. `@media (orientation: portrait) and (max-width: 768px)` is the only width-based structural media query.
7. Component-local container queries may only compact component controls and must not change page columns.
8. Layout dimensions and allowed query values come from `layout-constants.json` and pass the static validator.
9. Existing data, editing, persistence, media and synchronization behavior is unchanged.
