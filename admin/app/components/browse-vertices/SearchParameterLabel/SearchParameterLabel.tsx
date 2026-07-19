import { Button, Label, Tooltip } from "@fluentui/react-components";
import { Info16Regular } from "@fluentui/react-icons";

import styles from "./SearchParameterLabel.module.css";

export interface SearchParameterLabelProps {
  controlId: string;
  label: string;
  description: string;
  testId: string;
}

/**
 * Visible form label plus a keyboard- and pointer-discoverable explanation.
 * The label stays associated with its control; the Tooltip contains only
 * supplemental contract detail, never validation or required instructions.
 */
export function SearchParameterLabel({
  controlId,
  label,
  description,
  testId,
}: SearchParameterLabelProps) {
  return (
    <span className={styles.root}>
      <Label htmlFor={controlId}>{label}</Label>
      <Tooltip content={description} relationship="description" withArrow>
        <Button
          type="button"
          appearance="transparent"
          size="small"
          icon={<Info16Regular />}
          aria-label={`About ${label}`}
          data-testid={testId}
        />
      </Tooltip>
    </span>
  );
}
