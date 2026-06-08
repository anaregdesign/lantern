import { useCallback, useState, type FormEvent } from "react";
import { Button, MessageBar, MessageBarBody } from "@fluentui/react-components";
import { LightbulbFilament20Regular } from "@fluentui/react-icons";
import { VertexPicker } from "~/components/shared/VertexPicker/VertexPicker";
import styles from "./SeedPrompt.module.css";

export interface SeedPromptProps {
  onOpen: (seed: string) => void;
}

/**
 * Shown when the Illuminate route has no `?seed=`. Lets the user kick off
 * an exploration from any key without first visiting the Browse screen.
 *
 * Per #457 the blind text input is now a type-ahead {@link VertexPicker}:
 * keys are suggested live from ScanVertices and the total match count is
 * surfaced beneath the field. Committing a suggestion (click / ↵ / Tab) or
 * the explicit Open button both route through
 * {@link SeedPromptProps.onOpen}.
 */
export function SeedPrompt({ onOpen }: SeedPromptProps) {
  const [seed, setSeed] = useState("");
  const trimmed = seed.trim();

  const open = useCallback(
    (key: string) => {
      const next = key.trim();
      if (next === "") return;
      onOpen(next);
    },
    [onOpen],
  );

  const submit = useCallback(
    (event: FormEvent) => {
      event.preventDefault();
      open(seed);
    },
    [open, seed],
  );

  return (
    <form
      className={styles.form}
      onSubmit={submit}
      data-testid="illuminate-seed-prompt"
    >
      <MessageBar intent="info">
        <MessageBarBody>
          Enter a vertex key to illuminate its neighbourhood, or click
          “Illuminate” from any row on the Browse screen.
        </MessageBarBody>
      </MessageBar>
      <VertexPicker
        value={seed}
        onValueChange={setSeed}
        onSelect={open}
        label="Seed key"
        placeholder="e.g. user:42"
        autoFocus
        inputTestId="illuminate-seed-input"
        captionTestId="illuminate-seed-matches"
      />
      <Button
        appearance="primary"
        icon={<LightbulbFilament20Regular />}
        type="submit"
        disabled={trimmed === ""}
        data-testid="illuminate-open"
      >
        Open
      </Button>
    </form>
  );
}
