# Client Layer Responsibilities

## `app/routes/`

- Define route modules.
- Read loader and action inputs.
- Map route data into page composition.
- Delegate non-trivial logic to `client/usecase` or `server/usecase`.

Do not call ORM clients, SDK clients, or other server-infrastructure modules
directly; hide business rules in route files; or let route modules become the
main state container for reusable interactions.

## `app/components/<feature>/`

- Render UI from props.
- Hold tiny UI-local state only when it is truly view-local, such as popover
  open state, active tab index, focused element, or an uncontrolled input
  bridge.

Do not fetch data, own mutation orchestration, build DTOs for the server,
contain business rules or persistence rules, or introduce component-local
`state/` or `reducers/` directories.

## `app/components/shared/`

- Hold reusable presentational primitives.
- Keep these components styleable and composable.
- Treat `shared` as an extraction target, not as the default starting location
  for new components.

Promote a component to `shared` only when:

1. it is used or clearly about to be used across multiple features
2. the API can be expressed in generic UI terms rather than product vocabulary
3. it does not need feature-specific state or handlers
4. extraction reduces duplication without turning it into a configurable
   monster

Do not import feature use cases, own feature vocabulary, or hide data fetching
or mutation logic in `shared`.

## `app/lib/client/usecase/`

- Own screen-level state and event handlers.
- Assemble view models for components.
- Coordinate API clients, router calls, optimistic UI, reducers, and derived
  state.
- Hold use-case-local DTO mapping when that mapping is part of the interaction
  flow.
- Expose a stable interface for the view layer.

Prefer a feature directory when more than one file is needed for the same
flow.

Reserve `store.ts` for cases where shared identity and lifecycle actually
matter across multiple sibling views. Do not default to a store when a Hook
plus reducer is enough.

## `app/lib/client/infrastructure/`

- Implement browser-facing and network-facing adapters.
- Wrap `fetch`, `localStorage`, clipboard, browser events, and router
  integration.
- Translate transport details into domain-friendly or usecase-friendly
  interfaces.

Keep framework adapters here, not in a generic common layer.

## `app/lib/client/infrastructure/api/`

- Hold feature or resource API clients.
- Keep transport and serialization details here.
- Return shapes that the owning use case can consume directly.
- Default to JSON DTOs, not domain entity instances.
- Keep client-side mapping shallow here; put use-case-specific reconstruction
  in the owning use case.

Client flow should normally be:

```text
client/usecase
  -> client/infrastructure/api
  -> HTTP JSON DTO
  -> server/usecase
```

## `app/lib/client/infrastructure/browser/`

- Hold browser-only integrations such as `localStorage`, `sessionStorage`,
  clipboard, media query, `BroadcastChannel`, `IntersectionObserver`, or
  document event adapters.
- Keep direct DOM and browser API calls here unless the logic is tiny and
  strictly component-local.

## Optional `client/infrastructure/repositories/`

Do not make this a baseline directory.

Add a client-side repository layer only when the client must hide multiple
data sources behind one abstraction, for example:

- IndexedDB plus remote API
- memory cache plus remote fetch
- offline-first synchronization
- local-first conflict handling

If the client is only calling HTTP endpoints, prefer `api/` instead of a
repository abstraction.
