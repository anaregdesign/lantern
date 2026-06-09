# Layout And Dependency Rules Overview

This topic outgrew a single reference. Use the split references below instead
of reading one oversized file.

- [`layout-and-module-placement.md`](layout-and-module-placement.md): canonical
  directory layout, feature public API boundaries, naming, and typical flow
- [`client-layer-responsibilities.md`](client-layer-responsibilities.md): what
  belongs in routes, components, shared UI, and client infrastructure
- [`server-and-domain-layer-responsibilities.md`](server-and-domain-layer-responsibilities.md):
  what belongs in domain modules and server layers
- [`boundary-and-contract-rules.md`](boundary-and-contract-rules.md): dependency
  direction, contract placement, validation, authorization, serialization, and
  import smells
- [`domain-modeling-and-type-rules.md`](domain-modeling-and-type-rules.md):
  concept ownership, domain-centered modeling, `class` vs `type`, `interface`,
  and `unknown`
- [`dependency-injection-lifetime-and-side-effects.md`](dependency-injection-lifetime-and-side-effects.md):
  DI, runtime safety, side effects, lifetime, and migration workflow

Always read `layout-and-module-placement.md` first, then read the narrowest
additional matching reference for the task instead of loading the whole
architecture catalog.
