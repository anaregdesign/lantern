import {
  OperationalGroup,
  OperationalSearchMode,
  validateOperationalValues,
  type OperationalPolicyValues,
} from "lantern-sdk/web";
export type OperationalDraft = Readonly<Record<string, string>>;
export type OperationalField = {
  key: string;
  label: string;
  kind: "integer" | "decimal" | "boolean" | "search-mode";
  max?: string;
};
export type OperationalGroupDefinition = {
  group: OperationalGroup;
  label: string;
  note: string;
  fields: readonly OperationalField[];
};
export const operationalDefinitions: readonly OperationalGroupDefinition[] = [
  {
    group: OperationalGroup.SLOW_RPC,
    label: "Slow RPC diagnostics",
    note: "Update the threshold used for slow RPC diagnostics. Zero disables slow diagnostics.",
    fields: [{ key: "thresholdMs", label: "Threshold (ms)", kind: "integer" }],
  },
  {
    group: OperationalGroup.RATE,
    label: "Request admission",
    note: "RPS and Burst change atomically for each node. Zero RPS and zero Burst disable the limiter. Management access retains its dedicated recovery capacity.",
    fields: [
      { key: "rps", label: "RPS", kind: "decimal" },
      { key: "burst", label: "Burst", kind: "integer" },
    ],
  },
  {
    group: OperationalGroup.REQUEST,
    label: "Request and traversal limits",
    note: "These limits apply to new requests; an in-flight request retains its captured policy.",
    fields: [
      {
        key: "deleteByPrefixDefaultLimit",
        label: "Delete by prefix default limit",
        kind: "integer",
      },
      {
        key: "deleteByPrefixMaxLimit",
        label: "Delete by prefix max limit",
        kind: "integer",
      },
      { key: "illuminateMaxK", label: "Illuminate max k", kind: "integer" },
      {
        key: "illuminateMaxStep",
        label: "Illuminate max step",
        kind: "integer",
      },
      { key: "maxBatchSize", label: "Max batch size", kind: "integer" },
      { key: "maxKeyLen", label: "Max key len", kind: "integer" },
      { key: "scanDefaultLimit", label: "Scan default limit", kind: "integer" },
      { key: "scanMaxLimit", label: "Scan max limit", kind: "integer" },
      {
        key: "traversalMaxPushes",
        label: "Traversal max pushes",
        kind: "integer",
      },
      {
        key: "traversalMaxResults",
        label: "Traversal max results",
        kind: "integer",
      },
      {
        key: "traversalMaxTouchedEdges",
        label: "Traversal max touched edges",
        kind: "integer",
      },
      {
        key: "traversalTimeoutMs",
        label: "Traversal timeout (ms)",
        kind: "integer",
      },
    ],
  },
  {
    group: OperationalGroup.BACKUP,
    label: "Backup production",
    note: "Review and Apply do not delete backups. Future scheduled retention can remove older backups. Retain zero keeps all backups. Interval is stored exactly in nanoseconds.",
    fields: [
      { key: "enabled", label: "Enabled", kind: "boolean" },
      {
        key: "intervalNanos",
        label: "Interval (nanoseconds)",
        kind: "integer",
      },
      { key: "retain", label: "Retain", kind: "integer" },
    ],
  },
  {
    group: OperationalGroup.CAPACITY,
    label: "Graph capacity and garbage collection",
    note: "Capacity reductions cannot evict data to satisfy the new policy. Current graph dependencies are checked by Server.",
    fields: [
      { key: "gcEdgeBudget", label: "GC edge budget", kind: "integer" },
      {
        key: "gcIntervalSeconds",
        label: "GC interval (seconds)",
        kind: "integer",
      },
      { key: "maxEdges", label: "Max edges", kind: "integer" },
      {
        key: "maxEdgeCausalEntries",
        label: "Max edge causal entries",
        kind: "integer",
      },
      {
        key: "maxVertexCausalEntries",
        label: "Max vertex causal entries",
        kind: "integer",
      },
      { key: "maxVertices", label: "Max vertices", kind: "integer" },
    ],
  },
  {
    group: OperationalGroup.LIFECYCLE,
    label: "Lifecycle and readiness",
    note: "New lifecycle and readiness decisions use the committed policy. Readiness changes may remove this node from traffic.",
    fields: [
      {
        key: "drainDelaySeconds",
        label: "Drain delay (seconds)",
        kind: "integer",
      },
      {
        key: "maxReplicationLag",
        label: "Max replication lag",
        kind: "integer",
      },
      {
        key: "shutdownTimeoutSeconds",
        label: "Shutdown timeout (seconds)",
        kind: "integer",
      },
    ],
  },
  {
    group: OperationalGroup.SEARCH,
    label: "Search request policy",
    note: "Application requires the reported restart procedure. A mixed-fingerprint rolling restart is not guaranteed safe. Compatibility and existing cursor behavior are checked by Server.",
    fields: [
      { key: "defaultLimit", label: "Default limit", kind: "integer" },
      { key: "maxLimit", label: "Max limit", kind: "integer" },
      { key: "defaultMode", label: "Default mode", kind: "search-mode" },
      { key: "defaultMinShould", label: "Default min should", kind: "integer" },
      {
        key: "timeoutMs",
        max: "4294967295",
        label: "Timeout (ms)",
        kind: "integer",
      },
      {
        key: "maxQueryBytes",
        max: "4294967295",
        label: "Max query bytes",
        kind: "integer",
      },
      {
        key: "maxQueryTerms",
        max: "4294967295",
        label: "Max query terms",
        kind: "integer",
      },
      {
        key: "maxDictionaryVisits",
        label: "Max dictionary visits",
        kind: "integer",
      },
      { key: "maxPostingVisits", label: "Max posting visits", kind: "integer" },
      {
        key: "maxPositionVisits",
        label: "Max position visits",
        kind: "integer",
      },
      {
        key: "maxExpirationVisits",
        label: "Max expiration visits",
        kind: "integer",
      },
    ],
  },
  {
    group: OperationalGroup.REPLICATION,
    label: "Replication scheduling",
    note: "Resident workers capture new scheduling values at a documented transition. Current replication work is not discarded.",
    fields: [
      {
        key: "antiEntropyGapWarnThreshold",
        label: "Anti entropy gap warn threshold",
        kind: "integer",
      },
      {
        key: "antiEntropyIntervalMs",
        label: "Anti entropy interval (ms)",
        kind: "integer",
      },
      {
        key: "antiEntropySubscribeTimeoutMs",
        label: "Anti entropy subscribe timeout (ms)",
        kind: "integer",
      },
      {
        key: "pumpBackoffMaxMs",
        label: "Pump backoff max (ms)",
        kind: "integer",
      },
      {
        key: "pumpBackoffMinMs",
        label: "Pump backoff min (ms)",
        kind: "integer",
      },
    ],
  },
];
export function operationalDefinition(
  group: OperationalGroup,
): OperationalGroupDefinition {
  const definition = operationalDefinitions.find((d) => d.group === group);
  if (!definition) throw new Error("Unknown operational group.");
  return definition;
}
export function operationalDraft(
  values: OperationalPolicyValues,
): OperationalDraft {
  const group = validateOperationalValues(values),
    definition = operationalDefinition(group);
  const source = values.group.value as unknown as Record<string, unknown>;
  return Object.fromEntries(
    definition.fields.map((field) => [field.key, String(source[field.key])]),
  );
}
function integer(draft: OperationalDraft, key: string, max?: bigint): bigint {
  const value = draft[key];
  if (!value || !/^(0|[1-9][0-9]{0,19})$/.test(value))
    throw new Error(
      "Enter a whole non-negative number for every integer setting.",
    );
  const parsed = BigInt(value);
  if (max !== undefined && parsed > max)
    throw new Error(`Enter a value at most ${max.toString()} for ${key}.`);
  return parsed;
}
function decimal(draft: OperationalDraft, key: string): number {
  const value = draft[key];
  if (
    !value ||
    !/^(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$/.test(value) ||
    !Number.isFinite(Number(value))
  )
    throw new Error("Enter a finite non-negative decimal.");
  return Number(value);
}
function boolean(draft: OperationalDraft, key: string): boolean {
  if (draft[key] !== "true" && draft[key] !== "false")
    throw new Error("Select enabled or disabled.");
  return draft[key] === "true";
}
export function operationalValues(
  group: OperationalGroup,
  draft: OperationalDraft,
): OperationalPolicyValues {
  let values: OperationalPolicyValues;
  switch (group) {
    case OperationalGroup.SLOW_RPC:
      values = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "slowRpc",
          value: {
            $typeName: "graph.v1.OperationalSlowRPCValues",
            thresholdMs: integer(draft, "thresholdMs"),
          },
        },
      };
      break;
    case OperationalGroup.RATE:
      values = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "rate",
          value: {
            $typeName: "graph.v1.OperationalRateValues",
            rps: decimal(draft, "rps"),
            burst: integer(draft, "burst"),
          },
        },
      };
      break;
    case OperationalGroup.REQUEST:
      values = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "request",
          value: {
            $typeName: "graph.v1.OperationalRequestValues",
            deleteByPrefixDefaultLimit: integer(
              draft,
              "deleteByPrefixDefaultLimit",
            ),
            deleteByPrefixMaxLimit: integer(draft, "deleteByPrefixMaxLimit"),
            illuminateMaxK: integer(draft, "illuminateMaxK"),
            illuminateMaxStep: integer(draft, "illuminateMaxStep"),
            maxBatchSize: integer(draft, "maxBatchSize"),
            maxKeyLen: integer(draft, "maxKeyLen"),
            scanDefaultLimit: integer(draft, "scanDefaultLimit"),
            scanMaxLimit: integer(draft, "scanMaxLimit"),
            traversalMaxPushes: integer(draft, "traversalMaxPushes"),
            traversalMaxResults: integer(draft, "traversalMaxResults"),
            traversalMaxTouchedEdges: integer(
              draft,
              "traversalMaxTouchedEdges",
            ),
            traversalTimeoutMs: integer(draft, "traversalTimeoutMs"),
          },
        },
      };
      break;
    case OperationalGroup.BACKUP:
      values = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "backup",
          value: {
            $typeName: "graph.v1.OperationalBackupValues",
            enabled: boolean(draft, "enabled"),
            intervalNanos: integer(draft, "intervalNanos"),
            retain: integer(draft, "retain"),
          },
        },
      };
      break;
    case OperationalGroup.CAPACITY:
      values = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "capacity",
          value: {
            $typeName: "graph.v1.OperationalCapacityValues",
            gcEdgeBudget: integer(draft, "gcEdgeBudget"),
            gcIntervalSeconds: integer(draft, "gcIntervalSeconds"),
            maxEdges: integer(draft, "maxEdges"),
            maxEdgeCausalEntries: integer(draft, "maxEdgeCausalEntries"),
            maxVertexCausalEntries: integer(draft, "maxVertexCausalEntries"),
            maxVertices: integer(draft, "maxVertices"),
          },
        },
      };
      break;
    case OperationalGroup.LIFECYCLE:
      values = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "lifecycle",
          value: {
            $typeName: "graph.v1.OperationalLifecycleValues",
            drainDelaySeconds: integer(draft, "drainDelaySeconds"),
            maxReplicationLag: integer(draft, "maxReplicationLag"),
            shutdownTimeoutSeconds: integer(draft, "shutdownTimeoutSeconds"),
          },
        },
      };
      break;
    case OperationalGroup.SEARCH:
      values = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "search",
          value: {
            $typeName: "graph.v1.OperationalSearchValues",
            defaultLimit: integer(draft, "defaultLimit"),
            maxLimit: integer(draft, "maxLimit"),
            defaultMode: Number(
              integer(draft, "defaultMode"),
            ) as OperationalSearchMode,
            defaultMinShould: integer(draft, "defaultMinShould"),
            timeoutMs: integer(draft, "timeoutMs", 4294967295n),
            maxQueryBytes: integer(draft, "maxQueryBytes", 4294967295n),
            maxQueryTerms: integer(draft, "maxQueryTerms", 4294967295n),
            maxDictionaryVisits: integer(draft, "maxDictionaryVisits"),
            maxPostingVisits: integer(draft, "maxPostingVisits"),
            maxPositionVisits: integer(draft, "maxPositionVisits"),
            maxExpirationVisits: integer(draft, "maxExpirationVisits"),
          },
        },
      };
      break;
    case OperationalGroup.REPLICATION:
      values = {
        $typeName: "graph.v1.OperationalPolicyValues",
        group: {
          case: "replication",
          value: {
            $typeName: "graph.v1.OperationalReplicationValues",
            antiEntropyGapWarnThreshold: integer(
              draft,
              "antiEntropyGapWarnThreshold",
            ),
            antiEntropyIntervalMs: integer(draft, "antiEntropyIntervalMs"),
            antiEntropySubscribeTimeoutMs: integer(
              draft,
              "antiEntropySubscribeTimeoutMs",
            ),
            pumpBackoffMaxMs: integer(draft, "pumpBackoffMaxMs"),
            pumpBackoffMinMs: integer(draft, "pumpBackoffMinMs"),
          },
        },
      };
      break;
    default:
      throw new Error("Unknown operational group.");
  }
  validateOperationalValues(values);
  return values;
}
