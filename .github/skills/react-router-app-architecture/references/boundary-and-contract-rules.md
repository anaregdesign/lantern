# Boundary And Contract Rules

## Dependency Direction

Follow this direction strictly:

```text
routes/components
  -> client/usecase
  -> client/infrastructure
  -> domain

components/shared
  -> nothing application-specific

server/usecase
  -> domain
  -> domain/repositories

server/infrastructure
  -> domain
  -> external systems
```

Treat `domain` as the inner policy layer and `server/infrastructure` as the
outer integration layer.

## Contract Placement Rule

Do not move API `Request` or `Response` structures into `domain` just because
they are reused.

Use this decision order:

1. Keep route-specific transport shapes near the owning route or server use
   case.
2. Keep client transport DTOs near the owning API client.
3. Promote a type into `domain` only when it is a real business concept with
   invariants.
4. If transport contracts become broadly shared across many boundaries,
   introduce a dedicated `app/lib/contracts/` directory later instead of
   polluting `domain`.

## Constant Placement Rule

Do not create a global constants dump by default.

Use this order:

1. keep a literal local when it is obvious and used once
2. extract to the nearest owning module when the name improves readability
3. extract to a feature-level `constants.ts` only when several modules in the
   same feature share it
4. promote to a wider scope only when the constant is truly cross-feature and
   stable

Use `domain` for constants only when the value is really a domain concept.

## Validation Ownership Rule

Validate at the narrowest boundary that can correctly own the rule.

Use this split:

- routes and API adapters: request shape, query parsing, content-type,
  required fields, basic schema validation
- use cases: permission checks, workflow preconditions, cross-field application
  rules
- domain: business invariants that must always hold
- infrastructure: persistence constraints and external service response
  validation

Do not push domain invariants outward just because the route can reject early,
and do not pull HTTP-specific validation into `domain`.

## Error Mapping Rule

Keep error categories explicit:

- transport errors
- application or use-case errors
- domain errors
- infrastructure errors

Use cases should return or throw errors that still make sense without HTTP.
Route modules should translate those errors into status codes and response
envelopes.

## Authorization Ownership Rule

Keep authentication and authorization distinct.

Use this split:

- route or server entry: resolve the current principal or session
- use case: decide whether the requested operation is allowed
- domain policy: enforce reusable business authorization rules when they are
  part of the domain language

Do not bury authorization in repositories, and do not make UI visibility checks
the only access control.

## Serialization Rule

Convert between transport, persistence, and domain shapes at explicit
boundaries.

Do not let these cross boundaries unchanged without intent:

- `Date`
- database ids
- money-like values
- enums with transport-specific names
- value objects

Prefer DTOs with primitives over leaking runtime-rich objects across HTTP.

## Import Smell Checks

Treat these as architecture failures:

- `components` importing server modules or ORM/SDK data clients
- `components/shared` importing feature-specific client use cases
- `client/usecase` importing server modules
- `domain` importing React Router, browser APIs, ORM clients, or any
  infrastructure SDK
- `server/usecase` depending on concrete repository classes when a repository
  port would do
- transport `Request` or `Response` types being moved into `domain` without
  real business invariants
- `usecase` code constructing its own repositories or gateways with `new`
- module-level mutable server state holding request or user context
- circular imports across route, use case, or infrastructure boundaries
- authorization enforced only in the UI with no server-side check
- persistence or transport serialization leaking directly into domain rules
