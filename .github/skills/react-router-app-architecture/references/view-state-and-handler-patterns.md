# View State And Handler Patterns

## When To Add A View State Module

Add a use-case module when at least one is true:

- the view has more than one piece of related state
- multiple handlers mutate that state
- async fetching, optimistic updates, or retries are involved
- derived values are non-trivial
- the same screen needs to be reused or tested with different data

For a single boolean toggle, keep state in the component.

## Suggested Structure

For a feature `thread-composer`:

```text
app/lib/client/usecase/thread-composer/
  use-thread-composer.ts
  state.ts
  reducer.ts
  selectors.ts
  handlers.ts
  types.ts
```

Use this responsibility split:

- `types.ts`: state-only types used by reducer, selectors, and handlers
- `state.ts`: initial state factory and state shape definition
- `reducer.ts`: reducer plus action types
- `selectors.ts`: derived values such as visible items, computed flags, view
  models
- `handlers.ts`: event-to-dispatch mapping, async command helpers, error
  mapping
- `use-thread-composer.ts`: the public Hook or controller entry point

For very small features, two files such as `use-feature.ts` and `state.ts`
are enough.

## Component Contract

Components should:

- accept props from the use case
- call passed handlers
- render JSX
- hold ephemeral UI state only when it is genuinely view-local

Components should not:

- import server modules or ORM/data clients
- call API clients directly
- own reducer logic
- own async orchestration
- own derived business view models

## Component File And Styling Rules

Apply these rules to every component file, in addition to the contract above:

- One React component per `.tsx` file. The file name (`PascalCase.tsx`) and
  the primary exported component name match exactly.
- Co-define the component's `Props` type in the same file as
  `type <ComponentName>Props = { ... }`.
- Default to Fluent UI React v9 primitives; use `makeStyles` with Fluent UI
  tokens for theme-aware visuals layered on those primitives.
- Default to a sibling `<ComponentName>.module.css` for component-owned
  structural styling, imported as
  `import styles from "./<ComponentName>.module.css";`. Do not import
  non-module CSS files inside a component.
- Reserve global CSS for resets, font loading, baseline body styles, and the
  one-time `FluentProvider` host wiring under `app/styles/`.

See
[`component-file-and-css-module-rules.md`](component-file-and-css-module-rules.md)
for the full set of file-shape, Fluent UI, and CSS Module conventions,
including the narrow exception for tiny private sub-components.

## Action Granularity

Prefer descriptive action names that match user intent:

- `composerOpened`
- `messageDraftEdited`
- `attachmentRemoved`
- `submitFailed`

Avoid generic action names such as `SET_STATE`, `UPDATE_FIELD`, or `RESET`
when they hide what the user actually did.

## Selector Rule

Keep selectors pure and dependency-free.

Selectors should:

- take the state shape as input
- return derived values
- not call APIs
- not trigger side effects
- not mutate state

When a derived value needs request data, move that work into the use case
Hook, not into a selector.

## Handler Rule

Handlers can:

- read current state through a passed selector or snapshot
- call async dependencies through the use case
- dispatch reducer actions

Handlers should not:

- own UI rendering
- import React components
- write directly to module-level mutable state

## Hook Contract

A `use-<feature>.ts` Hook should:

- accept its dependencies through its argument list or React Context
- expose a small interface to the view layer
- combine state, selectors, and handlers into a single object suitable for the
  view
- own subscription cleanup

A view should be able to swap one Hook implementation for another in tests
without changing the component tree.
