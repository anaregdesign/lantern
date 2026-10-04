import {
  Button,
  Checkbox,
  Field,
  Input,
  Select,
} from "@fluentui/react-components";
import { SecurityAction, SecurityEffect } from "lantern-sdk/web";
import {
  ACTIONS,
  pairAction,
  type RuleDraft,
} from "~/lib/client/usecase/security/security-drafts";
import styles from "./RuleEditor.module.css";

export function RuleEditor({
  rule,
  index,
  change,
  remove,
}: {
  rule: RuleDraft;
  index: number;
  change(index: number, patch: Partial<RuleDraft>): void;
  remove(index: number): void;
}) {
  return (
    <section className={styles.rule} aria-label={`Rule ${index + 1}`}>
      <Field label={`Rule ${index + 1} ID`}>
        <Input
          value={rule.id}
          onChange={(_, data) => change(index, { id: data.value })}
        />
      </Field>
      <Field label={`Rule ${index + 1} action`}>
        <Select
          value={String(rule.action)}
          onChange={(_, data) =>
            change(index, { action: Number(data.value) as SecurityAction })
          }
        >
          {ACTIONS.map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </Select>
      </Field>
      <Field label={`Rule ${index + 1} effect`}>
        <Select
          value={String(rule.effect)}
          onChange={(_, data) =>
            change(index, { effect: Number(data.value) as SecurityEffect })
          }
        >
          <option value={SecurityEffect.ALLOW}>Allow</option>
          <option value={SecurityEffect.DENY}>Deny</option>
        </Select>
      </Field>
      {rule.global ? (
        <p>Global capability</p>
      ) : (
        <>
          {pairAction(rule.action) && (
            <Field label={`Rule ${index + 1} selector`}>
              <Select
                value={rule.pair ? "pair" : "prefix"}
                disabled={rule.action === SecurityAction.EDGE_CREATE}
                onChange={(_, data) =>
                  change(index, { pair: data.value === "pair" })
                }
              >
                <option value="prefix">Logical prefix</option>
                <option value="pair">Directed prefix pair</option>
              </Select>
            </Field>
          )}
          {rule.pair ? (
            <>
              <Checkbox
                label={`Rule ${index + 1}: all sources`}
                checked={rule.allTails}
                onChange={(_, data) =>
                  change(index, { allTails: data.checked === true })
                }
              />
              <Field
                label={`Rule ${index + 1} tail prefix`}
                hint="Source endpoint; literal prefix."
              >
                <Input
                  disabled={rule.allTails}
                  value={rule.tailPrefix}
                  onChange={(_, data) =>
                    change(index, { tailPrefix: data.value })
                  }
                />
              </Field>
              <Checkbox
                label={`Rule ${index + 1}: all targets`}
                checked={rule.allHeads}
                onChange={(_, data) =>
                  change(index, { allHeads: data.checked === true })
                }
              />
              <Field
                label={`Rule ${index + 1} head prefix`}
                hint="Target endpoint; direction matters."
              >
                <Input
                  disabled={rule.allHeads}
                  value={rule.headPrefix}
                  onChange={(_, data) =>
                    change(index, { headPrefix: data.value })
                  }
                />
              </Field>
              <p>
                Both prefixes must match. Assign endpoint read access
                separately.
              </p>
            </>
          ) : (
            <>
              <Checkbox
                label={`Rule ${index + 1}: all data keys`}
                checked={rule.allKeys}
                onChange={(_, data) =>
                  change(index, { allKeys: data.checked === true })
                }
              />
              <Field
                label={`Rule ${index + 1} logical prefix`}
                hint="Literal prefix; * is an ordinary character."
              >
                <Input
                  disabled={rule.allKeys}
                  value={rule.prefix}
                  onChange={(_, data) => change(index, { prefix: data.value })}
                />
              </Field>
            </>
          )}
        </>
      )}
      <Button onClick={() => remove(index)}>Remove rule {index + 1}</Button>
    </section>
  );
}
