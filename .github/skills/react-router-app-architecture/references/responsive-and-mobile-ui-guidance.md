# Responsive And Mobile UI Guidance

## Goal

Keep the UI usable, readable, and touch-friendly from narrow mobile screens up
through desktop without forking the product into separate architectures.

## Core Approach

- Design responsive layouts that stay capable on both mobile and desktop.
  Start from the most constrained layout when it helps, but do not treat a
  rigid mobile-first process as mandatory.
- Use fluid layout primitives such as CSS Grid, Flexbox, intrinsic sizing, and
  percentage or `clamp()`-based sizing before adding hard breakpoints.
- Choose breakpoints from content pressure, not from device marketing names.
- Keep media, cards, panels, and data containers able to shrink or wrap
  instead of assuming desktop width.

## Document And Viewport Basics

- Keep the standard mobile viewport meta tag in the document head:
  `width=device-width, initial-scale=1`.
- Add `viewport-fit=cover` only when the layout intentionally handles
  safe-area insets.
- Treat ordinary app content as needing reflow at narrow widths rather than
  requiring two-dimensional scrolling.
- Do not lock orientation unless one orientation is genuinely essential to the
  task.

## Interaction On Touch Devices

- Do not make hover the only way to discover required actions or meaning.
- Keep a touch path and keyboard path for the same important task.
- Meet at least WCAG 2.2 minimum target size expectations or provide
  equivalent spacing between targets.
- Prefer larger touch targets when practical, especially for primary actions,
  icon-only buttons, and dismiss controls.
- Be careful with sticky headers, sticky footers, and bottom action bars so
  they do not hide inputs or validation on small screens.

## Data-Dense UI On Small Screens

- If a table, chart, or filter bar becomes unreadable on mobile, adapt the
  presentation instead of shrinking everything uniformly.
- Prefer stacked summaries, progressive disclosure, card views, or drill-in
  patterns before forcing dense desktop layouts onto mobile.
- Use horizontal scrolling for dense data only when the structure truly
  requires it, and keep labels or context understandable when scrolling.
- Keep chart labels, legends, and summaries readable on small screens, and
  provide a non-hover path to key values.

## Architecture Placement

- Keep responsive layout behavior in component-scoped styles — the
  component's own CSS Module or `makeStyles` block — and in presentational
  components by default. Do not introduce global media queries for
  feature-specific layout.
- Move viewport-aware state into `app/lib/client/usecase/<feature>/` only when
  it affects interaction flow, data loading, or feature behavior rather than
  pure presentation.
- Keep device detection and browser capability checks in client infrastructure
  when they are truly required.

## Verification

- Check at least one desktop viewport and one narrow mobile viewport for
  UI-affecting changes.
- If orientation matters, verify both portrait and landscape.
- Confirm there is no accidental horizontal scrolling, clipped primary action,
  unreadable text, or hidden validation on the touched flow.
- Re-check Tooltip, Popover, chart, table, and dialog behavior on touch-sized
  layouts, not just desktop.
