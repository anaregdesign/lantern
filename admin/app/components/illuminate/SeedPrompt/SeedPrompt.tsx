import { useState, type FormEvent } from "react";
import {
  Button,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
} from "@fluentui/react-components";
import { LightbulbFilament20Regular } from "@fluentui/react-icons";
import styles from "./SeedPrompt.module.css";

export interface SeedPromptProps {
  onOpen: (seed: string) => void;
}

/**
 * Shown when the Illuminate route has no `?seed=`. Lets the user kick off
 * an exploration from any key without first visiting the Browse screen.
 */
export function SeedPrompt({ onOpen }: SeedPromptProps) {
  const [seed, setSeed] = useState("");

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const trimmed = seed.trim();
    if (trimmed === "") return;
    onOpen(trimmed);
  };

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
      <Field label="Seed key" className={styles.field}>
        <Input
          value={seed}
          onChange={(_, data) => setSeed(data.value)}
          placeholder="e.g. user:42"
          data-testid="illuminate-seed-input"
          autoFocus
        />
      </Field>
      <Button
        appearance="primary"
        icon={<LightbulbFilament20Regular />}
        type="submit"
        disabled={seed.trim() === ""}
        data-testid="illuminate-open"
      >
        Open
      </Button>
    </form>
  );
}
