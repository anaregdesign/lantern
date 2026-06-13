import {
  Badge,
  Button,
  Field,
  Input,
  Label,
  Radio,
  RadioGroup,
  Slider,
  Switch,
  Tooltip,
} from "@fluentui/react-components";
import {
  Add20Regular,
  ArrowClockwise20Regular,
  Delete20Regular,
} from "@fluentui/react-icons";
import { useCallback, useState } from "react";
import { VertexPicker } from "~/components/shared/VertexPicker/VertexPicker";
import { ExpansionChipStrip } from "../ExpansionChipStrip/ExpansionChipStrip";
import type {
  IlluminateControls,
  IlluminateStatus,
} from "~/lib/client/usecase/illuminate/state";
import type {
  Algorithm,
  Objective,
} from "~/lib/client/infrastructure/api/illuminate";
import type { ExpansionChip } from "~/lib/client/usecase/illuminate/selectors";
import styles from "./IlluminateToolbar.module.css";

export interface IlluminateToolbarProps {
  initialSeed: string;
  controls: IlluminateControls;
  status: IlluminateStatus;
  canClear: boolean;
  vertexCount: number;
  edgeCount: number;
  expansionCount: number;
  /**
   * Per-expansion lineage chips (#456). The strip replaces the legacy
   * `<code>` seed echo: the first chip carries the seed marker so the
   * URL-level seed is still surfaced at a glance.
   */
  expansionChips: ExpansionChip[];
  onControlsChange: (next: IlluminateControls) => void;
  onClear: () => void;
  onRefresh: () => void;
  /**
   * Fired when the user clicks one of the lineage chips (#456). The
   * handler is pure UI — pan the canvas camera to the chip's origin
   * vertex; no state mutation, no re-fetch.
   */
  onChipClick: (originKey: string) => void;
  /**
   * Fired when the user commits a key in the inline “Expand from key…”
   * picker (#457). Under the additive model (#466) this dispatches a fresh
   * expansion (`ill.expand`) rather than replacing the URL-level seed, so
   * the operator can grow the accumulator from an arbitrary key without
   * navigating back to the SeedPrompt.
   */
  onExpandFromKey: (key: string) => void;
}

// Per #410 the post-traversal reduction is the orthogonal triple
// algorithm × objective × weighting. The toolbar surfaces each as its
// own control so an operator can independently change one axis without
// re-learning combo names.
const ALGORITHMS: Array<{ value: Algorithm; label: string }> = [
  { value: "ALGORITHM_UNSPECIFIED", label: "None (raw subgraph)" },
  { value: "ALGORITHM_MINIMUM_SPANNING_TREE", label: "Spanning tree" },
  { value: "ALGORITHM_SHORTEST_PATH_TREE", label: "Shortest-path tree" },
];

const OBJECTIVES: Array<{ value: Objective; label: string }> = [
  { value: "OBJECTIVE_MINIMIZE", label: "Minimize (cost)" },
  { value: "OBJECTIVE_MAXIMIZE", label: "Maximize (relevance)" },
];

/**
 * Controlled toolbar above the Illuminate canvas. Owns no async state of
 * its own — every change fans out through `onControlsChange` so the
 * usecase hook stays the single source of truth.
 *
 * Per #466 the additive model means there is no seed stack to pop — the
 * old `Pop` button is replaced with `Clear` which empties the
 * accumulator (and navigates back to the SeedPrompt).
 */
export function IlluminateToolbar({
  initialSeed,
  controls,
  status,
  canClear,
  vertexCount,
  edgeCount,
  expansionCount,
  expansionChips,
  onControlsChange,
  onClear,
  onRefresh,
  onChipClick,
  onExpandFromKey,
}: IlluminateToolbarProps) {
  const [expandOpen, setExpandOpen] = useState(false);
  const [expandValue, setExpandValue] = useState("");

  const commitExpand = useCallback(
    (key: string) => {
      const next = key.trim();
      if (next === "") return;
      onExpandFromKey(next);
      setExpandValue("");
      setExpandOpen(false);
    },
    [onExpandFromKey],
  );

  const update = <K extends keyof IlluminateControls>(
    key: K,
    value: IlluminateControls[K],
  ) => {
    onControlsChange({ ...controls, [key]: value });
  };

  return (
    <section
      className={styles.toolbar}
      aria-label="Illuminate controls"
      data-testid="illuminate-toolbar"
    >
      <div className={styles.seedRow}>
        <Label className={styles.seedLabel}>Lineage</Label>
        <div className={styles.chipStripSlot}>
          <ExpansionChipStrip
            chips={expansionChips}
            onChipClick={onChipClick}
          />
        </div>
        <Badge
          appearance="tint"
          color="informative"
          data-testid="illuminate-counter"
        >
          {vertexCount} vertices · {edgeCount} edges · {expansionCount}{" "}
          expansions
        </Badge>
        <Button
          appearance="subtle"
          size="small"
          icon={<Add20Regular />}
          onClick={() => setExpandOpen((open) => !open)}
          aria-expanded={expandOpen}
          data-testid="illuminate-expand-toggle"
        >
          Expand from key…
        </Button>
        <Tooltip
          content="Re-fire the most recent expansion with current controls"
          relationship="label"
          withArrow
        >
          <Button
            appearance="subtle"
            size="small"
            icon={<ArrowClockwise20Regular />}
            disabled={status === "loading" || initialSeed === ""}
            onClick={onRefresh}
            data-testid="illuminate-refresh"
          >
            Refresh
          </Button>
        </Tooltip>
        <Tooltip
          content="Empty accumulator and return to seed prompt"
          relationship="label"
          withArrow
        >
          <Button
            appearance="subtle"
            size="small"
            icon={<Delete20Regular />}
            disabled={!canClear}
            onClick={onClear}
            data-testid="illuminate-clear"
          >
            Clear
          </Button>
        </Tooltip>
        <StatusBadge status={status} />
      </div>

      {expandOpen ? (
        <div className={styles.expandRow} data-testid="illuminate-expand-row">
          <div className={styles.expandPickerSlot}>
            <VertexPicker
              value={expandValue}
              onValueChange={setExpandValue}
              onSelect={commitExpand}
              label="Expand from key"
              placeholder="Type a vertex key…"
              autoFocus
              inputTestId="illuminate-expand-input"
              captionTestId="illuminate-expand-matches"
            />
          </div>
          <Button
            appearance="primary"
            size="small"
            disabled={expandValue.trim() === ""}
            onClick={() => commitExpand(expandValue)}
            data-testid="illuminate-expand-submit"
          >
            Expand
          </Button>
          <Button
            appearance="subtle"
            size="small"
            onClick={() => {
              setExpandOpen(false);
              setExpandValue("");
            }}
            data-testid="illuminate-expand-cancel"
          >
            Cancel
          </Button>
        </div>
      ) : null}

      <div className={styles.knobs}>
        <Field
          label={`Step (${controls.step})`}
          className={styles.knobField}
          orientation="horizontal"
        >
          <Slider
            min={1}
            max={5}
            step={1}
            value={controls.step}
            onChange={(_, data) => update("step", data.value)}
            data-testid="illuminate-step"
          />
        </Field>
        <Field
          label={`K (${controls.k})`}
          className={styles.knobField}
          orientation="horizontal"
        >
          <Slider
            min={1}
            max={32}
            step={1}
            value={controls.k}
            onChange={(_, data) => update("k", data.value)}
            data-testid="illuminate-k"
          />
        </Field>
        <Switch
          label="TF-IDF reweight"
          checked={controls.weighting === "WEIGHTING_TFIDF"}
          onChange={(_, data) =>
            update(
              "weighting",
              data.checked ? "WEIGHTING_TFIDF" : "WEIGHTING_RAW",
            )
          }
          data-testid="illuminate-tfidf"
        />
      </div>

      <Field label="Algorithm" className={styles.optimization}>
        <RadioGroup
          value={controls.algorithm}
          onChange={(_, data) => update("algorithm", data.value as Algorithm)}
          layout="horizontal-stacked"
        >
          {ALGORITHMS.map((opt) => (
            <Radio
              key={opt.value}
              value={opt.value}
              label={opt.label}
              data-testid={`illuminate-algorithm-${opt.value}`}
            />
          ))}
        </RadioGroup>
      </Field>

      <Field label="Objective" className={styles.optimization}>
        <RadioGroup
          value={
            controls.objective === "OBJECTIVE_UNSPECIFIED"
              ? "OBJECTIVE_MAXIMIZE"
              : controls.objective
          }
          onChange={(_, data) => update("objective", data.value as Objective)}
          layout="horizontal-stacked"
          // Per #560 the objective governs BOTH the per-hop top-k pruning
          // (always, even when algorithm = none) and the post-traversal
          // reduction, so it is always meaningful — never disabled. The
          // UNSPECIFIED fallback mirrors the server's MAXIMIZE default.
        >
          {OBJECTIVES.map((opt) => (
            <Radio
              key={opt.value}
              value={opt.value}
              label={opt.label}
              data-testid={`illuminate-objective-${opt.value}`}
            />
          ))}
        </RadioGroup>
      </Field>

      <Field
        label="Vertex prefix"
        className={styles.optimization}
        // Per #606 the prefix restricts the traversal frontier to keys with
        // this prefix; the seed is always kept as the anchor. Empty = no
        // filter. The value is matched verbatim (case-sensitive).
        hint="Restrict the frontier to keys with this prefix. Empty = all keys; the seed is always kept."
      >
        <Input
          value={controls.vertexPrefix}
          onChange={(_, data) => update("vertexPrefix", data.value)}
          placeholder="all keys (e.g. users/)"
          data-testid="illuminate-prefix"
        />
      </Field>
    </section>
  );
}

function StatusBadge({ status }: { status: IlluminateStatus }) {
  if (status === "loading") {
    return (
      <Badge
        appearance="tint"
        color="informative"
        data-testid="illuminate-status"
      >
        Loading…
      </Badge>
    );
  }
  if (status === "error") {
    return (
      <Badge appearance="tint" color="danger" data-testid="illuminate-status">
        Error
      </Badge>
    );
  }
  if (status === "ready") {
    return (
      <Badge appearance="tint" color="success" data-testid="illuminate-status">
        Ready
      </Badge>
    );
  }
  return (
    <Badge appearance="tint" color="subtle" data-testid="illuminate-status">
      Idle
    </Badge>
  );
}
