# Stateful Flow Compromises

Use these rules for flows such as chat, streaming generation, multi-step
wizards, live playback, or long-lived session handling.

## Principle

Do not pretend every highly stateful flow can stay as a simple stateless Hook
plus presentational view split.

When identity, long-lived runtime handles, streaming state, or cancellation
semantics matter, allow a controlled compromise:

- a feature-local controller
- a feature-local store
- a reducer-backed state machine

Keep the compromise contained to the owning feature directory.

## Where To Put It

Use a feature directory under `app/lib/client/usecase/`.

Preferred shape:

```text
app/lib/client/usecase/chat-session/
  use-chat-session.ts
  controller.ts
  store.ts
  state.ts
  reducer.ts
  selectors.ts
  handlers.ts
```

Do not promote these modules to top-level `app/stores` or global client state
by default.

## When A Store Is Justified

Allow `store.ts` when at least one of these is true:

- multiple sibling views must observe the same live session
- the feature owns long-lived identity beyond a single form submit
- streaming or incremental updates arrive over time
- cancellation, replay, resume, or reconnect logic exists
- runtime handles such as `AbortController`, stream readers, or subscriptions
  must be coordinated

If none of these are true, prefer a Hook plus reducer without a dedicated
store.

## State Split Rule

Separate the state into three categories.

### Durable state

State that should survive rerenders and often persistence boundaries.

Examples:

- message list
- session metadata
- wizard step data
- playback bookmark

### Ephemeral runtime state

State that exists only while a live operation is running.

Examples:

- active request id
- abort controller
- current stream chunk buffer
- retry backoff state
- current pending phase

### Infrastructure handles

Objects that talk to the outside world.

Examples:

- SSE reader
- WebSocket handle
- audio context
- timer registration
- session pool handle

Do not mix these categories into one anonymous state blob.

## Controlled Architecture Compromise

For a stateful feature, this is an acceptable compromise:

- components remain mostly render-only
- route modules remain shell and HTTP wiring
- a feature-local controller or store owns lifecycle-heavy orchestration
- infrastructure adapters remain separate from the controller

This is better than forcing streaming logic into:

- route modules
- presentational components
- generic global stores
- ad-hoc module-level mutable variables

## Event And Effect Rule

- Keep user intent as explicit actions or handler calls.
- Keep stream events as separate actions from user events.
- Use Effects only to attach or detach external systems.
- Keep reducer transitions pure even when the controller is stateful.

Prefer explicit phases such as:

```text
idle -> submitting -> streaming -> succeeded
idle -> submitting -> failed
idle -> submitting -> cancelled
```

## Async Safety Rule

Stateful flows must guard against stale completion.

Use one or more of these:

- `AbortController`
- request ids
- session ids
- stream token comparison
- current-phase guards

Never let an old response or stream continue mutating state after a newer
intent has replaced it.

## External Store Rule

Use `useSyncExternalStore` only when multiple components must subscribe to the
same live feature state and a Hook-local reducer is no longer enough.

Do not introduce an external store merely to avoid prop drilling if the state
still belongs to a single screen.

## React Router Compromise Rule

Use React Router state first for ordinary request lifecycle:

- loaders for reads
- actions for route-owned writes
- fetchers for non-navigation writes

But for chat-like streaming or session-oriented flows, allow a feature-local
runtime store for the parts React Router does not model well:

- token streaming
- incremental partial updates
- reconnect
- abort and resume
- live session coordination

## General Examples

- `chat-session/store.ts`: keep streaming messages, request ids, and
  cancellation state
- `wizard-session/reducer.ts`: keep multi-step transition logic explicit
- `media-playback/controller.ts`: coordinate playback state, timers, and
  external player events
- `generation-session/handlers.ts`: submit prompt, receive stream chunks,
  handle cancel and retry

## Smells

Refactor when you see:

- chat state spread across several unrelated components
- stream readers or abort controllers inside presentational components
- route files managing incremental streaming state directly
- generic global store introduced for only one screen
- reducer state mixed with live infrastructure handles in untyped bags
- runtime handles persisted as if they were durable domain data
