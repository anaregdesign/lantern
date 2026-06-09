# Hotspot Refactor Workflow

Use this workflow when a `ts` or `tsx` file has accumulated too many
responsibilities and must be split without breaking behavior.

## Goal

Reduce one hotspot file into explicit modules with clearer boundaries, smaller
review batches, and preserved behavior.

Do not treat this as a rewrite. Treat it as controlled extraction.

## Priority Rule

When several refactor candidates exist, do not start with the largest file by
default.

Prioritize by this order:

1. correctness risk
2. change frequency
3. architecture damage
4. extraction leverage
5. file size

Interpret the order like this:

- correctness risk: the file is likely to cause bugs, regressions, race
  conditions, authorization mistakes, or persistence mistakes
- change frequency: the file is touched often and slows normal feature
  delivery
- architecture damage: the file currently breaks boundaries such as
  route-to-infrastructure coupling, server-to-client leakage, or
  DTO-to-domain leakage
- extraction leverage: refactoring this file will make several nearby files
  easier to clean up afterward
- file size: use size as a signal, but not as the deciding factor by itself

Good first targets usually have both high risk and high churn.

Examples of high-priority hotspots:

- a route file that mixes HTTP handling, data-client queries, and business
  rules
- a component that owns async orchestration, retries, and reducer-worthy state
- a use case module with module-level mutable state or stale async race bugs
- a repository or gateway that leaks persistence or SDK errors across
  boundaries

Deprioritize hotspots when they are:

- large but stable
- mostly ugly rather than risky
- isolated from active feature work
- likely to force a broad rename or move with little behavioral payoff

Within one hotspot, prioritize seams in this order:

1. hidden side effects and mutable state
2. boundary violations
3. duplicated validation or error mapping
4. reducer, selector, and handler extraction
5. naming cleanup and cosmetic reshaping

Stabilize behavior first. Improve elegance second.

## Phase 1: Analyze

Read the hotspot file and classify each responsibility before moving code.

Identify:

- UI rendering
- route or HTTP wiring
- state transitions
- event handlers
- async orchestration
- DTO parsing or mapping
- domain rules
- infrastructure access
- cross-cutting utilities

Mark what is:

- pure and easy to extract
- boundary-sensitive and should stay near the entry point
- currently coupled because of naming or hidden shared state
- risky because it changes behavior, ordering, or async lifecycle

Useful checks:

- count imports and note which layer each import belongs to
- find mutable module state
- find `useEffect`, `fetch`, `new`, data-client calls, and large inline
  condition blocks
- find repeated parameter groups and repeated derived expressions

Do not edit yet. First understand why the file grew.

## Phase 2: Plan

Write a refactor plan around seams, not around lines.

Prefer this extraction order:

1. pure constants and small utilities
2. local types and DTO mappers
3. selectors and derived read models
4. reducer and state definitions
5. event handlers and async command helpers
6. API adapters or infrastructure adapters
7. domain rules or policies
8. entry-point cleanup

Choose target files based on responsibility:

- render-only code -> `components`
- reducer, selectors, handlers -> `client/usecase/<feature>/`
- API calls or browser integrations -> `client/infrastructure/*`
- server orchestration -> `server/usecase`
- persistence or SDK logic -> `server/infrastructure/*`
- business invariants -> `domain/*`

Define the smallest safe batch. A good batch:

- removes one responsibility
- keeps the old file compiling
- is reviewable without reading the whole system

Do not combine naming migrations, behavior changes, and large structural
movement in the same batch unless they are inseparable.

Also plan:

- which validation rules stay at the boundary and which move inward
- which error categories need explicit mapping after extraction
- what the public entry point of the feature will be after the split
- how to avoid introducing circular imports

## Phase 3: Consider Risks

Before moving code, check the failure modes.

Common risks:

- hidden module-level mutable state
- stale closures after extraction
- accidental dependency inversion
- transport DTOs leaking into `domain`
- request context leaking into singletons
- circular imports caused by rushed extraction
- tests asserting file-local implementation details
- over-extraction into vague common modules

If the file is both a hotspot and a behavior hotspot, add or update
characterization tests first.

Good characterization targets:

- reducer transitions
- selector outputs
- request parsing
- response mapping
- route status code behavior
- pending and error states
- validation failures and their mapped transport responses

## Phase 4: Execute In Small Batches

Extract one seam at a time.

Recommended batch sequence:

1. Move pure helpers and constants
2. Move types and DTO mapping helpers
3. Move selectors
4. Move reducer and state
5. Move async handlers
6. Move adapters
7. Shrink the original file to composition only

After each batch:

- fix imports
- rerun targeted tests
- rerun typecheck if the boundary changed
- confirm no new circular dependency appeared
- confirm the extracted module did not accidentally become another feature's
  private dependency

When a batch is too big, split it further. Favor many small extractions over
one heroic refactor.

## Phase 5: Verify

After the refactor, verify both behavior and architecture.

Behavior checks:

- existing tests still pass
- changed flows still work manually
- no stale async result overwrites newer state
- no status code or response-shape regression

Architecture checks:

- the hotspot file now has one clear role
- extracted modules live in the correct layer
- no new `server -> client` or boundary-breaking imports exist
- no generic catch-all module was introduced
- the remaining entry file mostly wires modules together
- validation and error mapping still live at intentional boundaries

## Phase 6: Report

Summarize the refactor in terms of responsibilities moved, not just files
changed.

State:

- what responsibilities were extracted
- what stayed and why
- what risks were reduced
- what follow-up seams still remain

If the file is still too large, leave a concrete next seam instead of
pretending the refactor is complete.

## Heuristics

Use these heuristics while deciding whether to extract.

Extract when:

- a block can be named clearly
- a block belongs to another layer
- a block can be tested in isolation
- a block is reused or will clearly stabilize
- a block hides state transitions or mapping logic

Do not extract when:

- the only outcome is more files with no boundary improvement
- the new module name would be vague, such as `helpers` or `utils`
- the code is tightly coupled and should first be simplified in place

## Target End State

A successful hotspot refactor usually ends like this:

- entry file: composition and top-level flow only
- use case files: state, reducer, selectors, handlers
- infrastructure files: transport or persistence details
- domain files: invariants and business rules
- presentational files: rendering only
