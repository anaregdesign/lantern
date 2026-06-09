# Domain Modeling And Type Rules

## Concept Ownership And Consolidation Rule

Do not keep parallel classes, variables, or functions that perform the same
job in the same boundary without a clear reason.

Prefer this rule:

- one concept
- one name
- one owner

Consolidate only when the modules represent the same concept in the same
boundary and differ only because of historical drift or accidental duplication.

Do not consolidate merely because names or fields look similar across
boundaries.

## Domain-Centered Application Rule

Build business behavior around domain language and domain models, but do not
build the entire application as if everything were a domain object.

Use the domain as the semantic center for:

- invariants
- value semantics
- lifecycle transitions
- business rule names

Keep HTTP DTOs, route parsing, view models, browser runtime state, and
persistence adapters outside `domain`.

## Object-Oriented Modeling Rule

Use object-oriented modeling selectively, not by default.

Prefer `class` when at least one of these is true:

- identity matters over time
- invariants must be protected together with state
- lifecycle transitions are part of the business language
- behavior belongs naturally on the model instead of in a detached helper

Prefer `type` plus pure functions when the module is mainly a DTO, parser,
mapper, stateless transform, or lightweight contract.

## Composition Over Inheritance Rule

Prefer composition over inheritance.

Use inheritance only when:

- the subtype relationship is stable
- substitutability is real
- shared behavior cannot be expressed more clearly through composition

Avoid deep class hierarchies for UI components, repositories, API clients, or
use cases.

## Anemic Model Boundary

Do not force every rule into classes, but also do not let domain models become
empty bags of fields.

Healthy split:

- entities and value objects protect their own invariants
- policies decide cross-entity rules
- domain services coordinate domain behavior that belongs to no single model
- use cases orchestrate application flow, permissions, persistence, and side
  effects

## Interface And Type Rule

Choose `interface` and `type` by role, not by habit.

Prefer `interface` when:

- the shape is an object-like port or contract
- multiple implementations are expected
- the contract is part of DI or architecture boundaries

Prefer `type` when:

- the shape is a DTO or response envelope
- the shape is local to one feature or one module
- unions, intersections, mapped types, tuples, or primitives are involved

Avoid `I*`-prefixed port names and one-off interfaces for local object
literals.

## Unknown Type Rule

Use `unknown` for data that has crossed a trust boundary but has not yet been
proven safe.

Best practice:

1. receive untrusted data as `unknown`
2. validate or narrow it immediately
3. convert it into a DTO, value object, or domain model
4. keep the validated shape flowing inward instead of the raw `unknown`

Treat `unknown` as a boundary quarantine type, not as a long-lived application
type.
