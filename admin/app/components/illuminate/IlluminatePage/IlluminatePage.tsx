import { useCallback, useEffect } from "react";
import { MessageBar, MessageBarBody } from "@fluentui/react-components";
import { useNavigate, useSearchParams } from "react-router";
import { IlluminateCanvas } from "../IlluminateCanvas/IlluminateCanvas";
import { IlluminateTable } from "../IlluminateTable/IlluminateTable";
import { IlluminateToolbar } from "../IlluminateToolbar/IlluminateToolbar";
import { SeedPrompt } from "../SeedPrompt/SeedPrompt";
import { useIlluminate } from "~/lib/client/usecase/illuminate/use-illuminate";
import styles from "./IlluminatePage.module.css";

/**
 * Top-level orchestrator for the Illuminate screen. The URL's `?seed=`
 * query param is the source of truth for the active seed; pushing a new
 * seed via the canvas / table updates the URL so the browser back button
 * walks the user back through the seed history naturally.
 */
export function IlluminatePage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const rawSeed = searchParams.get("seed") ?? "";
  const urlSeed = decodeSeed(rawSeed);

  const ill = useIlluminate(urlSeed);

  // Mirror reducer-driven seed changes (push/pop) back into the URL so
  // the browser history walks naturally. The URL is authoritative on the
  // way IN (a dedicated effect inside `useIlluminate` syncs `urlSeed` →
  // reducer); this effect is purely state → URL, and must NEVER clear
  // the URL from a transient empty reducer state — that would race the
  // initial SEED_CHANGED priming step and wipe the seed the user just
  // navigated to.
  useEffect(() => {
    if (ill.state.seed === "" || ill.state.seed === urlSeed) return;
    setSearchParams({ seed: ill.state.seed }, { replace: false });
  }, [ill.state.seed, urlSeed, setSearchParams]);

  const handleNodeClick = useCallback(
    (key: string) => {
      if (key && key !== ill.state.seed) {
        ill.push(key);
      }
    },
    [ill],
  );

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
          Explore a vertex&rsquo;s neighbourhood. Click a node to re-seed; use{" "}
          <em>Pop</em> to walk back through the history stack.
        </p>
      </header>

      {urlSeed === "" ? (
        <SeedPrompt onOpen={openFromPrompt} />
      ) : (
        <>
          <IlluminateToolbar
            seed={ill.state.seed}
            controls={ill.state.controls}
            status={ill.state.status}
            canPop={ill.canPop}
            onControlsChange={ill.setControls}
            onPop={ill.pop}
            onRefresh={ill.refresh}
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
          <IlluminateCanvas
            nodes={ill.view.nodes}
            edges={ill.view.edges}
            onNodeClick={handleNodeClick}
            isBusy={ill.isBusy}
          />
          <details className={styles.tableDisclosure}>
            <summary className={styles.summary}>
              List view ({ill.view.nodes.length} vertices,&nbsp;
              {ill.view.edges.length} edges)
            </summary>
            <IlluminateTable nodes={ill.view.nodes} onIlluminate={ill.push} />
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
