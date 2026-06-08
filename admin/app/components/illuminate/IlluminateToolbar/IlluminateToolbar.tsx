import {
  Badge,
  Button,
  Field,
  Label,
  Radio,
  RadioGroup,
  Slider,
  Switch,
  Tooltip,
} from "@fluentui/react-components";
import {
  ArrowClockwise20Regular,
  Delete20Regular,
} from "@fluentui/react-icons";
import type {
  IlluminateControls,
  IlluminateStatus,
} from "~/lib/client/usecase/illuminate/state";
import type {
  Algorithm,
  Objective,
} from "~/lib/client/infrastructure/api/illuminate";
import styles from "./IlluminateToolbar.module.css";

export interface IlluminateToolbarProps {
  initialSeed: string;
  controls: IlluminateControls;
  status: IlluminateStatus;
  canClear: boolean;
  vertexCount: number;
  edgeCount: number;
  expansionCount: number;
  onControlsChange: (next: IlluminateControls) => void;
  onClear: () => void;
  onRefresh: () => void;
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
  onControlsChange,
  onClear,
  onRefresh,
}: IlluminateToolbarProps) {
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
        <Label className={styles.seedLabel}>Seed</Label>
        <Tooltip content={initialSeed} relationship="label" withArrow>
          <code className={styles.seedValue} data-testid="illuminate-seed">
            {initialSeed || "—"}
          </code>
        </Tooltip>
        <Badge
          appearance="tint"
          color="informative"
          data-testid="illuminate-counter"
        >
          {vertexCount} vertices · {edgeCount} edges · {expansionCount}{" "}
          expansions
        </Badge>
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
              ? "OBJECTIVE_MINIMIZE"
              : controls.objective
          }
          onChange={(_, data) => update("objective", data.value as Objective)}
          layout="horizontal-stacked"
          // Objective is only meaningful when an algorithm reduction is
          // selected; disable the radio when algorithm = UNSPECIFIED so the
          // UI reflects the server semantics.
          disabled={controls.algorithm === "ALGORITHM_UNSPECIFIED"}
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
