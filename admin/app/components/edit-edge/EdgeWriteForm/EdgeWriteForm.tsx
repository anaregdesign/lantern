import {
  Button,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
  Spinner,
} from "@fluentui/react-components";
import { Save20Regular } from "@fluentui/react-icons";
import { TtlField } from "../../edit-vertex/TtlField/TtlField";
import type { EdgeWriteInputs } from "~/lib/client/usecase/edit-edge/edge-codec";
import type { TtlInput } from "~/lib/client/usecase/edit-vertex/value-codec";
import styles from "../EdgeDetailPage/EdgeDetailPage.module.css";

export interface EdgeWriteFormProps {
  /** "add" or "put" — only used for test ids. */
  mode: "add" | "put";
  title: string;
  description: string;
  inputs: EdgeWriteInputs;
  status: "idle" | "saving" | "saved" | "error";
  error: string | null;
  valid: boolean;
  onWeight: (value: string) => void;
  onTtl: (ttl: TtlInput) => void;
  onSubmit: () => void;
  submitLabel: string;
}

/**
 * Generic weight+TTL form used twice on the edge detail page: once for
 * `AddEdge` and once for `PutEdge`. Keeping the shape shared makes the
 * subtle semantic distinction the *only* difference users see.
 */
export function EdgeWriteForm(props: EdgeWriteFormProps) {
  const handleSubmit = (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    props.onSubmit();
  };
  return (
    <form
      onSubmit={handleSubmit}
      className={styles.card}
      data-testid={`edge-form-${props.mode}`}
    >
      <h2 className={styles.cardTitle}>{props.title}</h2>
      <p className={styles.cardLead}>{props.description}</p>
      <Field
        label="Weight"
        hint="Floating-point. Negative values are allowed for PutEdge."
      >
        <Input
          type="number"
          step="any"
          value={props.inputs.weight}
          onChange={(_, data) => props.onWeight(data.value)}
          data-testid={`edge-${props.mode}-weight`}
        />
      </Field>
      <TtlField
        value={props.inputs.ttl}
        onChange={props.onTtl}
        label="Expires in"
      />
      {props.error ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>{props.error}</MessageBarBody>
        </MessageBar>
      ) : null}
      <div className={styles.formActions}>
        <Button
          appearance="primary"
          type="submit"
          icon={
            props.status === "saving" ? (
              <Spinner size="tiny" />
            ) : (
              <Save20Regular />
            )
          }
          disabled={!props.valid || props.status === "saving"}
          data-testid={`edge-${props.mode}-submit`}
        >
          {props.submitLabel}
        </Button>
      </div>
    </form>
  );
}
