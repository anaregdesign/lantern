import {
  Button,
  MessageBar,
  MessageBarBody,
  Spinner,
} from "@fluentui/react-components";
import { Delete20Regular } from "@fluentui/react-icons";
import { Link, useNavigate } from "react-router";
import { useEffect } from "react";
import { useEditEdge } from "~/lib/client/usecase/edit-edge/use-edit-edge";
import type { Edge } from "~/lib/client/infrastructure/api/get-edge";
import { ExpirationCell } from "../../browse-edges/ExpirationCell/ExpirationCell";
import { DeleteEdgeDialog } from "../DeleteEdgeDialog/DeleteEdgeDialog";
import { EdgeWriteForm } from "../EdgeWriteForm/EdgeWriteForm";
import styles from "./EdgeDetailPage.module.css";

export interface EdgeDetailPageProps {
  tail: string;
  head: string;
}

/**
 * Detail view for a directed edge. Surfaces both Add (accumulate) and
 * Put (overwrite) forms side-by-side because users repeatedly confused
 * the two in the F2 dogfood; we now keep the help text adjacent.
 */
export function EdgeDetailPage(props: EdgeDetailPageProps) {
  const navigate = useNavigate();
  const editor = useEditEdge(props.tail, props.head);

  useEffect(() => {
    if (editor.deleted) {
      navigate("/edges");
    }
  }, [editor.deleted, navigate]);

  return (
    <div className={styles.root}>
      <header className={styles.header}>
        <div className={styles.crumbs}>
          <Link to="/edges">Edges</Link>
          <span aria-hidden="true">/</span>
          <Link to={`/vertices/${encodeURIComponent(props.tail)}`}>
            {props.tail}
          </Link>
          <span aria-hidden="true">→</span>
          <Link to={`/vertices/${encodeURIComponent(props.head)}`}>
            {props.head}
          </Link>
        </div>
        <h1 className={styles.title}>Edge</h1>
        <div className={styles.edgePair}>
          <span data-testid="edge-detail-tail">{props.tail}</span>
          <span className={styles.arrow} aria-hidden="true">
            →
          </span>
          <span data-testid="edge-detail-head">{props.head}</span>
        </div>
      </header>

      {editor.state.loadStatus === "loading" ? (
        <div className={styles.placeholder} data-testid="edge-detail-loading">
          <Spinner label="Loading edge…" size="small" />
        </div>
      ) : null}

      {editor.state.loadStatus === "error" ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>
            {editor.state.loadError ?? "Unable to load edge."}
          </MessageBarBody>
        </MessageBar>
      ) : null}

      {editor.state.loadStatus === "ready" && editor.state.edge ? (
        <CurrentEdge edge={editor.state.edge} onDelete={editor.openDeleteDialog} />
      ) : null}

      {editor.state.loadStatus === "not-found" ? (
        <section className={styles.card} data-testid="edge-detail-missing">
          <h2 className={styles.cardTitle}>Edge not found</h2>
          <p>
            No edge currently exists. Use the form below to create one with
            either AddEdge or PutEdge.
          </p>
        </section>
      ) : null}

      <div className={styles.cards}>
        <EdgeWriteForm
          mode="add"
          title="Add contribution"
          description="Appends another time-decaying contribution. Repeated calls accumulate weight at the current decay rate."
          inputs={editor.state.addInputs}
          status={editor.state.addStatus}
          error={editor.state.addError}
          valid={editor.addValid}
          onWeight={(v) => editor.setWeight("add", v)}
          onTtl={(t) => editor.setTtl("add", t)}
          onSubmit={editor.submitAdd}
          submitLabel="Add contribution"
        />
        <EdgeWriteForm
          mode="put"
          title="Replace edge"
          description="Overwrites the edge with this exact weight and expiration. Idempotent — repeated calls leave the same value."
          inputs={editor.state.putInputs}
          status={editor.state.putStatus}
          error={editor.state.putError}
          valid={editor.putValid}
          onWeight={(v) => editor.setWeight("put", v)}
          onTtl={(t) => editor.setTtl("put", t)}
          onSubmit={editor.submitPut}
          submitLabel="Replace edge"
        />
      </div>

      <DeleteEdgeDialog
        open={editor.state.deleteRequested}
        tail={props.tail}
        head={props.head}
        deleting={editor.state.deleteStatus === "deleting"}
        onCancel={editor.closeDeleteDialog}
        onConfirm={editor.confirmDelete}
      />

      {editor.state.deleteStatus === "error" ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>
            {editor.state.deleteError ?? "Delete failed."}
          </MessageBarBody>
        </MessageBar>
      ) : null}
    </div>
  );
}

interface CurrentEdgeProps {
  edge: Edge;
  onDelete: () => void;
}

function CurrentEdge({ edge, onDelete }: CurrentEdgeProps) {
  return (
    <section className={styles.card} data-testid="edge-detail-read">
      <h2 className={styles.cardTitle}>Current edge</h2>
      <div className={styles.viewRow}>
        <span className={styles.viewLabel}>Weight</span>
        <span className={styles.viewValue} data-testid="edge-current-weight">
          {edge.weight ?? 0}
        </span>
      </div>
      <div className={styles.viewRow}>
        <span className={styles.viewLabel}>Expires</span>
        <span className={styles.viewValue}>
          <ExpirationCell expiration={edge.expiration} />
        </span>
      </div>
      <div className={styles.formActions}>
        <Button
          appearance="secondary"
          icon={<Delete20Regular />}
          onClick={onDelete}
          data-testid="edge-delete-trigger"
        >
          Delete
        </Button>
      </div>
    </section>
  );
}
