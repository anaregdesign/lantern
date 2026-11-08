import {
  Button,
  Checkbox,
  Field,
  Input,
  Select,
} from "@fluentui/react-components";
import { SecurityPermission, SecurityResource } from "lantern-sdk/web";
import {
  PERMISSIONS,
  RESOURCES,
  type RuleDraft,
} from "~/lib/client/usecase/security/security-drafts";
import { FieldHelp } from "~/components/shared/FieldHelp";
import styles from "./RuleEditor.module.css";

export function RuleEditor({
  rule,
  index,
  change,
  remove,
  error,
}: {
  rule: RuleDraft;
  index: number;
  change(index: number, patch: Partial<RuleDraft>): void;
  remove(index: number): void;
  error?: string;
}) {
  const data = rule.resourceKind === SecurityResource.DATA;
  return (
    <section className={styles.rule} aria-label={`Rule ${index + 1}`}>
      <Field
        label={`Rule ${index + 1} ID`}
        validationMessage={error?.includes("Rule ID") ? error : undefined}
        hint={
          <FieldHelp
            meaning="Unique within this Role; 1–64 lowercase letters, numbers, hyphens or underscores."
            example="read-project"
          />
        }
      >
        <Input
          value={rule.id}
          onChange={(_, value) => change(index, { id: value.value })}
        />
      </Field>
      <Field
        label={`Rule ${index + 1} resource`}
        hint={
          <FieldHelp
            meaning="Data and management resources are independent. A data grant gives no management access."
            example="Logical data for project notes"
          />
        }
      >
        <Select
          value={String(rule.resourceKind)}
          onChange={(_, value) =>
            change(index, {
              resourceKind: Number(value.value) as SecurityResource,
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
      <Field
        label={`Rule ${index + 1} permission`}
        validationMessage={error?.includes("Read only") ? error : undefined}
        hint={
          <FieldHelp
            meaning="Read and Write are independent positive grants. Write includes creation, update, deletion and TTL changes."
            details="Read includes scoped search, traversal, CDC and export with Server disclosure checks. Operations and Schema currently support Read only. No matching grant means refusal."
          />
        }
      >
        <Select
          value={String(rule.permission)}
          onChange={(_, value) =>
            change(index, {
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
      {data && (
        <>
          <Field
            label={`Rule ${index + 1} data scope`}
            hint={
              <FieldHelp
                meaning="Use a literal key prefix, explicit all-data scope, or each assigned user's trusted UID namespace."
                example="projects:demo:"
                details="An empty prefix is all logical data keys only when explicitly selected. * is an ordinary character. Internal sys: metadata is never included. Personal scope is users:<trusted UID>:; email is not an identity or a grant."
              />
            }
          >
            <Select
              value={rule.personal ? "personal" : "prefix"}
              onChange={(_, value) =>
                change(index, {
                  personal: value.value === "personal",
                  allKeys: false,
                })
              }
            >
              <option value="prefix">Literal prefix / all logical data</option>
              <option value="personal">
                Assigned user&apos;s personal namespace
              </option>
            </Select>
          </Field>
          {rule.personal ? (
            <p>
              Each assigned user: {rule.uidBefore}&lt;trusted UID&gt;
              {rule.uidAfter}
            </p>
          ) : (
            <>
              <Checkbox
                label={`Rule ${index + 1}: all data keys`}
                checked={rule.allKeys}
                onChange={(_, value) =>
                  change(index, { allKeys: value.checked === true })
                }
              />
              <Field
                label={`Rule ${index + 1} logical prefix`}
                validationMessage={
                  error && /literal logical|public logical/.test(error)
                    ? error
                    : undefined
                }
                hint="Literal prefix; no wildcard expansion."
              >
                <Input
                  disabled={rule.allKeys}
                  value={rule.prefix}
                  onChange={(_, value) =>
                    change(index, { prefix: value.value })
                  }
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
