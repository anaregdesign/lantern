import {
  Button,
  Field,
  Input,
  Select,
  Title3,
} from "@fluentui/react-components";
import {
  SecurityAction,
  SecurityEffect,
  type ExplainAccessResponse,
} from "lantern-sdk/web";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import { pairAction } from "~/lib/client/usecase/security/security-drafts";
import styles from "./AccessExplanation.module.css";

export function AccessExplanation({
  editor,
  result,
  disabled,
}: {
  editor: SecurityEditor;
  result?: ExplainAccessResponse;
  disabled: boolean;
}) {
  return (
    <section className={styles.panel} aria-label="Explain access">
      <Title3>Explain effective access</Title3>
      <fieldset disabled={disabled} className={styles.fields}>
        <Field label="Explanation Issuer">
          <Input
            value={editor.explanation.issuer}
            onChange={(_, data) =>
              editor.patchExplanation({ issuer: data.value })
            }
          />
        </Field>
        <Field label="Explanation subject">
          <Input
            value={editor.explanation.subject}
            onChange={(_, data) =>
              editor.patchExplanation({ subject: data.value })
            }
          />
        </Field>
        <Field label="Explanation action">
          <Select
            value={String(editor.explanation.action)}
            onChange={(_, data) =>
              editor.patchExplanation({
                action: Number(data.value) as SecurityAction,
                edge:
                  pairAction(Number(data.value) as SecurityAction) &&
                  (editor.explanation.edge ||
                    Number(data.value) === SecurityAction.EDGE_CREATE),
              })
            }
          >
            {editor.actions.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </Select>
        </Field>
        {pairAction(editor.explanation.action) && (
          <Field label="Explanation resource">
            <Select
              value={editor.explanation.edge ? "edge" : "key"}
              onChange={(_, data) =>
                editor.patchExplanation({ edge: data.value === "edge" })
              }
            >
              <option value="key">Logical key</option>
              <option value="edge">Directed Edge</option>
            </Select>
          </Field>
        )}
        {editor.explanation.edge ? (
          <>
            <Field label="Explanation tail">
              <Input
                value={editor.explanation.tail}
                onChange={(_, data) =>
                  editor.patchExplanation({ tail: data.value })
                }
              />
            </Field>
            <Field label="Explanation head">
              <Input
                value={editor.explanation.head}
                onChange={(_, data) =>
                  editor.patchExplanation({ head: data.value })
                }
              />
            </Field>
            <p>
              Check endpoint read access and any other required actions
              separately.
            </p>
          </>
        ) : (
          <Field
            label="Logical key"
            hint="Leave empty for a global capability."
          >
            <Input
              value={editor.explanation.key}
              onChange={(_, data) =>
                editor.patchExplanation({ key: data.value })
              }
            />
          </Field>
        )}
        <Button onClick={editor.explain}>Ask Server</Button>
      </fieldset>
      {result && (
        <div role="status">
          <p>
            {result.allowed ? "Allowed" : "Denied"} · Server revision{" "}
            {result.version?.revision.toString()}
          </p>
          <ul>
            {result.matches.map((match, i) => (
              <li key={i}>
                {match.roleId} / {match.ruleId}:{" "}
                {match.effect === SecurityEffect.DENY ? "Deny" : "Allow"}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
