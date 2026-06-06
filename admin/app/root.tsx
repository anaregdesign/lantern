import {
  FluentProvider,
  webDarkTheme,
  webLightTheme,
} from "@fluentui/react-components";
import {
  isRouteErrorResponse,
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
} from "react-router";

import type { Route } from "./+types/root";
import { AppShell } from "~/components/shared/AppShell/AppShell";
import { ConnectionProvider } from "~/lib/client/usecase/connection/connection-context";
import { usePreferredTheme } from "~/lib/client/usecase/theme/use-preferred-theme";
import errorStyles from "./styles/error-boundary.module.css";
import "./styles/global.css";

export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
      </head>
      <body>
        {children}
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function App() {
  const theme = usePreferredTheme();
  return (
    <FluentProvider theme={theme === "dark" ? webDarkTheme : webLightTheme}>
      <ConnectionProvider>
        <AppShell>
          <Outlet />
        </AppShell>
      </ConnectionProvider>
    </FluentProvider>
  );
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  let title = "Something went wrong";
  let detail = "An unexpected error occurred.";
  let stack: string | undefined;

  if (isRouteErrorResponse(error)) {
    title = error.status === 404 ? "404 — Not found" : `Error ${error.status}`;
    detail =
      error.status === 404
        ? "The requested page could not be found."
        : (error.statusText ?? detail);
  } else if (import.meta.env.DEV && error instanceof Error) {
    detail = error.message;
    stack = error.stack;
  }

  return (
    <FluentProvider theme={webLightTheme}>
      <main className={errorStyles.errorMain}>
        <h1>{title}</h1>
        <p>{detail}</p>
        {stack ? (
          <pre className={errorStyles.errorStack}>
            <code>{stack}</code>
          </pre>
        ) : null}
      </main>
    </FluentProvider>
  );
}
