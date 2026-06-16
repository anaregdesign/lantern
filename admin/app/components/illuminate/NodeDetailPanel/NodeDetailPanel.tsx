import { useCallback, useEffect, useRef, useState } from "react";
import {
  Button,
  DrawerBody,
  DrawerHeader,
  DrawerHeaderTitle,
  OverlayDrawer,
  Spinner,
  Tooltip,
} from "@fluentui/react-components";
import {
  ArrowExpand20Regular,
  Copy16Regular,
  Dismiss20Regular,
  Open20Regular,
} from "@fluentui/react-icons";
import { useNavigate } from "react-router";
import { ExpirationCell } from "~/components/browse-vertices/ExpirationCell/ExpirationCell";
import { ValueCell } from "~/components/browse-vertices/ValueCell/ValueCell";
import { StringValueView } from "~/components/shared/StringValueView/StringValueView";
import type { InspectedVertexDetail } from "~/lib/client/usecase/illuminate/selectors";
import { edgeIdOf } from "~/lib/client/usecase/illuminate/state";
import { useInboundEdges } from "~/lib/client/usecase/illuminate/use-inbound-edges";
import styles from "./NodeDetailPanel.module.css";

export interface NodeDetailPanelProps {
  /**
   * The inspected vertex's view-model, or `null` when the Drawer is
   * closed. Driving `open` straight off this prop means a TTL sweep that
   * drops the inspected vertex (selector returns `null`) auto-closes the
   * Drawer with no extra wiring (#461).
   */
  detail: InspectedVertexDetail | null;
  /** Dismiss the Drawer (Esc, close button, or backdrop) → clears selection. */
  onClose: () => void;
  /**
   * "Expand from here" — fire an additive expansion from the inspected
   * vertex (same path as a node-body click) and close the Drawer (#461).
   */
  onExpandFromHere: (key: string) => void;
}

const COPIED_RESET_MS = 1_200;

/**
 * Read-only inspector for a single vertex, surfaced as an end-anchored
 * Fluent Drawer (#461). Opens from the per-node info icon on the canvas;
 * shows the vertex key (with copy-to-clipboard), its decoded value and
 * expiration, the outgoing edges already in the accumulator (no fetch),
 * and an on-demand "Show inbound edges" action that scans for edges
 * terminating at this vertex WITHOUT merging them into the canvas
 * accumulator (the canvas stays the sole graph owner per #466).
 *
 * Non-modal so the canvas stays interactive while the Drawer is pinned —
 * #458 hover focus keeps following the cursor underneath it.
 */
export function NodeDetailPanel({
  detail,
  onClose,
  onExpandFromHere,
}: NodeDetailPanelProps) {
  const navigate = useNavigate();
  const inbound = useInboundEdges(detail?.key ?? null);
  const [copied, setCopied] = useState(false);
  const copiedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Clear the transient "Copied" badge whenever the inspected vertex
  // changes or the component unmounts, so it never lingers on the wrong
  // key.
  useEffect(() => {
    setCopied(false);
    return () => {
      if (copiedTimerRef.current) {
        clearTimeout(copiedTimerRef.current);
        copiedTimerRef.current = null;
      }
    };
  }, [detail?.key]);

  const copyKey = useCallback(async (key: string) => {
    try {
      await navigator.clipboard?.writeText(key);
      setCopied(true);
      if (copiedTimerRef.current) clearTimeout(copiedTimerRef.current);
      copiedTimerRef.current = setTimeout(() => {
        setCopied(false);
        copiedTimerRef.current = null;
      }, COPIED_RESET_MS);
    } catch {
      // Clipboard can reject in insecure contexts / when permission is
      // denied. The key is still visible in the header, so there's
      // nothing to recover — just skip the "Copied" affordance.
    }
  }, []);

  const open = detail !== null;

  return (
    <OverlayDrawer
      as="aside"
      position="end"
      modalType="non-modal"
      open={open}
      onOpenChange={(_, data) => {
        if (!data.open) onClose();
      }}
      data-testid="illuminate-node-detail"
      className={styles.drawer}
    >
      {detail ? (
        <>
          <DrawerHeader>
            <DrawerHeaderTitle
              action={
                <Button
                  appearance="subtle"
                  aria-label="Close vertex detail"
                  icon={<Dismiss20Regular />}
                  data-testid="illuminate-detail-close"
                  onClick={onClose}
                />
              }
            >
              <span className={styles.headerLabel}>Vertex</span>
            </DrawerHeaderTitle>
          </DrawerHeader>
          <DrawerBody>
            <div className={styles.body}>
              <section className={styles.section}>
                <div className={styles.keyRow}>
                  <code
                    className={styles.key}
                    data-testid="illuminate-detail-key"
                  >
                    {detail.key}
                  </code>
                  <Tooltip
                    content={copied ? "Copied" : "Copy key"}
                    relationship="label"
                    withArrow
                  >
                    <Button
                      appearance="subtle"
                      size="small"
                      icon={<Copy16Regular />}
                      aria-label="Copy vertex key"
                      data-testid="illuminate-detail-copy"
                      onClick={() => void copyKey(detail.key)}
                    />
                  </Tooltip>
                  {copied ? (
                    <span
                      className={styles.copied}
                      role="status"
                      data-testid="illuminate-detail-copied"
                    >
                      Copied
                    </span>
                  ) : null}
                </div>
              </section>

              <section className={styles.section}>
                <h3 className={styles.sectionTitle}>Value</h3>
                {typeof detail.vertex.string === "string" ? (
                  <StringValueView value={detail.vertex.string} />
                ) : (
                  <ValueCell vertex={detail.vertex} />
                )}
              </section>

              <section className={styles.section}>
                <h3 className={styles.sectionTitle}>Expires</h3>
                <ExpirationCell expiration={detail.vertex.expiration} />
              </section>

              <section className={styles.section}>
                <h3 className={styles.sectionTitle}>
                  Outgoing edges ({detail.outgoing.length})
                </h3>
                {detail.outgoing.length === 0 ? (
                  <p className={styles.empty}>No outgoing edges.</p>
                ) : (
                  <ul
                    className={styles.edgeList}
                    data-testid="illuminate-detail-outgoing"
                  >
                    {detail.outgoing.map((o) => (
                      <li key={o.id} className={styles.edgeRow}>
                        <span className={styles.edgeArrow} aria-hidden="true">
                          &rarr;
                        </span>
                        <code className={styles.edgeTarget}>{o.target}</code>
                        <span className={styles.edgeWeight}>w={o.weight}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>

              <section className={styles.section}>
                <h3 className={styles.sectionTitle}>Inbound edges</h3>
                <InboundEdges
                  status={inbound.status}
                  edges={inbound.edges}
                  error={inbound.error}
                  onLoad={inbound.load}
                />
              </section>

              <section className={styles.actions}>
                <Button
                  appearance="primary"
                  icon={<ArrowExpand20Regular />}
                  data-testid="illuminate-detail-expand"
                  onClick={() => onExpandFromHere(detail.key)}
                >
                  Expand from here
                </Button>
                <Button
                  appearance="secondary"
                  icon={<Open20Regular />}
                  data-testid="illuminate-detail-open-page"
                  onClick={() =>
                    navigate(`/vertices/${encodeURIComponent(detail.key)}`)
                  }
                >
                  Open vertex page
                </Button>
              </section>
            </div>
          </DrawerBody>
        </>
      ) : null}
    </OverlayDrawer>
  );
}

interface InboundEdgesProps {
  status: ReturnType<typeof useInboundEdges>["status"];
  edges: ReturnType<typeof useInboundEdges>["edges"];
  error: string | null;
  onLoad: () => void;
}

/**
 * The on-demand inbound-edge sub-view. Split out so the load/loading/
 * loaded/error states stay readable and the parent body keeps to its
 * declarative section layout.
 */
function InboundEdges({ status, edges, error, onLoad }: InboundEdgesProps) {
  if (status === "idle") {
    return (
      <Button
        appearance="subtle"
        size="small"
        data-testid="illuminate-detail-inbound-toggle"
        onClick={onLoad}
      >
        Show inbound edges
      </Button>
    );
  }
  if (status === "loading") {
    return <Spinner size="tiny" label={"Loading inbound edges\u2026"} />;
  }
  if (status === "error") {
    return (
      <div className={styles.inboundError}>
        <p
          className={styles.errorText}
          data-testid="illuminate-detail-inbound-error"
        >
          {error ?? "Failed to load inbound edges."}
        </p>
        <Button appearance="subtle" size="small" onClick={onLoad}>
          Retry
        </Button>
      </div>
    );
  }
  // loaded
  if (edges.length === 0) {
    return <p className={styles.empty}>No inbound edges.</p>;
  }
  return (
    <ul className={styles.edgeList} data-testid="illuminate-detail-inbound">
      {edges.map((e) => {
        const tail = e.tail ?? "";
        const head = e.head ?? "";
        return (
          <li key={edgeIdOf(tail, head)} className={styles.edgeRow}>
            <code className={styles.edgeTarget}>{tail}</code>
            <span className={styles.edgeArrow} aria-hidden="true">
              &rarr;
            </span>
            <span className={styles.edgeWeight}>w={e.weight ?? 0}</span>
          </li>
        );
      })}
    </ul>
  );
}
