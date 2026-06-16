import {
  Badge,
  Button,
  MessageBar,
  MessageBarBody,
  Spinner,
} from "@fluentui/react-components";
import {
  Delete20Regular,
  Edit20Regular,
  LightbulbFilament20Regular,
  Save20Regular,
} from "@fluentui/react-icons";
import { Link, useNavigate } from "react-router";
import { useEffect } from "react";
import { useEditVertex } from "~/lib/client/usecase/edit-vertex/use-edit-vertex";
import { kindOfVertex } from "~/lib/client/usecase/edit-vertex/value-codec";
import type { Vertex } from "~/lib/client/infrastructure/api/get-vertex";
import { ExpirationCell } from "../../browse-vertices/ExpirationCell/ExpirationCell";
import { ValueCell } from "../../browse-vertices/ValueCell/ValueCell";
import { StringValueView } from "../../shared/StringValueView/StringValueView";
import { DeleteVertexDialog } from "../DeleteVertexDialog/DeleteVertexDialog";
import { KindSelector } from "../KindSelector/KindSelector";
import { TtlField } from "../TtlField/TtlField";
import { ValueEditor } from "../ValueEditor/ValueEditor";
import styles from "./VertexDetailPage.module.css";

export interface VertexDetailPageProps {
  vertexKey: string;
}

/**
 * Read/Edit/Delete page for a single vertex. Begins in view mode so the
 * user can confirm they have the right row before mutating it, then
 * flips to an inline editor sharing reducer state with the API handlers.
 */
export function VertexDetailPage(props: VertexDetailPageProps) {
  const navigate = useNavigate();
  const editor = useEditVertex(props.vertexKey);

  // After a successful delete, redirect to the listing.
  useEffect(() => {
    if (editor.deleted) {
      navigate("/vertices");
    }
  }, [editor.deleted, navigate]);

  return (
    <div className={styles.root}>
      <header className={styles.header}>
        <div className={styles.crumbs}>
          <Link to="/vertices">Vertices</Link>
          <span aria-hidden="true">/</span>
          <span data-testid="vertex-detail-key">{props.vertexKey}</span>
        </div>
        <h1 className={styles.title}>{props.vertexKey}</h1>
        <div className={styles.toolbar}>
          <Button
            appearance="subtle"
            icon={<LightbulbFilament20Regular />}
            onClick={() =>
              navigate(
                `/illuminate?seed=${encodeURIComponent(props.vertexKey)}`,
              )
            }
          >
            Illuminate
          </Button>
        </div>
      </header>

      {editor.state.loadStatus === "loading" ? (
        <div className={styles.placeholder} data-testid="vertex-detail-loading">
          <Spinner label="Loading vertex…" size="small" />
        </div>
      ) : null}

      {editor.state.loadStatus === "error" ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>
            {editor.state.loadError ?? "Unable to load vertex."}
          </MessageBarBody>
        </MessageBar>
      ) : null}

      {editor.state.loadStatus === "not-found" ? (
        <NotFoundView
          vertexKey={props.vertexKey}
          editing={editor.editing}
          onBeginEdit={editor.beginEdit}
        />
      ) : null}

      {editor.state.loadStatus === "ready" &&
      editor.state.vertex &&
      !editor.editing ? (
        <ReadView
          vertex={editor.state.vertex}
          onEdit={editor.beginEdit}
          onDelete={editor.openDeleteDialog}
        />
      ) : null}

      {editor.editing ? <EditView editor={editor} /> : null}

      <DeleteVertexDialog
        open={editor.state.deleteRequested}
        vertexKey={props.vertexKey}
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

interface ReadViewProps {
  vertex: Vertex;
  onEdit: () => void;
  onDelete: () => void;
}

function ReadView({ vertex, onEdit, onDelete }: ReadViewProps) {
  const kind = kindOfVertex(vertex);
  return (
    <section className={styles.card} data-testid="vertex-detail-read">
      <h2 className={styles.cardTitle}>Current value</h2>
      <div className={styles.viewRow}>
        <span className={styles.viewLabel}>Kind</span>
        <Badge appearance="tint" shape="rounded" className={styles.viewKind}>
          {kind}
        </Badge>
      </div>
      <div className={styles.viewRow}>
        <span className={styles.viewLabel}>Value</span>
        {typeof vertex.string === "string" ? (
          <StringValueView value={vertex.string} />
        ) : (
          <span className={styles.viewValue}>
            <ValueCell vertex={vertex} />
          </span>
        )}
      </div>
      <div className={styles.viewRow}>
        <span className={styles.viewLabel}>Expires</span>
        <span className={styles.viewValue}>
          <ExpirationCell expiration={vertex.expiration} />
        </span>
      </div>
      <div className={styles.formActions}>
        <Button
          appearance="secondary"
          icon={<Delete20Regular />}
          onClick={onDelete}
          data-testid="vertex-delete-trigger"
        >
          Delete
        </Button>
        <Button
          appearance="primary"
          icon={<Edit20Regular />}
          onClick={onEdit}
          data-testid="vertex-edit-trigger"
        >
          Edit value
        </Button>
      </div>
    </section>
  );
}

interface NotFoundViewProps {
  vertexKey: string;
  editing: boolean;
  onBeginEdit: () => void;
}

function NotFoundView({ vertexKey, editing, onBeginEdit }: NotFoundViewProps) {
  if (editing) return null;
  return (
    <section className={styles.card} data-testid="vertex-detail-missing">
      <h2 className={styles.cardTitle}>Vertex not found</h2>
      <p>
        No vertex currently exists for key <code>{vertexKey}</code>. You can
        create one by writing a value below.
      </p>
      <div className={styles.formActions}>
        <Button
          appearance="primary"
          onClick={onBeginEdit}
          data-testid="vertex-create-trigger"
        >
          Create vertex
        </Button>
      </div>
    </section>
  );
}

interface EditViewProps {
  editor: ReturnType<typeof useEditVertex>;
}

function EditView({ editor }: EditViewProps) {
  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    await editor.save();
  };
  return (
    <form
      onSubmit={handleSubmit}
      className={styles.card}
      data-testid="vertex-detail-edit"
    >
      <h2 className={styles.cardTitle}>
        {editor.state.loadStatus === "not-found"
          ? "Create vertex"
          : "Edit vertex"}
      </h2>
      <KindSelector value={editor.state.kind} onChange={editor.setKind} />
      <ValueEditor
        kind={editor.state.kind}
        inputs={editor.state.inputs}
        onTextInput={editor.setInput}
        onBoolChange={editor.setBool}
        onBytesEncodingChange={editor.setBytesEncoding}
      />
      <TtlField value={editor.state.ttl} onChange={editor.setTtl} />
      {editor.state.saveStatus === "error" ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>
            {editor.state.saveError ?? "Save failed."}
          </MessageBarBody>
        </MessageBar>
      ) : null}
      <div className={styles.formActions}>
        <Button
          appearance="secondary"
          onClick={editor.cancelEdit}
          disabled={editor.state.saveStatus === "saving"}
          type="button"
        >
          Cancel
        </Button>
        <Button
          appearance="primary"
          type="submit"
          icon={
            editor.state.saveStatus === "saving" ? (
              <Spinner size="tiny" />
            ) : (
              <Save20Regular />
            )
          }
          disabled={!editor.formValid || editor.state.saveStatus === "saving"}
          data-testid="vertex-save"
        >
          Save
        </Button>
      </div>
    </form>
  );
}
