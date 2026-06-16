import {
  Button,
  DrawerBody,
  DrawerHeader,
  DrawerHeaderTitle,
  OverlayDrawer,
} from "@fluentui/react-components";
import { Dismiss24Regular } from "@fluentui/react-icons";
import { CLI_COMMAND_REFERENCE, type CliCommandDoc } from "~/lib/cli/verbs";
import styles from "./CliCommandReference.module.css";

export interface CliCommandReferenceProps {
  /** Whether the reference drawer is open. */
  open: boolean;
  /** Close the drawer (Esc, the dismiss button, or the scrim). */
  onClose: () => void;
}

interface CommandGroup {
  readonly name: string;
  readonly docs: readonly CliCommandDoc[];
}

/**
 * Groups the flat reference by its `group` field, preserving first-seen
 * order. Computed once at module load — the reference is static.
 */
function groupReference(
  docs: readonly CliCommandDoc[],
): readonly CommandGroup[] {
  const order: string[] = [];
  const byGroup = new Map<string, CliCommandDoc[]>();
  for (const doc of docs) {
    let bucket = byGroup.get(doc.group);
    if (bucket === undefined) {
      bucket = [];
      byGroup.set(doc.group, bucket);
      order.push(doc.group);
    }
    bucket.push(doc);
  }
  return order.map((name) => ({ name, docs: byGroup.get(name) ?? [] }));
}

const GROUPED = groupReference(CLI_COMMAND_REFERENCE);

/**
 * Slide-in "Commands" cheat sheet for the /cli page (#646).
 *
 * Surfaces the same grammar the `help` verb prints, but as a scannable,
 * grouped reference so a first-time operator can discover every verb, its
 * signature, and a runnable example without already knowing to type
 * `help`. The data comes from {@link CLI_COMMAND_REFERENCE}, which a unit
 * test binds to the real parser so the examples can never drift.
 *
 * Render-only: open/close state is owned by the parent CliPage. The modal
 * `OverlayDrawer` manages its own focus trap and Esc-to-close; the
 * window-level Esc handler in `useCli` only fires while a command is in
 * flight and does not stop propagation, so the two never conflict.
 */
export function CliCommandReference({
  open,
  onClose,
}: CliCommandReferenceProps) {
  return (
    <OverlayDrawer
      open={open}
      position="end"
      size="medium"
      onOpenChange={(_, data) => {
        if (!data.open) onClose();
      }}
      data-testid="cli-command-reference-drawer"
    >
      <DrawerHeader>
        <DrawerHeaderTitle
          action={
            <Button
              appearance="subtle"
              aria-label="Close commands"
              icon={<Dismiss24Regular />}
              onClick={onClose}
              data-testid="cli-command-reference-close"
            />
          }
        >
          CLI commands
        </DrawerHeaderTitle>
      </DrawerHeader>
      <DrawerBody>
        <div className={styles.body} data-testid="cli-command-reference">
          <p className={styles.intro}>
            The same grammar as <code>lantern repl</code>. Type a command and
            press <kbd className={styles.kbd}>Enter</kbd>; press{" "}
            <kbd className={styles.kbd}>Tab</kbd> to autocomplete.
          </p>

          {GROUPED.map((group) => (
            <section key={group.name} className={styles.group}>
              <h3 className={styles.groupTitle}>{group.name}</h3>
              <ul className={styles.list}>
                {group.docs.map((doc) => (
                  <li
                    key={doc.signature}
                    className={styles.item}
                    data-testid="cli-command-row"
                  >
                    <code className={styles.signature}>{doc.signature}</code>
                    <p className={styles.summary}>{doc.summary}</p>
                    <code className={styles.example}>
                      <span className={styles.examplePrompt} aria-hidden="true">
                        {"❯ "}
                      </span>
                      {doc.example}
                    </code>
                  </li>
                ))}
              </ul>
            </section>
          ))}

          <section className={styles.group}>
            <h3 className={styles.groupTitle}>Tips</h3>
            <ul className={styles.tips}>
              <li>
                Press <kbd className={styles.kbd}>Tab</kbd> to autocomplete
                verbs, keys, and option names.
              </li>
              <li>
                Quoting: <code>{'"double"'}</code> allows C-style escapes (
                <code>{'\\" \\\\ \\n \\r \\t'}</code>);{" "}
                <code>{"'single'"}</code> is verbatim.
              </li>
              <li>
                Verbs and objectives are case-insensitive; argument values keep
                their case.
              </li>
            </ul>
          </section>
        </div>
      </DrawerBody>
    </OverlayDrawer>
  );
}
