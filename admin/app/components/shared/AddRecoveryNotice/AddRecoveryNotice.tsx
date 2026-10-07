import { Button, MessageBar, MessageBarBody } from "@fluentui/react-components";
import type { AddAttempt } from "~/lib/client/usecase/add-recovery/add-recovery";

export function AddRecoveryNotice({
  attempts,
  check,
}: {
  attempts: readonly AddAttempt[];
  check(id: string): Promise<void>;
}) {
  return attempts.map((attempt) => (
    <MessageBar
      key={attempt.id}
      intent={attempt.phase === "confirmed" ? "success" : "warning"}
      layout="multiline"
      data-testid="add-recovery-notice"
    >
      <MessageBarBody>
        {attempt.message}
        {attempt.phase !== "confirmed" ? (
          <Button
            disabled={attempt.phase === "sending"}
            onClick={() => {
              void check(attempt.id);
            }}
          >
            Check original Add
          </Button>
        ) : null}
      </MessageBarBody>
    </MessageBar>
  ));
}
