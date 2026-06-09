# Chart And Data Visualization Guidance

## Goal

Use the simplest accessible chart that answers the product question without
overloading the canvas.

## Choose The Chart Deliberately

- Use line charts for continuous trends over time.
- Use bar or column charts to compare values across discrete categories or
  distinct periods.
- Use combo charts only when line and bar marks truly share the same X-axis
  and the comparison would be weaker as separate views.
- Add a secondary Y-axis only when measures genuinely have different scales,
  and label both axes explicitly.

## Keep The Visual Quiet

- Keep data ink as the primary focus. Avoid 3D effects, heavy shadows,
  decorative gradients, and non-informational chrome.
- Keep axis labels readable and keep gridlines visually secondary.
- Use theme-aware colors instead of ad hoc hardcoded palettes.
- Keep chart titles, legends, and units short and easy to scan.
- Match legend markers to the actual chart geometry so users do not decode
  shapes twice.

## Labels, Tooltip, And Interaction

- Keep key context visible: chart title, timeframe, units, series names,
  thresholds, and major status cues.
- Use Tooltip for supplemental inspection, not as the only place where users
  can discover the chart's meaning.
- Keep interactions simple and direct. Add filtering, drilldown, or zoom only
  when they materially improve exploration.
- If a chart becomes crowded, reduce the number of visible series or split the
  view instead of stacking more colors, legends, and labels into one canvas.

## Motion

- Animate only meaningful state changes.
- If both axes and marks change, transition the axes first and the data
  second.
- Avoid independent animation of many marks at the same time.
- Prefer subtle motion that helps users track change instead of decorative
  animation.

## Accessibility

- Never rely on color alone. Add labels, shape differences, annotations, or
  patterns when distinctions matter.
- Keep keyboard access and focus treatment intentional for interactive chart
  controls.
- For important charts, provide a nearby text summary and, when exact values
  matter, an accessible table or equivalent non-hover path to the data.
- If the chart behaves like a complex image, connect it to a visible text
  description with standard accessible markup.
- Do not hide critical insight, error state, or threshold meaning only inside
  a hover Tooltip.

## Architecture Placement

- Put DTO-to-series mapping, filters, grouping, sorting, and drill state in
  `app/lib/client/usecase/<feature>/`.
- Keep shared chart shells, wrappers, and common chart presentation primitives
  in `app/components/shared/`.
- Keep feature-specific chart views near the owning feature component.
- Treat chart library adapters as client-side UI infrastructure only when they
  need a reusable wrapper or abstraction boundary.
