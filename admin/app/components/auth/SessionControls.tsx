import { Button, Text } from "@fluentui/react-components";
import { useAuth } from "~/lib/client/usecase/auth/use-auth";
import styles from "./SessionControls.module.css";
export function SessionControls() {
  const { state, controller, logout } = useAuth();
  if (state.kind === "off")
    return <Text className={styles.label}>Authentication off</Text>;
  if (state.kind !== "ready") return null;
  return (
    <div className={styles.controls}>
      <Text className={styles.label}>{state.principal.identity?.subject}</Text>
      {!state.principal.recentAuthentication && (
        <Button
          onClick={() =>
            controller.login(state.principal.identity!.issuer, true)
          }
        >
          Verify identity
        </Button>
      )}
      <Button
        onClick={() => {
          void logout();
        }}
      >
        Sign out
      </Button>
    </div>
  );
}
