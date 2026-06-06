import {
  Button,
  Dialog,
  DialogActions,
  DialogBody,
  DialogContent,
  DialogSurface,
  DialogTitle,
  Spinner,
} from "@fluentui/react-components";

export interface DeleteVertexDialogProps {
  open: boolean;
  vertexKey: string;
  deleting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}

/**
 * Confirmation Dialog for vertex deletion. The key is shown verbatim so
 * the user has a final chance to spot a typo before the irreversible
 * action.
 */
export function DeleteVertexDialog(props: DeleteVertexDialogProps) {
  return (
    <Dialog
      open={props.open}
      onOpenChange={(_, data) => {
        if (!data.open) props.onCancel();
      }}
    >
      <DialogSurface>
        <DialogBody>
          <DialogTitle>Delete vertex?</DialogTitle>
          <DialogContent>
            <p>
              This permanently removes the vertex with key{" "}
              <code data-testid="delete-vertex-key">{props.vertexKey}</code>.
              Edges that reference this key are not removed automatically.
            </p>
          </DialogContent>
          <DialogActions>
            <Button appearance="secondary" onClick={props.onCancel}>
              Cancel
            </Button>
            <Button
              appearance="primary"
              onClick={props.onConfirm}
              disabled={props.deleting}
              data-testid="confirm-delete-vertex"
              icon={props.deleting ? <Spinner size="tiny" /> : undefined}
            >
              Delete
            </Button>
          </DialogActions>
        </DialogBody>
      </DialogSurface>
    </Dialog>
  );
}
