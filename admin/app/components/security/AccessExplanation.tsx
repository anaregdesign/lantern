import {
  Button,
  Field,
  Input,
  Select,
  Title3,
} from "@fluentui/react-components";
import {
  SecurityPermission,
  SecurityResource,
  type ExplainAccessResponse,
} from "lantern-sdk/web";
import { FieldHelp } from "~/components/shared/FieldHelp";
import type { SecurityEditor } from "~/lib/client/usecase/security/use-security-editor";
import {
  PERMISSIONS,
  RESOURCES,
} from "~/lib/client/usecase/security/security-drafts";
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
      <p>
        Server evaluates current assigned Roles and the shared Edge policy. This
        check grants no access.
      </p>
      <fieldset disabled={disabled} className={styles.fields}>
        <Field
          label="Explanation Issuer"
          hint={
            <FieldHelp
              meaning="Exact configured Issuer of the user to inspect."
              example="https://idp.example"
            />
          }
        >
          <Input
            value={editor.explanation.issuer}
            onChange={(_, value) =>
              editor.patchExplanation({ issuer: value.value })
            }
          />
        </Field>
        <Field
          label="Explanation subject"
          hint={
            <FieldHelp
              meaning="Exact provider subject, independent of display email."
              example="sample-user-42"
            />
          }
        >
          <Input
            value={editor.explanation.subject}
            onChange={(_, value) =>
              editor.patchExplanation({ subject: value.value })
            }
          />
        </Field>
        <Field
          label="Explanation permission"
          hint="Read and Write are independent. Write includes Delete and TTL changes."
        >
          <Select
            value={String(editor.explanation.permission)}
            onChange={(_, value) =>
              editor.patchExplanation({
                permission: Number(value.value) as SecurityPermission,
              })
            }
          >
            {PERMISSIONS.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </Select>
        </Field>
        <Field
          label="Explanation resource"
          hint="Data grants never grant Security, Settings, Operations or Schema access."
        >
          <Select
            value={String(editor.explanation.resourceKind)}
            onChange={(_, value) =>
              editor.patchExplanation({
                resourceKind: Number(value.value) as SecurityResource,
                edge: false,
              })
            }
          >
            {RESOURCES.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </Select>
        </Field>
        {editor.explanation.resourceKind === SecurityResource.DATA && (
          <>
            <Field
              label="Data target"
              hint="Edges have derived endpoint authority; no independent Edge grant."
            >
              <Select
                value={editor.explanation.edge ? "edge" : "key"}
                onChange={(_, value) =>
                  editor.patchExplanation({ edge: value.value === "edge" })
                }
              >
                <option value="key">Logical key</option>
                <option value="edge">Directed Edge</option>
              </Select>
            </Field>
            {editor.explanation.edge ? (
              <>
                <Field
                  label="Explanation tail"
                  hint={
                    <FieldHelp
                      meaning="The source endpoint of the directed Edge."
                      example="projects:demo:alice"
                    />
                  }
                >
                  <Input
                    value={editor.explanation.tail}
                    onChange={(_, value) =>
                      editor.patchExplanation({ tail: value.value })
                    }
                  />
                </Field>
                <Field
                  label="Explanation head"
                  hint={
                    <FieldHelp
                      meaning="The destination endpoint of the directed Edge."
                      example="projects:demo:bob"
                    />
                  }
                >
                  <Input
                    value={editor.explanation.head}
                    onChange={(_, value) =>
                      editor.patchExplanation({ head: value.value })
                    }
                  />
                </Field>
                <p>
                  Reading an Edge needs Read on both endpoints. The shared
                  editing rule chooses Read or Write separately for tail → head.
                  Under readable → readable, Vertex readers can edit eligible
                  Edges; writable modes may allow blind edits.
                </p>
              </>
            ) : (
              <Field
                label="Logical key"
                hint={
                  <FieldHelp
                    meaning="Exact public logical key to check; * is an ordinary character."
                    example="projects:demo:note"
                  />
                }
              >
                <Input
                  value={editor.explanation.key}
                  onChange={(_, value) =>
                    editor.patchExplanation({ key: value.value })
                  }
                />
              </Field>
            )}
          </>
        )}
        <Button onClick={editor.explain}>Ask Server</Button>
      </fieldset>
      {result && (
        <div role="status">
          <p>
            {result.allowed ? "Allowed" : "Refused"} · Policy cut{" "}
            {result.version?.currentCut?.sequence.toString()}
          </p>
          <ul>
            {result.matches.map((match, index) => (
              <li key={index}>
                {match.roleId} / {match.ruleId}: Allow{" "}
                {
                  PERMISSIONS.find(
                    ([permission]) => permission === match.permission,
                  )?.[1]
                }{" "}
                on{" "}
                {
                  RESOURCES.find(
                    ([resource]) => resource === match.resourceKind,
                  )?.[1]
                }
                {match.endpoint && ` · ${match.endpoint}`}
              </li>
            ))}
          </ul>
          {!result.allowed && (
            <p>No sufficient matching Allow under the current policy.</p>
          )}
        </div>
      )}
    </section>
  );
}
