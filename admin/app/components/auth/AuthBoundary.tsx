import {
  Button,
  MessageBar,
  MessageBarBody,
  Spinner,
  Title2,
} from "@fluentui/react-components";
import type { ReactNode } from "react";
import { useAuth } from "~/lib/client/usecase/auth/use-auth";
import styles from "./AuthBoundary.module.css";

export function AuthBoundary({ children }: { children: ReactNode }) {
  const { state, controller } = useAuth();
  if (state.kind === "off" || state.kind === "ready")
    return <div key={`${state.kind}:${state.epoch}`}>{children}</div>;
  return (
    <section className={styles.panel} aria-label="Authentication">
      {controller.getLogoutMessage() && (
        <MessageBar>
          <MessageBarBody>{controller.getLogoutMessage()}</MessageBarBody>
        </MessageBar>
      )}
      {state.kind === "checking" ? (
        <Spinner label="Checking authentication" />
      ) : (
        <>
          <Title2>
            {state.kind === "login"
              ? "Sign in to Lantern"
              : "Authentication unavailable"}
          </Title2>
          {state.kind === "error" ? (
            <>
              <MessageBar intent="error">
                <MessageBarBody>{state.message}</MessageBarBody>
              </MessageBar>
              <Button
                onClick={() => {
                  void controller.refresh(true);
                }}
              >
                Retry
              </Button>
            </>
          ) : (
            state.issuers.map((issuer) => (
              <Button
                key={issuer.issuer}
                appearance="primary"
                onClick={() => controller.login(issuer.issuer)}
              >
                Sign in with {issuer.label || issuer.issuer}
              </Button>
            ))
          )}
        </>
      )}
    </section>
  );
}
