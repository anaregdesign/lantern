---
name: react-router-app-architecture
description: "Own React Router app-code architecture, route boundaries, UI structure, and verification for Vite-based web apps. Use when the request mentions React Router routes, loaders or actions, component boundaries, use cases, domain models, server-side data access through repository ports, Fluent UI, responsive UI, charts, Playwright UI verification, or app-structure refactoring. Do not use this skill for `/docs/spec` planning, Azure platform setup, app registration, end-user auth contract design, release delivery, or for choosing a specific ORM or database engine."
---

# React Router App Architecture

## Overview

Use this skill as the default architecture workflow for a React Router SPA that
uses Vite. Use it from initial bootstrap through ongoing implementation. Keep
FlatRoute modules declarative, view files thin, data access server-only, and
dependency direction explicit before writing code.

This skill owns code structure, dependency direction, UI guardrails, and
verification for the app codebase. Do not use it to define cloud provider
choice, identity provisioning, secret-store topology, IaC layout, or deployment
infrastructure; let a companion hosting skill add those concerns while
preserving these rules.

This skill is also persistence-agnostic. It defines where the data access layer
lives, how it is shaped (repository ports in `domain`, repository
implementations and ORM/SDK code in `server/infrastructure`), and how it must
not leak across boundaries — but it does not pick an ORM, query builder, or
database engine. Plug in the data stack that fits the project (Drizzle, Kysely,
Prisma, TypeORM, raw `pg`, an HTTP backend, etc.) inside
`app/lib/server/infrastructure/` and keep the rest of the rules unchanged.

This skill also does not own `/docs/spec`, `/docs/plans/plan.md`, commit-log
workflow, branch naming workflow, or PR management; use a repository workflow
skill for those concerns and keep this skill focused on app code.

For new or unstandardized UI work, prefer Fluent UI React v9 and a quiet,
simple visual presentation. Keep primary labels and layouts concise, and move
only supplemental, non-essential detail into Tooltip or InfoLabel patterns.

When data visualization is required, prefer the simplest accessible chart that
matches the analytical task and keep chart interaction lightweight.

For responsive behavior, prefer guidance that keeps the same feature usable on
both desktop and mobile rather than treating `mobile-first` as a universal
process requirement.

If a companion hosting skill explicitly overrides runtime mode or config
bootstrap, keep these architecture and boundary rules and let the companion
override only the hosting-specific pieces.

## Quick Start

1. Always read
   [`references/layout-and-module-placement.md`](references/layout-and-module-placement.md)
   before deciding where code should live.
2. Run a placement preflight before implementing:
   - list every new file, moved file, and extracted file
   - assign the exact canonical target path for each file before writing code
   - if a file does not fit the canonical layout, stop and revise the placement
     plan before creating a new directory
3. Classify the requested change:
   - route composition
   - presentational UI
   - client interaction flow
   - server use case
   - persistence or external integration
   - domain rule
   - cross-boundary contract or utility
4. Place code in the canonical layout:
   - `app/routes/`
   - `app/components/<feature>/`
   - `app/components/shared/`
   - `app/lib/client/usecase/`
   - `app/lib/client/usecase/<feature>/use-<feature>.ts`
   - `app/lib/client/usecase/<feature>/state.ts`
   - `app/lib/client/usecase/<feature>/reducer.ts`
   - `app/lib/client/usecase/<feature>/selectors.ts`
   - `app/lib/client/usecase/<feature>/handlers.ts`
   - `app/lib/client/infrastructure/`
   - `app/lib/client/infrastructure/api/`
   - `app/lib/client/infrastructure/browser/`
   - `app/lib/server/usecase/`
   - `app/lib/server/infrastructure/`
   - `app/lib/server/infrastructure/config/`
   - `app/lib/server/infrastructure/repositories/`
   - `app/lib/server/infrastructure/gateways/`
   - `app/lib/domain/entities/`
   - `app/lib/domain/value-objects/`
   - `app/lib/domain/policies/`
   - `app/lib/domain/services/`
   - `app/lib/domain/repositories/`
5. Read the narrowest additional reference file after the layout reference:
   - project bootstrap and dependency install:
     [`references/project-bootstrap.md`](references/project-bootstrap.md)
   - architecture overview index for multi-boundary changes:
     [`references/layout-and-dependency-rules.md`](references/layout-and-dependency-rules.md)
   - client layer responsibilities:
     [`references/client-layer-responsibilities.md`](references/client-layer-responsibilities.md)
   - server and domain layer responsibilities:
     [`references/server-and-domain-layer-responsibilities.md`](references/server-and-domain-layer-responsibilities.md)
   - boundary, contract, and validation rules:
     [`references/boundary-and-contract-rules.md`](references/boundary-and-contract-rules.md)
   - domain modeling and type rules:
     [`references/domain-modeling-and-type-rules.md`](references/domain-modeling-and-type-rules.md)
   - dependency injection, lifetime, and side-effect rules:
     [`references/dependency-injection-lifetime-and-side-effects.md`](references/dependency-injection-lifetime-and-side-effects.md)
   - FlatRoute REST API rules:
     [`references/flat-route-rest-api-guidelines.md`](references/flat-route-rest-api-guidelines.md)
   - state and handler composition:
     [`references/view-state-and-handler-patterns.md`](references/view-state-and-handler-patterns.md)
   - component file, Fluent UI, and CSS Module rules:
     [`references/component-file-and-css-module-rules.md`](references/component-file-and-css-module-rules.md)
   - chart and data visualization guidance:
     [`references/chart-and-data-visualization-guidance.md`](references/chart-and-data-visualization-guidance.md)
   - responsive and mobile UI guidance:
     [`references/responsive-and-mobile-ui-guidance.md`](references/responsive-and-mobile-ui-guidance.md)
   - Playwright UI verification workflow:
     [`references/playwright-ui-verification.md`](references/playwright-ui-verification.md)
   - stateful flow compromise rules:
     [`references/stateful-flow-compromises.md`](references/stateful-flow-compromises.md)
   - hotspot refactor workflow:
     [`references/hotspot-refactor-workflow.md`](references/hotspot-refactor-workflow.md)
   - verification before push:
     [`references/verification-gates.md`](references/verification-gates.md)

## Non-Negotiable Rules

- Keep dependency direction inward:
  - `app/routes` and `app/components` depend on client-facing orchestration,
    never on server infrastructure or ORM/data-client code
  - `app/lib/client/usecase` depends on `domain` and client adapters
  - `app/lib/server/usecase` depends on `domain` and repository ports
  - `app/lib/server/infrastructure` implements repository ports and external
    integrations
  - `app/lib/domain/*` depends only on other domain modules
- Lock file placement before coding. Name the exact target file paths first,
  then implement.
- Keep feature-local presentational components in `app/components/<feature>/`.
  Use `app/components/shared/` only for feature-agnostic UI that is already
  reused or clearly about to be reused across features.
- Keep ORM clients, SDK clients, query builders, and direct database/network
  access inside `app/lib/server/infrastructure/`. Server use cases and the
  domain layer must talk to repository ports, not to concrete data clients.
- Keep `app/components/` presentational. Allow only ephemeral UI state there,
  such as local input focus or disclosure toggles.
- Prefer Fluent UI React v9 (`@fluentui/react-components`) for new UI work
  unless the repository already has a clear design-system standard or an
  approved migration plan says otherwise.
- Compose UI from documented Fluent UI primitives (`Field`, `Button`,
  `Dialog`, `MessageBar`, `Menu`, `Toolbar`, etc.) before introducing custom
  low-level controls, and use Fluent UI tokens through `makeStyles` for
  theme-aware visuals layered on those primitives.
- Do not mix two general-purpose component libraries in the same app. If a
  design system is already established, follow that system and document the
  deviation explicitly instead of layering Fluent UI on top.
- Keep UI visually simple: concise labels, low-noise layouts, restrained text
  density, and deliberate spacing over decorative complexity.
- Use Tooltip or InfoLabel for supplemental, non-essential detail. Do not hide
  required labels, key instructions, validation messages, or critical status
  only inside a Tooltip.
- When rendering charts, choose the simplest chart that matches the task: line
  charts for continuous trends over time, and bar or column charts for
  comparing discrete categories or ranked values.
- Keep charts low-noise and accessible: avoid 3D or decorative chart junk,
  avoid color-only encoding, and keep critical values or explanations
  discoverable without hover.
- For important charts, provide a nearby text summary and, when exact
  inspection matters, an accessible table or equivalent non-hover path to the
  underlying values.
- Build responsive UI so the same feature stays capable on both desktop and
  mobile, using fluid layout primitives and content-driven breakpoints rather
  than device-name-specific forks or fixed desktop widths.
- Support narrow-screen reflow and avoid ordinary horizontal scrolling for app
  UI. Do not lock orientation unless a single orientation is genuinely
  essential.
- Do not rely on hover-only or fine-pointer-only interaction for primary
  actions, labels, or critical explanation. Keep a touch and keyboard path for
  the same task.
- Keep touch targets and spacing mobile-usable. Meet at least WCAG 2.2 minimum
  target size expectations or provide equivalent spacing, and use larger
  targets when practical.
- One React component per `.tsx` file. The file name (`PascalCase.tsx`) and
  the primary exported component name must match exactly. Tiny private
  presentational helpers that have no state, no effects, and no exports may
  stay in the same file; everything else gets its own file.
- Default to CSS Modules for component-owned styling. Each component that
  needs custom CSS owns a sibling `<ComponentName>.module.css` colocated with
  the component, imported as `import styles from "./<ComponentName>.module.css";`
  and applied through the `styles` object. Do not import non-module CSS files
  inside components.
- Keep media queries and other responsive styles inside the component's own
  CSS Module (or `makeStyles`) so style scope stays bounded to the component
  that owns the layout.
- Reserve global CSS for a small, named set of concerns (reset, font loading,
  baseline body and root styles, one-time `FluentProvider` host wiring) and
  place it under `app/styles/`. Global stylesheets must not contain
  feature-specific or component-name selectors, and must not override Fluent
  UI internal class names.
- Avoid inline `style={{ ... }}` for static styling. Use `makeStyles` for
  theme-aware visuals on Fluent UI primitives and a colocated CSS Module for
  the component's structural layout; reserve inline style for genuinely
  dynamic runtime values.
- For UI-affecting changes, verify the actual rendered result with Playwright
  before pushing instead of relying only on code inspection.
- Prefer accessible locators and web-first assertions in Playwright, and check
  the touched flow at the relevant route and viewport sizes.
- Keep async state, mutation handlers, and derived view models in
  `app/lib/client/usecase/`.
- Co-locate `state`, `reducer`, `selector`, and `handler` modules inside the
  owning feature directory under `app/lib/client/usecase/<feature>/`.
- Do not create horizontal buckets such as `app/state/`, `app/reducers/`,
  `app/stores/`, `app/handlers/`, or `app/lib/client/usecase/state/`.
- Use FlatRoute (`@react-router/fs-routes` `flatRoutes()`) as the default
  routing convention. One file per route module under `app/routes/`, with
  dots in the file name mapping to URL slashes and `$segment` marking dynamic
  parameters. Deviate only when the project already uses a different
  convention, a third-party generator imposes one, or a specific framework
  integration requires it.
- Shape API URLs as resources, not actions: collection at `/items`, single
  resource at `/items/$itemId`, sub-collection at `/items/$itemId/comments`.
  Reserve verb-shaped paths (`/items/$itemId/publish`) for genuine operations
  that do not fit CRUD, and keep them rare.
- Keep route modules responsible for HTTP, loader/action wiring, and top-level
  composition only.
- Validate at the correct layer: transport shape in routes or API adapters,
  application rules in use cases, business invariants in domain.
- Default client-side data access to `api` adapters plus DTO mapping inside the
  owning use case. Introduce a client-side repository abstraction only for
  clear multi-source or local-first requirements.
- Instantiate repositories and gateways in a composition root or dependency
  factory, not inside domain models or use cases.
- Prefer React Router's official Vite-powered bootstrap for new projects. Use
  plain `create-vite` only when you intentionally choose a lower-level React
  Router mode or must retrofit an existing starter.
- Do not put cloud-provider provisioning, app registration, secret-store
  policy, IaC topology, or release-infrastructure rules in this skill. Keep
  those in a companion hosting skill while preserving these code-level
  boundaries.
- Do not pick the ORM, query builder, or database engine inside this skill.
  Keep that choice in the project's data-stack decision and confine all of its
  imports to `app/lib/server/infrastructure/`.
- Keep authorization, serialization, migration steps, background side effects,
  and barrel exports intentional rather than ad hoc.
- Treat thread safety mainly as async safety and request safety: avoid
  module-level mutable state, keep request context out of singletons, and
  rebuild transaction-scoped dependencies per request.
- Keep feature internals private by default and avoid circular dependencies
  across extracted modules.
- Use `class` only when identity, invariants, or lifecycle matter. Prefer
  composition over inheritance, and keep DTO or transport shapes as `type` plus
  function-based modules.
- Use `interface` for stable object-shaped ports or contracts with multiple
  implementations. Use `type` for DTOs, unions, mapped shapes, and local
  view-model shapes.
- Use `unknown` at trust boundaries when a value exists but its shape is not
  yet proven. Narrow or parse it immediately; do not let raw `unknown` drift
  into use cases, components, or domain models.
- Keep one concept under one owner. Consolidate duplicate same-role modules
  only when they represent the same concept in the same boundary.
- Build business behavior around domain models and domain language, but do not
  force UI state, DTOs, or infrastructure concerns into `domain`.
- Keep constants in the narrowest owning module or feature. Extract repeated
  stable literals into `constants.ts` only when they have one clear owner; do
  not build a global constants dump.
- Do not create a generic common bucket. Keep code in the narrowest owning
  layer, and duplicate small utilities until a stable abstraction is obvious.
- Do not create alternate top-level buckets such as `app/features/`,
  `app/modules/`, `app/hooks/`, `app/services/`, `app/utils/`, `app/types/`, or
  `app/store/` unless an explicit migration plan or companion skill requires
  them.
- If a file does not fit the canonical layout, revise the plan before
  implementation instead of inventing a convenience directory.
- Prefer direct replacement over compatibility aliases when renaming
  architecture terms.

## Implementation Workflow

### Placement Preflight For Every Change

- Read
  [`references/layout-and-module-placement.md`](references/layout-and-module-placement.md)
  first.
- Write the exact target paths for every created or moved file before touching
  code.
- Confirm that each target path matches one canonical owner:
  - route HTTP composition: `app/routes/`
  - feature-local presentational UI: `app/components/<feature>/`
  - shared presentational UI: `app/components/shared/`
  - client orchestration and state: `app/lib/client/usecase/<feature>/`
  - client adapters: `app/lib/client/infrastructure/`
  - server orchestration: `app/lib/server/usecase/`
  - server integrations, ORM/SDK clients, and persistence:
    `app/lib/server/infrastructure/`
  - domain concepts and ports: `app/lib/domain/`
- If any planned file lacks a clear owner, fix the placement plan before
  implementing.

### 0. Bootstrap new projects correctly

- When starting from scratch, follow
  [`references/project-bootstrap.md`](references/project-bootstrap.md) before
  writing features.
- Prefer `create-react-router` for this skill's architecture because it
  already aligns with Vite, route modules, and SPA mode.
- Add `@react-router/fs-routes` and SPA configuration before layering domain or
  use-case code. Use FlatRoute (`flatRoutes()`) as the routing convention from
  the first route file so URL shape, dynamic segments, and index modules stay
  consistent across the app.
- Add Fluent UI React v9 early for new UI work so components, theming, and
  accessibility patterns stay consistent from the first screen.
- Pick the data stack (ORM, query builder, or remote backend client) once,
  document it in the project, and keep its imports confined to
  `app/lib/server/infrastructure/`. This skill is intentionally silent on
  which one.
- Before substantial feature work begins, prefer installing the baseline
  dependencies the project already knows it will need so architecture work does
  not drift around missing packages mid-implementation.
- When hosting, identity, or deployment requirements appear, keep this skill on
  code-level architecture and hand the platform-specific decisions to the
  companion hosting skill.

### 1. Model the change around a use case

- Name the user intent first.
- Put invariants in `domain/entities` or `domain/value-objects`, and put
  cross-entity rules in `domain/policies` or `domain/services` when they do not
  naturally belong to one model.
- Put behavior where the state lives: entity behavior on entities, value
  normalization on value objects, cross-entity rules on policies, and
  orchestration on services or use cases.
- Keep literals local until they become stable, named concepts. Extract only
  when the name improves readability or the value is reused inside one clear
  boundary.
- Treat untrusted data as `unknown` first, then narrow it at the boundary with
  a parser, schema, or type guard before promoting it to a DTO or domain shape.
- When two modules appear to serve the same role, first ask whether they are
  actually the same concept in the same layer. Merge duplicates inside one
  boundary, but keep separate shapes when one is a DTO, one is a view model, or
  one is a domain model.
- Put repository interfaces in `domain/repositories`.
- Keep request or response shapes next to the route, API client, or use case
  that owns them. Promote them only after the boundary is stable and truly
  reused.
- Do not move transport `Request` or `Response` types into `domain` just
  because several modules share them.

### 2. Keep view logic out of components

- Build a custom Hook, controller, or view-model module in
  `app/lib/client/usecase/` for each non-trivial screen or interaction flow.
- When the interaction has enough complexity to need submodules, create a
  feature directory and keep the use case internals there.
- Keep feature-specific presentational components in
  `app/components/<feature>/` by default. Promote a component to
  `app/components/shared/` only after it proves to be truly feature-agnostic.
- Prefer composing views from Fluent UI primitives before introducing custom
  low-level controls.
- Keep on-screen copy terse. Put optional elaboration behind Tooltip,
  InfoLabel, Popover, or a similar secondary affordance only when the extra
  detail is not required for task completion.
- Keep purely presentational responsive adaptation in CSS, layout primitives,
  and presentational components. Move breakpoint-aware state into `usecase`
  only when it changes interaction flow or data loading behavior.
- For chart-heavy views, keep series transformation, grouping, filtering, and
  drill state in `app/lib/client/usecase/`, and keep chart components focused
  on rendering, theming, and accessibility wiring.
- Let that module own:
  - async calls
  - reducer logic
  - derived screen state
  - event handlers
  - error and pending mapping
- Pass view-ready props into `app/components/<feature>/` or
  `app/components/shared/` when the component is genuinely cross-feature.

### 3. Use React Router primitives before inventing new state containers

- Use loader data for route reads.
- Use action or fetcher state for mutations.
- Use navigation or fetcher pending state instead of duplicating network flags
  in component state.
- Add client state only for interaction state that is not already owned by the
  router.

### 4. Move DTOs through the client boundary explicitly

- Return JSON DTOs from route loaders, actions, or API endpoints.
- Let `client/infrastructure/api` fetch those DTOs.
- Rebuild client-facing objects or value objects inside the owning use case
  only when that adds real value.
- Do not expect server-side `Entity` instance identity to cross the network
  boundary.

### 5. Assemble dependencies at the edge

- Keep `usecase` and `domain` code constructor-injected.
- Build repository, gateway, and service instances in route loaders, actions,
  server entry points, or dedicated dependency factories.
- Prefer manual DI with explicit factory functions before introducing a DI
  container.
- Recreate request-scoped dependencies such as transaction-bound repositories
  from the current request or transaction context.

### 6. Validate and map errors by layer

- Keep request parsing and input shape validation at the HTTP or client
  transport boundary.
- Keep use-case rule validation in `server/usecase` or `client/usecase`.
- Keep invariant enforcement in `domain`.
- Map domain, application, and infrastructure errors to transport responses at
  the edge.

### 7. Keep authorization and serialization explicit

- Resolve authentication at the edge, but keep authorization decisions in use
  cases or domain policies.
- Convert `Date`, ids, money-like values, and value objects at boundaries
  rather than leaking transport or ORM shapes inward.

### 8. Keep mutable state scoped

- Do not store request-specific state in module globals or singleton services.
- Pass auth context, user context, tenant context, and correlation metadata
  explicitly.
- Keep transaction handles scoped to the current transaction only.
- Treat client-side async race conditions as correctness issues and guard
  against stale responses.

### 9. Use explicit compromises for stateful flows

- For chat, streaming, session, playback, or wizard-style flows, allow a
  feature-local controller or store when lifecycle and identity genuinely
  matter.
- Keep that compromise inside `app/lib/client/usecase/<feature>/` rather than
  spreading mutable state across routes or components.
- Split durable state, ephemeral runtime state, and infrastructure handles
  explicitly.

### 10. Push side effects and migrations into explicit workflows

- Keep schema changes, background jobs, external notifications, and indexing
  side effects visible and testable.
- Do not hide them inside random route handlers or repositories.
- Use barrel exports sparingly and only when they do not obscure ownership or
  create cycles.

### 11. Verify before push

- Run targeted tests for the touched area.
- Run typecheck and lint or the project quality gate.
- Audit for boundary drift and forbidden imports.
- For UI-affecting changes, run the touched route in Playwright and confirm the
  rendered result, interaction states, and responsive layout.
- Fix architecture violations before pushing even if tests pass.

### 12. Refactor overloaded files in phases

- When a `ts` or `tsx` file has too many responsibilities, do not rewrite it in
  one jump.
- Follow the hotspot workflow in
  [`references/hotspot-refactor-workflow.md`](references/hotspot-refactor-workflow.md).
- When several hotspots exist, start with the one that combines correctness
  risk, change frequency, and boundary damage rather than the one that is
  merely largest.
- Separate analysis, planning, extraction sequencing, execution, and
  verification.
- Move one stable seam at a time and keep the file working after each batch.

## Placement Guide

- Need feature-local pure rendering and markup: `app/components/<feature>/`
- Need reusable pure UI primitives or shared Fluent UI composition wrappers:
  `app/components/shared/`
- Need route composition or loader/action bridging: `app/routes/`
- Need client-side state, handlers, reducers, selectors, or orchestration:
  `app/lib/client/usecase/<feature>/`
- Need browser APIs, API clients, or router adapters:
  `app/lib/client/infrastructure/`
- Need endpoint-specific API clients: `app/lib/client/infrastructure/api/`
- Need browser-only adapters such as storage, clipboard, media queries, or
  channel APIs: `app/lib/client/infrastructure/browser/`
- Need a business invariant or behavior-rich model: `app/lib/domain/entities/`
- Need a small immutable business concept with validation:
  `app/lib/domain/value-objects/`
- Need cross-entity business rules: prefer `app/lib/domain/policies/`
- Need domain-level orchestration that is still infrastructure-free:
  `app/lib/domain/services/`
- Need a repository port or domain-facing persistence contract:
  `app/lib/domain/repositories/`
- Need server orchestration: `app/lib/server/usecase/`
- Need ORM clients, SDK clients, file system access, or external service code:
  `app/lib/server/infrastructure/`
- Need platform-specific server config bootstrap or config readers:
  `app/lib/server/infrastructure/config/`
- Need persistence implementations: `app/lib/server/infrastructure/repositories/`
- Need external SDK or HTTP adapters: `app/lib/server/infrastructure/gateways/`
- Need a reusable type or utility: place it with the owning route, use case, or
  domain module first; extract only after repeated reuse proves the boundary

## References

- project bootstrap and baseline dependency install:
  [`references/project-bootstrap.md`](references/project-bootstrap.md)
- overview index for placement and dependency rules:
  [`references/layout-and-dependency-rules.md`](references/layout-and-dependency-rules.md)
- layout and module placement, always read first:
  [`references/layout-and-module-placement.md`](references/layout-and-module-placement.md)
- client layer responsibilities:
  [`references/client-layer-responsibilities.md`](references/client-layer-responsibilities.md)
- server and domain layer responsibilities:
  [`references/server-and-domain-layer-responsibilities.md`](references/server-and-domain-layer-responsibilities.md)
- boundary and contract rules:
  [`references/boundary-and-contract-rules.md`](references/boundary-and-contract-rules.md)
- domain modeling and type rules:
  [`references/domain-modeling-and-type-rules.md`](references/domain-modeling-and-type-rules.md)
- dependency injection, lifetime, and side-effect rules:
  [`references/dependency-injection-lifetime-and-side-effects.md`](references/dependency-injection-lifetime-and-side-effects.md)
- FlatRoute REST API guidance:
  [`references/flat-route-rest-api-guidelines.md`](references/flat-route-rest-api-guidelines.md)
- view-state and handler composition:
  [`references/view-state-and-handler-patterns.md`](references/view-state-and-handler-patterns.md)
- chart and data visualization guidance:
  [`references/chart-and-data-visualization-guidance.md`](references/chart-and-data-visualization-guidance.md)
- responsive and mobile UI guidance:
  [`references/responsive-and-mobile-ui-guidance.md`](references/responsive-and-mobile-ui-guidance.md)
- Playwright UI verification workflow:
  [`references/playwright-ui-verification.md`](references/playwright-ui-verification.md)
- stateful flow compromise rules:
  [`references/stateful-flow-compromises.md`](references/stateful-flow-compromises.md)
- hotspot refactor workflow:
  [`references/hotspot-refactor-workflow.md`](references/hotspot-refactor-workflow.md)
- verification before push:
  [`references/verification-gates.md`](references/verification-gates.md)
