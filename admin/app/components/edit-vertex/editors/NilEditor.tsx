import { MessageBar, MessageBarBody } from "@fluentui/react-components";

/**
 * `nil` is a presence-only marker — there is nothing to type. We still
 * render an explanatory MessageBar so the form has visible feedback.
 */
export function NilEditor() {
  return (
    <MessageBar intent="info">
      <MessageBarBody>
        Stores the key with no value. Useful for tombstones or set membership.
      </MessageBarBody>
    </MessageBar>
  );
}
