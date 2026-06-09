# Server And Domain Layer Responsibilities

## `app/lib/domain/entities/`

- Hold business concepts that enforce invariants or domain behavior.
- Use `class` when identity or invariants matter.
- Prefer named methods that express business operations over generic setters.
- Keep entities free from React, ORM clients, browser APIs, and HTTP concerns.

Do not place `CreateXRequest`, `UpdateXPayload`, or `ListXResponse` types here
unless they are genuinely domain concepts, which is rare.

## `app/lib/domain/value-objects/`

- Hold small immutable domain concepts with validation and equality semantics.
- Prefer validated factory functions or constructors over raw object literals.
- Keep them free from transport, framework, and persistence details.

Do not use value objects as a disguised home for endpoint DTOs.

## `app/lib/domain/policies/`

- Hold business rules that span multiple entities or require explicit decision
  logic.
- Prefer this directory when the code reads like a rule or policy statement.

## `app/lib/domain/services/`

- Hold domain-level orchestration that is still infrastructure-free and not
  naturally owned by a single entity or value object.
- Use this directory sparingly.
- Prefer `policies/` when the code is fundamentally a rule, and prefer
  `services/` only when it is true domain orchestration.

## `app/lib/domain/repositories/`

- Define repository ports as interfaces or types.
- Describe what the domain or use case needs from persistence.
- Keep these contracts independent from any specific ORM, query builder, or
  storage schema.
- Treat these as domain-facing persistence ports first.

Do not turn repository ports into generic request or response contract
storage.

## `app/lib/server/usecase/`

- Implement server-side application services.
- Orchestrate repositories and domain rules.
- Map between route inputs and domain operations.
- Accept repositories and gateways through constructors or explicit function
  parameters.
- Keep HTTP response formatting and status selection out of this layer.
- Keep authorization checks and side-effect decisions visible in this layer.

Do not leak ORM types into domain or client layers, or instantiate concrete
infrastructure-backed repositories inline with `new`.

## `app/lib/server/infrastructure/`

- Hold ORM clients, query builders, SDK clients, repository implementations,
  external API gateways, filesystem access, and environment-aware wiring.
- Map storage or service details into repository ports.
- Hold explicit job or side-effect adapters when a use case must trigger
  background work.

This is the only layer that should know about the project's chosen ORM, SQL
client, or backend SDKs.

## `app/lib/server/infrastructure/repositories/`

- Hold repository implementations backed by the project's chosen data stack
  (ORM, query builder, raw SQL client, filesystem persistence, or remote
  backend).
- Accept long-lived clients such as a connection pool, ORM client, or SDK
  client through constructors so lifetime stays explicit.
- Keep repositories stateless with respect to request identity, auth state,
  and current transaction.

## `app/lib/server/infrastructure/gateways/`

- Hold integrations with external SDKs and remote services.
- Accept SDK clients or configuration through constructors or explicit factory
  helpers.
- Keep outbound adapter logic here rather than inside use cases.
