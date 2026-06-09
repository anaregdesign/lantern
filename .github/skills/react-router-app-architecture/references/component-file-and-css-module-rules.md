# Component File And CSS Module Rules

Use these rules whenever you create or modify a React component, its styling,
or its file layout. They sit alongside
[`layout-and-module-placement.md`](layout-and-module-placement.md) and
[`view-state-and-handler-patterns.md`](view-state-and-handler-patterns.md) and
take precedence on file-shape and styling questions.

## Goal

- Keep each component independently readable, movable, and testable.
- Keep style impact local to the component that owns it.
- Keep Fluent UI as the default UI vocabulary and let CSS Modules cover the
  remaining structural and presentational gaps.

## One Component Per File

- A `.tsx` file exports exactly one React component as its primary, file-named
  export.
- File name and component name match exactly. `UserMenu.tsx` exports
  `UserMenu`; `ProfileEditorForm.tsx` exports `ProfileEditorForm`.
- Do not co-define a second top-level component in the same file just because
  the two are used together. Each gets its own file.
- Tiny presentational helpers that are clearly private to the component
  (e.g. a one-screen-use sub-row that never renders elsewhere) may stay in the
  same file when all of the following hold:
  - it is not exported
  - it does not hold its own state or effects
  - extracting it would not improve readability
  When in doubt, give it its own file.
- Pure utility helpers, hooks, types, and constants used by the component
  belong in sibling files, not appended to the component file.

## Filename And Export Conventions

- Component files: `PascalCase.tsx` matching the exported component name.
- Use `.tsx` only when the file renders JSX. Plain logic stays in `.ts`.
- Prefer named exports for components. Use a `default` export only when an
  external tool (e.g. a route module convention) requires it.
- Do not introduce barrel `index.ts` files inside `app/components/<feature>/`
  by default. Import components directly from their file. Add an `index.ts`
  only when the feature directory is intentionally a public boundary.
- The component's prop type is co-defined in the component file as
  `type <ComponentName>Props = { ... }`. Move it to a sibling
  `<ComponentName>.types.ts` only when the type is shared with another module
  in the same feature.

## Component File Layout

Preferred sibling layout for a feature-local component:

```text
app/components/profile-editor/
  ProfileEditorForm.tsx
  ProfileEditorForm.module.css
  ProfileSummaryCard.tsx
  ProfileSummaryCard.module.css
```

Inside `ProfileEditorForm.tsx`, keep this order:

1. imports (Fluent UI, React, sibling modules, then CSS Module)
2. local types (`type ProfileEditorFormProps = { ... }`)
3. the component function declaration
4. small private sub-components or helpers, if any, that satisfy the
   one-file-helper exception above

Do not let a component file own:

- async orchestration → put it in
  `app/lib/client/usecase/<feature>/`
- reducer logic → same
- API calls → put them in `app/lib/client/infrastructure/api/`
- server modules, ORM clients, or SDK clients

## Fluent UI Usage

- Default to `@fluentui/react-components` primitives (`Button`, `Field`,
  `Input`, `Dropdown`, `Dialog`, `Toolbar`, `MessageBar`, etc.) before
  introducing custom low-level controls.
- Use `@fluentui/react-icons` for iconography.
- Wrap the app root in `FluentProvider` with `webLightTheme` (or the project's
  documented theme) once, at the highest sensible boundary.
- Prefer Fluent UI's documented composition patterns:
  - `Field` for form rows with label, hint, and validation
  - `MessageBar` for inline status, not custom toasts
  - `Dialog`, `Drawer`, `Popover`, `Menu` from Fluent UI rather than custom
    overlays
  - `Tooltip` and `InfoLabel` only for supplemental, non-essential detail
- Keep primary labels, required-field markers, validation messages, and
  critical status visible without depending on hover.
- Respect Fluent UI tokens (`tokens.spacingHorizontalM`, etc.) instead of
  hand-typed pixel values when the surrounding style is theme-aware.
- Do not mix two general-purpose component libraries in the same app. If the
  repository already has an established design system, follow that system
  instead of layering Fluent UI on top.

### When To Use `makeStyles` Versus A CSS Module

Fluent UI ships its own atomic styling layer (`makeStyles` from
`@fluentui/react-components`, backed by Griffel). Use it for component styles
that need to be **theme-aware** or that override Fluent UI primitives:

- token-driven colors, spacing, typography (`tokens.colorNeutralForeground1`,
  `tokens.spacingVerticalS`)
- hover, focus, pressed, and disabled visuals layered on a Fluent UI element
- RTL-aware logical properties (`marginInlineStart`, `paddingBlock`)
- style variants computed from props at render time

Use a colocated CSS Module (`<ComponentName>.module.css`) for component-level
**structural** styling that is not theme-token sensitive:

- grid and flex layout for the component shell
- responsive breakpoints for the component's own layout
- positional concerns (sticky headers, side rails, page templates)
- decorative wrappers around charts or media

Both can coexist inside one component: Fluent UI tokens via `makeStyles` for
visuals on top of Fluent primitives, CSS Module for the surrounding layout
scaffolding the component owns.

Do **not** reach for inline `style` props for anything beyond one-off
runtime-computed values (e.g. a dynamic width derived from measurement).
Inline style is not a substitute for either pathway above.

## CSS Modules Are The Default

- Every component that needs custom CSS owns a sibling
  `<ComponentName>.module.css`.
- Import it as `import styles from "./<ComponentName>.module.css";` and apply
  classes through the `styles` object. Never use string class names that
  bypass the module scope.
- Class names inside `.module.css` are written in `camelCase` (or
  `kebab-case` consistently across the project) and describe the role of the
  element inside the component (`root`, `header`, `actions`, `emptyState`)
  rather than restating presentation (`redBold`, `marginTop20`).
- Keep selectors as flat as possible:
  - one-level class selectors are preferred
  - nested selectors are allowed only inside `:global(...)` escape hatches or
    for short structural relationships (`.row > .actions`)
  - avoid element-name selectors that match across the document (`div`,
    `span`, `button`) inside a CSS Module
- Compose shared visual fragments with `composes:` inside the same module or
  from another CSS Module rather than copy-pasting selector bodies.
- Do not use Tailwind-style utility classes or other untyped global class
  names in app components. If a project intentionally adopts a utility
  framework, document the deviation explicitly and confine it to that
  decision.

## Global CSS Is The Exception

Global CSS is allowed only for a small, named set of concerns and lives in a
clearly labeled location such as `app/styles/`:

- CSS reset or normalize
- web-font loading and `@font-face` declarations
- baseline body and root styles (background, default text rendering)
- one-time `FluentProvider` host element wiring

Anything beyond this list should be a CSS Module owned by a specific
component or layout, not a new global rule.

Forbidden in global stylesheets:

- feature-specific selectors
- component-name selectors (e.g. `.user-menu`, `.profile-card`)
- overrides targeting Fluent UI internal class names

## Responsive Styles Are Component-Scoped

- Media queries for a component's own layout live in that component's CSS
  Module, not in a global stylesheet.
- Use content-driven breakpoints chosen from the component's actual layout
  pressure, not device-named breakpoints copy-pasted across the app.
- When responsive behavior changes interaction flow or data loading (not just
  presentation), move that decision into `app/lib/client/usecase/<feature>/`
  and keep the CSS Module focused on visual reflow only.

## Forbidden Patterns

- Two exported components in one `.tsx` file.
- Component filename and exported component name disagreeing.
- Importing a CSS file globally (`import "./styles.css"`) inside a component
  module. Component-owned styles must go through `.module.css`.
- Inline `style={{ ... }}` for static styling that belongs in a CSS Module or
  `makeStyles`.
- A `.module.css` file that is not colocated with the component it styles.
- Hidden helper components growing their own state, effects, or async logic
  while still living inside another component's file.
- Generic shared CSS files such as `common.css`, `helpers.css`, or
  `utils.css` introduced as a styling dumping ground.

## Verification

Use these checks alongside [`verification-gates.md`](verification-gates.md):

```bash
# One component per file: every .tsx in app/components exports exactly one
# top-level React component whose name matches the file.
rg -n "^export (default |const |function )" app/components

# CSS Modules colocated with components: every styled component has a sibling
# .module.css.
find app/components -name "*.tsx" -print
find app/components -name "*.module.css" -print

# No global CSS imports inside components.
rg -n "import .*\.css['\"]" app/components | rg -v "\.module\.css"

# No inline style props except for clearly dynamic values.
rg -n "style=\{\{" app/components
```

Treat the results as signals, not hard failures. Investigate any match, and
either fix it or document why this component is the deliberate exception.
