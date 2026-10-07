import { Field, Select } from "@fluentui/react-components";
import type { ScopeOption } from "~/lib/client/usecase/data-scope/scope-options";
import styles from "./ScopeSelector.module.css";

export function ScopeSelector({
  prefix,
  options,
  denied,
  select,
}: {
  prefix: string;
  options: ScopeOption[];
  denied: string[];
  select(value: string): void;
}) {
  const custom = prefix !== "" && !options.some((o) => o.prefix === prefix);
  return (
    <section className={styles.root} aria-label="Current Role scope">
      <Field label="Role scope">
        <Select
          value={prefix}
          onChange={(_, data) => select(data.value)}
          data-testid="data-scope-select"
        >
          <option value="">
            {options.length ? "Current authorized keys" : "No granted scope"}
          </option>
          {options
            .filter((o) => o.prefix !== "")
            .map((option) => (
              <option key={option.prefix} value={option.prefix}>
                {option.prefix}
              </option>
            ))}
          {custom ? (
            <option value={prefix}>{prefix} (explicit prefix)</option>
          ) : null}
        </Select>
      </Field>
      <p className={styles.help}>
        Suggestions come from your current Roles. The Server checks every
        request.
      </p>
      {denied.length ? (
        <p className={styles.help} data-testid="scope-deny-exceptions">
          Deny exceptions:{" "}
          {denied.map((value) => value || "all logical keys").join(", ")}.
        </p>
      ) : null}
    </section>
  );
}
