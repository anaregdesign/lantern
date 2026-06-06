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
  ArrowUndo20Regular,
} from "@fluentui/react-icons";
import type {
  IlluminateControls,
  IlluminateStatus,
} from "~/lib/client/usecase/illuminate/state";
import type { Optimization } from "~/lib/client/infrastructure/api/illuminate";
import styles from "./IlluminateToolbar.module.css";

export interface IlluminateToolbarProps {
  seed: string;
  controls: IlluminateControls;
  status: IlluminateStatus;
  canPop: boolean;
  onControlsChange: (next: IlluminateControls) => void;
  onPop: () => void;
  onRefresh: () => void;
}

const OPTIMIZATIONS: Array<{ value: Optimization; label: string }> = [
  { value: "OPTIMIZATION_UNSPECIFIED", label: "All edges" },
  { value: "OPTIMIZATION_SHORTEST_PATH_TREE", label: "Shortest path tree" },
  {
    value: "OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE",
    label: "Inverse SPT",
  },
  {
    value: "OPTIMIZATION_MINIMUM_SPANNING_TREE",
    label: "Minimum spanning tree",
  },
  {
    value: "OPTIMIZATION_MAXIMUM_SPANNING_TREE",
    label: "Maximum spanning tree",
  },
];

/**
 * Controlled toolbar above the Illuminate canvas. Owns no async state of
 * its own — every change fans out through `onControlsChange` so the
 * usecase hook stays the single source of truth.
 */
export function IlluminateToolbar({
  seed,
  controls,
  status,
  canPop,
  onControlsChange,
  onPop,
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
        <Tooltip content={seed} relationship="label" withArrow>
          <code className={styles.seedValue} data-testid="illuminate-seed">
            {seed || "—"}
          </code>
        </Tooltip>
        <Button
          appearance="subtle"
          size="small"
          icon={<ArrowUndo20Regular />}
          disabled={!canPop}
          onClick={onPop}
          data-testid="illuminate-pop"
        >
          Pop
        </Button>
        <Button
          appearance="subtle"
          size="small"
          icon={<ArrowClockwise20Regular />}
          disabled={status === "loading" || seed === ""}
          onClick={onRefresh}
          data-testid="illuminate-refresh"
        >
          Refresh
        </Button>
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
          checked={controls.tfidf}
          onChange={(_, data) => update("tfidf", data.checked)}
          data-testid="illuminate-tfidf"
        />
      </div>

      <Field label="Tree selection" className={styles.optimization}>
        <RadioGroup
          value={controls.optimization}
          onChange={(_, data) =>
            update("optimization", data.value as Optimization)
          }
          layout="horizontal-stacked"
        >
          {OPTIMIZATIONS.map((opt) => (
            <Radio
              key={opt.value}
              value={opt.value}
              label={opt.label}
              data-testid={`illuminate-opt-${opt.value}`}
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
