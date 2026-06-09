# Project Bootstrap

Use this guide when starting a new project from scratch.

## Baseline Assumptions

Assume this stack unless the user explicitly says otherwise:

- TypeScript
- React Router framework mode
- SPA mode with `ssr: false`
- Fluent UI React v9 for the UI layer unless the repository already has an
  established design-system standard
- Node.js 20.19+ or 22.x

This skill does not pick the data stack. Whichever ORM, query builder, SQL
client, or remote backend you choose, confine its imports to
`app/lib/server/infrastructure/` and keep the rest of the layer rules
unchanged.

If a companion hosting skill explicitly requires a server runtime or a
secretless platform-config bootstrap, let that companion override the `ssr:
false` and local config-loading defaults in this file while keeping the rest
of the layer rules.

## Preferred Bootstrap Path

For this skill's architecture, prefer React Router's official Vite-powered
bootstrap over retrofitting a plain Vite template.

Reason:

- it matches `app/` and route-module conventions
- it keeps React Router configuration aligned with current framework mode
- it reduces manual setup drift before architecture work even starts

In practice, it is usually cleaner to install the baseline routing and UI
dependencies the project already knows it will need before feature
implementation starts, rather than adding them piecemeal in the middle of the
first feature. Install the chosen data stack at the same time using whatever
its own install instructions require.

## Baseline Dependencies To Have Ready

For the default stack in this skill, the baseline dependency set to line up
early is:

- Route-file support: `@react-router/fs-routes`
- Default UI layer when no existing design system overrides it:
  `@fluentui/react-components`, `@fluentui/react-icons`

In practice, that usually means these commands soon after scaffold:

```bash
npm install @react-router/fs-routes @fluentui/react-components @fluentui/react-icons
```

If the repository already has an established design system, do not add Fluent
UI casually just because it appears in the default list.

Install the data stack of your choice (ORM, query builder, SDK, or HTTP
backend client) separately, following that stack's own documentation. Keep
every import of that stack inside `app/lib/server/infrastructure/`.

## Recommended Flow

### 1. Scaffold the app

```bash
npx create-react-router@latest my-app
cd my-app
```

Use the TypeScript option when prompted.

Keep the standard mobile viewport tag in the document head:

```html
<meta name="viewport" content="width=device-width, initial-scale=1" />
```

Add `viewport-fit=cover` only when the layout intentionally handles safe-area
insets.

### 2. Switch the app to SPA mode by default

Create or update `react-router.config.ts`:

```ts
import type { Config } from "@react-router/dev/config";

export default {
  ssr: false,
} satisfies Config;
```

This keeps React Router in SPA mode while still using framework conventions.
If a companion hosting skill explicitly requires server runtime features such
as OAuth callbacks, server-owned secrets, or platform-managed config
bootstrap, do not force `ssr: false`.

### 3. Enable FlatRoute file routing

Install the file-route helper:

```bash
npm install @react-router/fs-routes
```

Create or update `app/routes.ts`:

```ts
import { flatRoutes } from "@react-router/fs-routes";
import type { RouteConfig } from "@react-router/dev/routes";

export default flatRoutes() satisfies RouteConfig;
```

This is the cleanest match for the route-file conventions used by this skill.

### 4. Install Fluent UI React v9

For the default UI baseline:

```bash
npm install @fluentui/react-components @fluentui/react-icons
```

Wrap the app root with `FluentProvider` and the appropriate theme, typically
`webLightTheme`, before building feature screens:

```tsx
import {
  FluentProvider,
  webLightTheme,
} from "@fluentui/react-components";

export function AppShell({ children }: { children: React.ReactNode }) {
  return <FluentProvider theme={webLightTheme}>{children}</FluentProvider>;
}
```

If the repository already has a clearly established design system, follow that
system instead of mixing component libraries casually.

### 5. Pick and install the data stack

This skill is intentionally silent on which ORM, query builder, or database
engine to use. Decide once at bootstrap time, document the choice in the
project, and install it following its own documentation.

When integrating it, keep these rules:

- every import of the chosen ORM, query builder, database driver, or remote
  backend SDK must live under `app/lib/server/infrastructure/`
- repository interfaces stay in `app/lib/domain/repositories/`
- repository implementations stay in
  `app/lib/server/infrastructure/repositories/`
- a long-lived client (connection pool, ORM client, SDK client) is
  constructed once in a server-only bootstrap module under
  `app/lib/server/infrastructure/` and shared by injection, not by import
  side effects
- environment-driven configuration (database URL, API base URL, secrets) is
  read inside `app/lib/server/infrastructure/config/`, not inside route or
  use-case modules

If the chosen stack ships its own scaffold, migration, or codegen tooling,
keep those commands documented in the project (`README.md`, `package.json`
scripts), but do not let their generated client leak out of
`app/lib/server/infrastructure/`.

### 6. Create the first architecture-aligned directories

Do not create every possible directory up front.

Create only the ones needed for the first feature or integration, usually:

```text
app/routes/
app/components/
app/lib/client/usecase/
app/lib/server/usecase/
app/lib/server/infrastructure/
app/lib/domain/
```

Add deeper directories such as `entities/`, `value-objects/`, `repositories/`,
or `gateways/` when the first real owner appears.

## Plain Vite Fallback

Use plain Vite bootstrap only when one of these is true:

- the user explicitly requires `create-vite`
- the project intentionally uses React Router data mode or declarative mode
  instead of framework mode
- an existing starter must be retrofitted

Fallback start:

```bash
npm create vite@latest my-app -- --template react-ts
cd my-app
```

Then:

1. install the React Router packages required by the chosen mode
2. if you need this skill's framework conventions such as `app/routes/`,
   route modules, and FlatRoute naming, prefer re-bootstrapping with
   `create-react-router` instead of manually recreating the framework stack
3. install and wire the chosen data stack exactly as above

Do not treat plain `create-vite` as the default when the target architecture
is React Router framework mode.

## Bootstrap Verification

Before starting feature work, confirm all of the following:

- `npm run dev` starts successfully
- SPA mode is enabled
- the standard mobile viewport meta tag is present
- FlatRoute routing is wired through `app/routes.ts`
- Fluent UI React v9 is installed and the app root is wrapped with
  `FluentProvider`, unless an existing design system overrides it
- the chosen data stack is installed
- no import of the chosen ORM, query builder, or SDK exists outside
  `app/lib/server/infrastructure/`
- route modules and domain folders are present, even if still minimal
