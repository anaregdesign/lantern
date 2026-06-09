# Verification Gates

## Use This Before Push

Run verification in this order:

1. Targeted tests for the changed behavior
2. Typecheck
3. Lint or project quality gate
4. Architecture drift checks
5. Playwright check of the touched route when UI is affected, including mobile
   viewport verification when layout or interaction is responsive
6. Manual spot check of the touched flow
7. Commit history sanity check when preparing shared commits

Do not push code that passes tests but breaks layer direction.
Do not push UI-affecting changes that were never checked in a real rendered
browser flow.

## Architecture Drift Checklist

Confirm all of the following:

- no ORM, query builder, or data-client imports outside
  `app/lib/server/infrastructure/`
- no server imports inside `app/components/` or `app/lib/client/*`
- no React or browser imports inside `app/lib/domain/*`
- no generic catch-all common directory reintroduced
- no convenience top-level buckets such as `app/features/`, `app/modules/`,
  `app/hooks/`, `app/services/`, `app/utils/`, `app/types/`, or `app/store/`
  introduced without an explicit migration plan
- no horizontal `state`, `reducers`, `stores`, or `handlers` directories
  introduced under `app/`
- no feature-specific presentational component placed outside
  `app/components/<feature>/` without a deliberate reason
- no feature-specific logic imported into `app/components/shared/`
- no component moved into `shared` only for convenience while still carrying
  feature vocabulary
- no client adapter pretending to return server-side live instances across the
  network
- no transport `Request` or `Response` DTOs promoted into `domain` without
  domain meaning
- no `usecase` or `domain` module instantiating repositories or gateways
  directly
- no module-level mutable server state carrying request or user context
- no circular imports introduced across feature internals or layers
- no authorization rule enforced only in the UI
- no boundary object such as `Date`, ORM record, or raw persistence shape
  leaking where a mapped shape should exist
- no business logic buried in route modules
- no non-trivial async orchestration left inside presentational components
- no new vague file names such as `helpers.ts`, `utils.ts`, or `common.ts`
- no DTO or response-envelope classes introduced where `type` plus functions
  would be clearer
- no entity reduced to public mutable fields plus generic setters
- no `interface` used for one-off DTOs or `I*`-prefixed port names without a
  strong reason
- no parallel same-role modules left in one boundary without a clear
  ownership distinction
- no forced merge that collapses DTOs, view models, persistence records, and
  domain models into one shape
- no catch-all `constants` dump created without a clear ownership boundary
- no business rule encoded only as scattered magic numbers or strings
- no raw `unknown` value flowing past its trust boundary without narrowing or
  parsing
- no `.tsx` file under `app/components/` exporting more than one top-level
  React component, and no file whose primary exported component name disagrees
  with the file name
- no component-owned CSS imported as a non-module stylesheet inside a
  component module; per-component styling lives in a sibling
  `<ComponentName>.module.css` (or `makeStyles`)
- no feature-specific selectors or component-name selectors added to global
  stylesheets under `app/styles/`
- no inline `style={{ ... }}` used for static styling that belongs in a CSS
  Module or `makeStyles`
- no second general-purpose component library introduced alongside Fluent UI
  without a documented deviation

## Useful Search Patterns

Use `rg` for quick audits. Adjust the data-client name (`prisma`, `drizzle`,
`kysely`, `pg`, etc.) to whatever the project actually picked:

```bash
rg -n "lib/server" app/routes app/components app/lib/client
rg -n "from ['\"][^'\"]*react" app/lib/domain
find app/lib -maxdepth 2 -type d | sort
find app -type d \( -name features -o -name modules -o -name hooks -o -name services -o -name utils -o -name types -o -name store \) | sort
find app -type d \( -name state -o -name states -o -name reducer -o -name reducers -o -name store -o -name stores -o -name handler -o -name handlers \) | sort
rg -n "lib/client/usecase|lib/server|routes/" app/components/shared
rg -n "Thread|Chat|Billing|Project|Profile|Order|Invoice|Session" app/components/shared
rg -n "new [A-Z][A-Za-z0-9_]+\(|instanceof " app/lib/client
rg -n "Request|Response|Payload|Dto|DTO" app/lib/domain
rg -n "new .*Repository|new .*Gateway" app/lib/domain app/lib/server/usecase
rg -n "let current|let active|let request|let user|let tenant|globalThis\.|module\.exports\." app/lib/server
madge --circular app 2>/dev/null || true
rg -n "index\.ts$" app
rg --files app | rg "/(helpers|utils|common|misc|temp|new)\.(ts|tsx)$"
rg -n "useEffect\(" app/components

# One component per file: scan exports under app/components and confirm each
# file exposes exactly one top-level React component whose name matches the
# file. Investigate any file with multiple `export` lines at the top level.
rg -n "^export (default |const |function )" app/components

# CSS Modules colocated with components: every styled component should have a
# sibling .module.css. Investigate any orphan stylesheet or component that
# imports styles through a different path.
find app/components -name "*.tsx" -print
find app/components -name "*.module.css" -print

# No global CSS imports inside components.
rg -n "import .*\.css['\"]" app/components | rg -v "\.module\.css"

# Inline style usage. Inspect each match and confirm it is a genuinely dynamic
# runtime value, not static styling that belongs in a CSS Module.
rg -n "style=\{\{" app/components
```

When the project's data stack is an ORM or SDK with a clear import root, add
one focused audit for it, for example:

```bash
rg -n "@prisma/client" app   # adapt the package name to the chosen stack
```

Interpret results, do not blindly fail on matches. The point is to surface
suspicious files quickly.

## Push Gate Heuristic

Before `git push`, be able to state all of the following:

- the use case layer owns the interaction logic
- the view layer is mostly props plus rendering
- feature-local presentational components live under `app/components/<feature>/`
- `components/shared` stays presentational and feature-agnostic
- components started life under `app/components/<feature>/` unless they proved
  to be generic
- reducer and state modules live with their owning feature use case
- client data access returns DTOs unless a real local-first repository
  abstraction is justified
- transport contracts still live near their boundary unless they have become
  true domain concepts
- dependencies are wired at the edge rather than instantiated inside use cases
- mutable request context does not leak through singleton or module-level
  state
- validation rules live at the correct layer rather than collapsing into one
  layer
- errors are mapped intentionally rather than leaking raw infrastructure
  failures
- authorization has a server-side home and is not only a UI concern
- serialization boundaries remain explicit
- server persistence goes through server infrastructure
- file names reveal module responsibility without fallback names like
  `helpers` or `utils`
- classes are used for identity and invariants, not as generic containers or
  static utility bags
- `interface` is used for ports and stable object contracts, while `type` owns
  DTOs and unions
- the app is domain-centered for business behavior without pushing UI, HTTP,
  or persistence details into `domain`
- constants live with their owner instead of drifting into a global junk
  drawer
- `unknown` is used as a boundary quarantine type rather than a long-lived
  internal type
- reusable helpers still live in a specific owning layer unless the
  abstraction is clearly stable
- the changed area has tests or a clear reason why tests were not added
- UI-affecting changes were checked in Playwright at the relevant route,
  viewport sizes, and orientation when needed

If any statement is false, fix the architecture before pushing.
