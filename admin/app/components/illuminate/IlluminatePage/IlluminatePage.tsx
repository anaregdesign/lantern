import { useCallback, useMemo, useRef, useState } from "react";
import { MessageBar, MessageBarBody } from "@fluentui/react-components";
import { useNavigate, useSearchParams } from "react-router";
import {
  IlluminateCanvas,
  type IlluminateCanvasHandle,
} from "../IlluminateCanvas/IlluminateCanvas";
import { IlluminateTable } from "../IlluminateTable/IlluminateTable";
import { IlluminateToolbar } from "../IlluminateToolbar/IlluminateToolbar";
import { NodeDetailPanel } from "../NodeDetailPanel/NodeDetailPanel";
import { SeedPrompt } from "../SeedPrompt/SeedPrompt";
import { useIlluminate } from "~/lib/client/usecase/illuminate/use-illuminate";
import { selectInspectedDetail } from "~/lib/client/usecase/illuminate/selectors";
import { ACCUMULATOR_SOFT_CAP } from "~/lib/client/usecase/illuminate/state";
import styles from "./IlluminatePage.module.css";

/**
 * Top-level orchestrator for the Illuminate screen. Per #466 the URL's
 * `?seed=` query param is the source of truth for `initialSeed` only —
 * subsequent clicks on canvas/table append expansions to the in-memory
 * audit trail without changing the URL. Use the SeedPrompt or Clear
 * button to change the URL-level seed.
 */
export function IlluminatePage() {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const rawSeed = searchParams.get("seed") ?? "";
  const urlSeed = decodeSeed(rawSeed);

  const ill = useIlluminate(urlSeed);

  const canvasRef = useRef<IlluminateCanvasHandle | null>(null);

  // #461 node-detail Drawer. The inspected vertex is pure UI state
  // (page-local) — opening/closing never touches the accumulator,
  // expansions, or camera. The selector projects the live accumulator
  // into the panel's view-model and returns null once the vertex is
  // gone (TTL sweep), so the Drawer self-closes without extra wiring.
  const [inspectedVertex, setInspectedVertex] = useState<string | null>(null);
  const inspectedDetail = useMemo(
    () => selectInspectedDetail(ill.state, inspectedVertex),
    [ill.state, inspectedVertex],
  );

  const handleNodeClick = useCallback(
    (key: string) => {
      if (!key) return;
      // A node-body click is an additive expansion (#466 D11, idempotent).
      // It also dismisses the inspect Drawer so a body click never leaves
      // a stale panel open over the freshly expanded neighbourhood.
      setInspectedVertex(null);
      ill.expand(key);
    },
    [ill],
  );

  // #461 "Expand from here": same additive expansion as a body click,
  // then close the Drawer so the canvas takes focus on the new nodes.
  const handleExpandFromHere = useCallback(
    (key: string) => {
      ill.expand(key);
      setInspectedVertex(null);
    },
    [ill],
  );

  // #456 scroll-to-origin: pure camera move, no state mutation, no RPC.
  const handleChipClick = useCallback((originKey: string) => {
    canvasRef.current?.panToNode(originKey);
  }, []);

  const handleClear = useCallback(() => {
    // Clear navigates back to the bare `/illuminate` URL; the hook's
    // INITIAL_SEED_CHANGED handler aborts in-flight expansions and resets
    // the accumulator when it sees the empty URL.
    navigate("/illuminate");
  }, [navigate]);

  const openFromPrompt = useCallback(
    (seed: string) => {
      navigate(`/illuminate?seed=${encodeURIComponent(seed)}`);
    },
    [navigate],
  );

  return (
    <div className={styles.root}>
      <header className={styles.header}>
        <h1 className={styles.title}>Illuminate</h1>
        <p className={styles.lead}>
          Explore a vertex&rsquo;s neighbourhood. Click any node to expand its
          neighbourhood into the canvas; use <em>Clear</em> to start over.
        </p>
      </header>

      {urlSeed === "" ? (
        <SeedPrompt onOpen={openFromPrompt} />
      ) : (
        <>
          <IlluminateToolbar
            initialSeed={ill.state.initialSeed ?? ""}
            controls={ill.state.controls}
            status={ill.state.status}
            canClear={ill.canClear}
            vertexCount={ill.view.nodes.length}
            edgeCount={ill.view.edges.length}
            expansionCount={ill.expansionCount}
            expansionChips={ill.expansionChips}
            onControlsChange={ill.setControls}
            onClear={handleClear}
            onRefresh={ill.refresh}
            onChipClick={handleChipClick}
            onExpandFromKey={ill.expand}
          />
          {ill.state.error ? (
            <MessageBar
              intent="error"
              data-testid="illuminate-error"
              className={styles.alert}
            >
              <MessageBarBody>{ill.state.error}</MessageBarBody>
            </MessageBar>
          ) : null}
          {ill.view.overSoftCap ? (
            <MessageBar
              intent="warning"
              data-testid="illuminate-soft-cap"
              className={styles.alert}
            >
              <MessageBarBody>
                Accumulator past the soft cap of {ACCUMULATOR_SOFT_CAP} vertices
                ({ill.view.nodes.length}). Layout may slow down; use Clear to
                start over.
              </MessageBarBody>
            </MessageBar>
          ) : null}
          <IlluminateCanvas
            ref={canvasRef}
            nodes={ill.view.nodes}
            edges={ill.view.edges}
            latestExpansionOrigin={ill.view.latestExpansionOrigin}
            latestResultVertexKeys={ill.view.latestResultVertexKeys}
            latestResultEdgeIds={ill.view.latestResultEdgeIds}
            onNodeClick={handleNodeClick}
            onNodeInspect={setInspectedVertex}
            isBusy={ill.isBusy}
          />
          <NodeDetailPanel
            detail={inspectedDetail}
            onClose={() => setInspectedVertex(null)}
            onExpandFromHere={handleExpandFromHere}
          />
          <details className={styles.tableDisclosure}>
            <summary className={styles.summary}>
              List view ({ill.view.nodes.length} vertices,&nbsp;
              {ill.view.edges.length} edges)
            </summary>
            <IlluminateTable nodes={ill.view.nodes} onExpand={ill.expand} />
          </details>
        </>
      )}
    </div>
  );
}

function decodeSeed(raw: string): string {
  if (raw === "") return "";
  try {
    return decodeURIComponent(raw);
  } catch {
    // Browser already decoded once; pass through unmodified.
    return raw;
  }
}
