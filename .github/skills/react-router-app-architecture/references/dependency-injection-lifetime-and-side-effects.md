# Dependency Injection, Lifetime, And Side Effects

## Dependency Injection Rule

Keep dependencies explicit and easy to substitute.

Prefer:

- constructor injection for classes
- explicit function parameters for stateless helpers
- factory functions for assembly at routes, server entry points, or dedicated
  composition modules

Avoid:

- service locators that hide what a use case depends on
- module-level mutable singletons that carry domain state
- random direct imports of concrete repositories or gateways inside
  `usecase` or `domain`

Introduce a DI container only when the wiring complexity justifies it.

## Runtime Safety Rule

Treat thread safety in this codebase mainly as async safety and request
safety.

Use these rules:

- avoid module-level mutable state that is read or written by multiple
  requests
- keep request context, user context, auth context, and correlation metadata
  passed explicitly per call
- recreate transaction-scoped dependencies such as transaction-bound
  repositories per request or per transaction
- guard client-side concurrent flows against stale responses, such as
  multiple in-flight searches or rapid form retries

## Side Effect And Migration Rule

Make schema changes and runtime side effects explicit and reviewable.

Keep these workflows owned by `server/infrastructure` and the deployment
process:

- schema migrations
- seed data scripts
- background job triggers
- external notifications
- async fan-out

Do not bury migrations or side effects inside random route handlers, ad hoc
scripts, or unannotated repository methods.

## Lifetime Rule

Choose lifetimes deliberately for server-side dependencies.

Use this default:

- prefer per-request lifetime for use cases and repositories that interact
  with request-bound data, especially when they share a transaction or auth
  context
- prefer singleton lifetime for expensive stateless ORM/SDK clients such as
  database connection pools or HTTP SDK clients
- prefer transient lifetime for cheap stateless helpers and pure adapters

Avoid hidden global state that leaks across requests, and never share a single
mutable use-case instance across concurrent requests just to save allocation.

## Migration Workflow Rule

When changing architecture terms or boundaries, prefer the lowest-friction
migration path.

Use this order:

1. choose the canonical new name or shape
2. perform a direct replacement across the codebase in one focused change
3. update tests, types, and documentation in the same change
4. retain a compatibility alias only when a real consumer cannot migrate in
   the same change set
5. remove the alias as soon as the blocking consumer migrates

Avoid:

- introducing parallel old and new names that drift in scope
- keeping deprecated names alive past their stated removal point
