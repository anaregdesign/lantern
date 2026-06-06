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

export interface DeleteEdgeDialogProps {
  open: boolean;
  tail: string;
  head: string;
  deleting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}

export function DeleteEdgeDialog(props: DeleteEdgeDialogProps) {
  return (
    <Dialog
      open={props.open}
      onOpenChange={(_, data) => {
        if (!data.open) props.onCancel();
      }}
    >
      <DialogSurface>
        <DialogBody>
          <DialogTitle>Delete edge?</DialogTitle>
          <DialogContent>
            <p>
              Permanently removes the directed edge{" "}
              <code data-testid="delete-edge-tail">{props.tail}</code> →{" "}
              <code data-testid="delete-edge-head">{props.head}</code>.
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
              data-testid="confirm-delete-edge"
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
