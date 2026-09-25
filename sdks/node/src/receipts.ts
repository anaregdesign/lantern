import {
  MutationReceiptState as PbMutationReceiptState,
  type GetReceiptCapabilityResponse as PbGetReceiptCapabilityResponse,
  type ReceiptStatus as PbReceiptStatus,
} from "./gen/graph/v1/graph_pb.js";
import { InvalidArgumentError, LanternError } from "./errors.js";

export const RECEIPT_OPERATION_ID_BYTES = 49;
export const RECEIPT_GROUP_ID_BYTES = 16;
export const RECEIPT_EPOCH_BYTES = 16;
export const RECEIPT_POLICY_FINGERPRINT_BYTES = 32;
export const RECEIPT_NODE_ID_BYTES = 16;
export const RECEIPT_GENERATION_BYTES = 16;
export const RECEIPT_STATUS_MAX_ITEMS = 10_000;

const RECEIPT_OPERATION_ID_VERSION = 1;
const RECEIPT_OPERATION_RANDOM_BYTES = 24;
const RECEIPT_INTENT_DIGEST_BYTES = 32;
const MAX_SIGNED_INT64 = (1n << 63n) - 1n;
const MAX_UINT32 = 0xffff_ffff;

declare const operationIDBrand: unique symbol;
declare const groupIDBrand: unique symbol;
declare const deploymentEpochBrand: unique symbol;
declare const policyFingerprintBrand: unique symbol;
declare const nodeIDBrand: unique symbol;
declare const generationBrand: unique symbol;
declare const intentDigestBrand: unique symbol;

/** Canonical lowercase-hex form of one validated 49-byte receipt operation ID. */
export type OperationID = string & { readonly [operationIDBrand]: true };

/** Canonical lowercase-hex form of one validated 16-byte logical-call group ID. */
export type GroupID = string & { readonly [groupIDBrand]: true };

/** Canonical lowercase-hex form of one validated deployment epoch. */
export type ReceiptDeploymentEpoch = string & { readonly [deploymentEpochBrand]: true };

/** Canonical lowercase-hex form of one validated receipt-policy fingerprint. */
export type ReceiptPolicyFingerprint = string & { readonly [policyFingerprintBrand]: true };

/** Canonical lowercase-hex form of one validated serving NodeID. */
export type ReceiptNodeID = string & { readonly [nodeIDBrand]: true };

/** Canonical lowercase-hex form of one validated serving-generation marker. */
export type ReceiptGeneration = string & { readonly [generationBrand]: true };

/** Canonical lowercase-hex form of one server-authored receipt intent digest. */
export type ReceiptIntentDigest = string & { readonly [intentDigestBrand]: true };

export interface ReceiptEndpointContinuity {
  readonly deploymentEpoch: ReceiptDeploymentEpoch;
  readonly policyFingerprint: ReceiptPolicyFingerprint;
  readonly nodeId: ReceiptNodeID;
  readonly generation: ReceiptGeneration;
}

export interface EnabledReceiptCapability {
  readonly enabled: true;
  readonly continuity: ReceiptEndpointContinuity;
  readonly retentionMs: bigint;
  readonly maxEntries: bigint;
  readonly maxBytes: bigint;
  readonly serverNowUnixMs: bigint;
}

export interface DisabledReceiptCapability {
  readonly enabled: false;
}

/** Server-advertised receipt support. Disabled capability carries no identity. */
export type ReceiptCapability = EnabledReceiptCapability | DisabledReceiptCapability;

/**
 * Complete reusable wire identity for one logical mutation call.
 *
 * The value is JSON-persistable as-is: every byte identity is canonical
 * lowercase hex and every container is immutable. Persist it before the
 * first mutation send, then restore it with {@link parseReceiptOperationContext}.
 */
export interface ReceiptOperationContext {
  readonly operationIds: readonly OperationID[];
  readonly groupId: GroupID;
  readonly continuity: ReceiptEndpointContinuity;
}

/** Injectable entropy seam; production callers should use the default Web Crypto source. */
export type ReceiptRandomSource = (target: Uint8Array) => void;

export interface ReceiptEdgeRef {
  readonly tail: string;
  readonly head: string;
}

export interface EdgeDeleteReceiptResult extends ReceiptEdgeRef {
  readonly operationId: OperationID;
  /** The original server result, not the edge's current state. */
  readonly existed: boolean;
}

export interface EdgeDeleteReceiptBatchResult {
  readonly context: ReceiptOperationContext;
  readonly deleted: number;
  readonly results: readonly EdgeDeleteReceiptResult[];
}

export interface ConfirmedEdgeDeleteReceipt {
  readonly operationId: OperationID;
  readonly groupId: GroupID;
  readonly itemIndex: number;
  readonly itemCount: number;
  readonly intentSha256: ReceiptIntentDigest;
  readonly deadlineUnixMs: bigint;
  readonly originalResult: {
    readonly kind: "deleteEdge";
    readonly existed: boolean;
  };
}

export type ReceiptStatus =
  | {
      readonly state: "confirmed";
      readonly operationId: OperationID;
      readonly receipt: ConfirmedEdgeDeleteReceipt;
    }
  | {
      readonly state: "notYetObserved";
      readonly operationId: OperationID;
    }
  | {
      readonly state: "noLongerProvable";
      readonly operationId: OperationID;
    };

export type ReceiptReconciliationReason =
  | "capabilityUnavailable"
  | "capabilityDisabled"
  | "deploymentEpochChanged"
  | "policyChanged"
  | "nodeChanged"
  | "generationChanged"
  | "continuityRejected"
  | "mutationOutcomeUnknown";

function bytesToHex(bytes: Uint8Array): string {
  let hex = "";
  for (const value of bytes) {
    hex += value.toString(16).padStart(2, "0");
  }
  return hex;
}

function hexToBytes(name: string, value: unknown, byteLength: number): Uint8Array {
  if (typeof value !== "string" || !/^[0-9a-fA-F]+$/.test(value)) {
    throw new InvalidArgumentError(`${name} must be a hexadecimal string`);
  }
  if (value.length !== byteLength * 2) {
    throw new InvalidArgumentError(
      `${name} must encode exactly ${byteLength} bytes, got ${value.length / 2}`,
    );
  }
  const bytes = new Uint8Array(byteLength);
  for (let i = 0; i < byteLength; i++) {
    bytes[i] = Number.parseInt(value.slice(i * 2, i * 2 + 2), 16);
  }
  return bytes;
}

function hasNonzeroByte(bytes: Uint8Array): boolean {
  return bytes.some((value) => value !== 0);
}

function requireNonzero(name: string, bytes: Uint8Array): void {
  if (!hasNonzeroByte(bytes)) {
    throw new InvalidArgumentError(`${name} must be nonzero`);
  }
}

function operationIDFromValidatedBytes(bytes: Uint8Array): OperationID {
  if (bytes[0] !== RECEIPT_OPERATION_ID_VERSION) {
    throw new InvalidArgumentError(`operationId uses unsupported version ${bytes[0] ?? "missing"}`);
  }
  requireNonzero("operationId deployment epoch", bytes.subarray(1, 17));
  requireNonzero("operationId randomness", bytes.subarray(25));
  const issuedAt = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getBigUint64(
    17,
    false,
  );
  if (issuedAt > MAX_SIGNED_INT64) {
    throw new InvalidArgumentError("operationId issuance time exceeds signed 64-bit range");
  }
  return bytesToHex(bytes) as OperationID;
}

/** Parse and canonicalize one persisted operation ID. */
export function parseOperationID(value: unknown): OperationID {
  return operationIDFromValidatedBytes(
    hexToBytes("operationId", value, RECEIPT_OPERATION_ID_BYTES),
  );
}

export function operationIDToBytes(value: OperationID): Uint8Array {
  const operationId = parseOperationID(value);
  return hexToBytes("operationId", operationId, RECEIPT_OPERATION_ID_BYTES);
}

/** Parse and canonicalize one persisted logical-call group ID. */
export function parseGroupID(value: unknown): GroupID {
  const bytes = hexToBytes("groupId", value, RECEIPT_GROUP_ID_BYTES);
  requireNonzero("groupId", bytes);
  return bytesToHex(bytes) as GroupID;
}

function parseDeploymentEpoch(value: unknown): ReceiptDeploymentEpoch {
  const bytes = hexToBytes("deploymentEpoch", value, RECEIPT_EPOCH_BYTES);
  requireNonzero("deploymentEpoch", bytes);
  return bytesToHex(bytes) as ReceiptDeploymentEpoch;
}

function parsePolicyFingerprint(value: unknown): ReceiptPolicyFingerprint {
  const bytes = hexToBytes("policyFingerprint", value, RECEIPT_POLICY_FINGERPRINT_BYTES);
  requireNonzero("policyFingerprint", bytes);
  return bytesToHex(bytes) as ReceiptPolicyFingerprint;
}

function parseNodeID(value: unknown): ReceiptNodeID {
  const bytes = hexToBytes("nodeId", value, RECEIPT_NODE_ID_BYTES);
  requireNonzero("nodeId", bytes);
  return bytesToHex(bytes) as ReceiptNodeID;
}

function parseGeneration(value: unknown): ReceiptGeneration {
  const bytes = hexToBytes("generation", value, RECEIPT_GENERATION_BYTES);
  requireNonzero("generation", bytes);
  return bytesToHex(bytes) as ReceiptGeneration;
}

function parseContinuity(value: unknown): ReceiptEndpointContinuity {
  if (typeof value !== "object" || value === null) {
    throw new InvalidArgumentError("receipt continuity must be an object");
  }
  const raw = value as Record<string, unknown>;
  return Object.freeze({
    deploymentEpoch: parseDeploymentEpoch(raw.deploymentEpoch),
    policyFingerprint: parsePolicyFingerprint(raw.policyFingerprint),
    nodeId: parseNodeID(raw.nodeId),
    generation: parseGeneration(raw.generation),
  });
}

/**
 * Restore and validate a JSON-decoded receipt context before reuse.
 *
 * Validation includes ID version/length/nonzero entropy, one nonzero group,
 * unique operation IDs, endpoint marker lengths, and one deployment epoch
 * shared by every operation ID and the continuity record.
 */
export function parseReceiptOperationContext(value: unknown): ReceiptOperationContext {
  if (typeof value !== "object" || value === null) {
    throw new InvalidArgumentError("receipt operation context must be an object");
  }
  const raw = value as Record<string, unknown>;
  if (!Array.isArray(raw.operationIds) || raw.operationIds.length === 0) {
    throw new InvalidArgumentError("receipt operationIds must be a nonempty array");
  }
  if (raw.operationIds.length > MAX_UINT32) {
    throw new InvalidArgumentError(`receipt operationIds exceeds ${MAX_UINT32} items`);
  }
  const continuity = parseContinuity(raw.continuity);
  const operationIds = raw.operationIds.map(parseOperationID);
  const unique = new Set<OperationID>();
  for (let index = 0; index < operationIds.length; index++) {
    const operationId = operationIds[index]!;
    if (unique.has(operationId)) {
      throw new InvalidArgumentError(`receipt operationIds[${index}] duplicates an earlier item`);
    }
    unique.add(operationId);
    const encodedEpoch = operationId.slice(2, 2 + RECEIPT_EPOCH_BYTES * 2);
    if (encodedEpoch !== continuity.deploymentEpoch) {
      throw new InvalidArgumentError(
        `receipt operationIds[${index}] belongs to a different deployment epoch`,
      );
    }
  }
  return Object.freeze({
    operationIds: Object.freeze(operationIds),
    groupId: parseGroupID(raw.groupId),
    continuity,
  });
}

function cryptoRandomSource(target: Uint8Array): void {
  const cryptoObject = globalThis.crypto;
  if (!cryptoObject?.getRandomValues) {
    throw new LanternError("Web Crypto getRandomValues is unavailable");
  }
  cryptoObject.getRandomValues(target);
}

function randomNonzero(
  name: string,
  byteLength: number,
  randomSource: ReceiptRandomSource,
): Uint8Array {
  const bytes = new Uint8Array(byteLength);
  randomSource(bytes);
  if (!hasNonzeroByte(bytes)) {
    throw new InvalidArgumentError(`${name} random source returned an all-zero value`);
  }
  return bytes;
}

function normalizeEnabledCapability(
  capability: EnabledReceiptCapability,
): EnabledReceiptCapability {
  if (capability?.enabled !== true) {
    throw new InvalidArgumentError("an enabled receipt capability is required");
  }
  if (
    typeof capability.retentionMs !== "bigint" ||
    capability.retentionMs <= 0n ||
    typeof capability.maxEntries !== "bigint" ||
    capability.maxEntries <= 0n ||
    typeof capability.maxBytes !== "bigint" ||
    capability.maxBytes <= 0n
  ) {
    throw new InvalidArgumentError("receipt capability policy limits must be positive");
  }
  if (
    typeof capability.serverNowUnixMs !== "bigint" ||
    capability.serverNowUnixMs < 0n ||
    capability.serverNowUnixMs > MAX_SIGNED_INT64
  ) {
    throw new InvalidArgumentError(
      "receipt capability serverNowUnixMs must fit a nonnegative signed 64-bit value",
    );
  }
  return Object.freeze({
    enabled: true,
    continuity: parseContinuity(capability.continuity),
    retentionMs: capability.retentionMs,
    maxEntries: capability.maxEntries,
    maxBytes: capability.maxBytes,
    serverNowUnixMs: capability.serverNowUnixMs,
  });
}

/**
 * Mint one persisted logical-call context from an enabled capability.
 *
 * All item IDs share the capability epoch and server clock sample. The
 * injected source is called once for the GroupID and once per operation ID;
 * omit it in production to use `globalThis.crypto.getRandomValues`.
 */
export function mintReceiptOperationContext(
  capability: EnabledReceiptCapability,
  itemCount: number,
  randomSource: ReceiptRandomSource = cryptoRandomSource,
): ReceiptOperationContext {
  const normalized = normalizeEnabledCapability(capability);
  if (!Number.isSafeInteger(itemCount) || itemCount <= 0 || itemCount > MAX_UINT32) {
    throw new InvalidArgumentError(`receipt itemCount must be an integer in [1, ${MAX_UINT32}]`);
  }
  const groupId = bytesToHex(
    randomNonzero("groupId", RECEIPT_GROUP_ID_BYTES, randomSource),
  ) as GroupID;
  const epoch = hexToBytes(
    "deploymentEpoch",
    normalized.continuity.deploymentEpoch,
    RECEIPT_EPOCH_BYTES,
  );
  const operationIds: OperationID[] = [];
  const unique = new Set<OperationID>();
  for (let index = 0; index < itemCount; index++) {
    const bytes = new Uint8Array(RECEIPT_OPERATION_ID_BYTES);
    bytes[0] = RECEIPT_OPERATION_ID_VERSION;
    bytes.set(epoch, 1);
    new DataView(bytes.buffer).setBigUint64(17, normalized.serverNowUnixMs, false);
    bytes.set(
      randomNonzero(`operationIds[${index}]`, RECEIPT_OPERATION_RANDOM_BYTES, randomSource),
      25,
    );
    const operationId = operationIDFromValidatedBytes(bytes);
    if (unique.has(operationId)) {
      throw new InvalidArgumentError(
        `operationIds[${index}] random source duplicated an earlier item`,
      );
    }
    unique.add(operationId);
    operationIds.push(operationId);
  }
  return Object.freeze({
    operationIds: Object.freeze(operationIds),
    groupId,
    continuity: normalized.continuity,
  });
}

function wireBytes(
  name: string,
  value: Uint8Array,
  byteLength: number,
  nonzero = true,
): Uint8Array {
  if (!(value instanceof Uint8Array) || value.length !== byteLength) {
    throw new LanternError(
      `server returned ${name} with length ${value?.length ?? "missing"}; want ${byteLength}`,
    );
  }
  if (nonzero && !hasNonzeroByte(value)) {
    throw new LanternError(`server returned an all-zero ${name}`);
  }
  return new Uint8Array(value);
}

function operationIDFromWire(value: Uint8Array): OperationID {
  const bytes = wireBytes("operationId", value, RECEIPT_OPERATION_ID_BYTES);
  try {
    return operationIDFromValidatedBytes(bytes);
  } catch (error) {
    throw new LanternError("server returned a malformed operationId", { cause: error });
  }
}

function groupIDFromWire(value: Uint8Array): GroupID {
  return bytesToHex(wireBytes("groupId", value, RECEIPT_GROUP_ID_BYTES)) as GroupID;
}

/** Decode and strictly validate the capability wire response. */
export function receiptCapabilityFromWire(
  response: PbGetReceiptCapabilityResponse,
): ReceiptCapability {
  if (!response.enabled) {
    if (
      response.policy !== undefined ||
      response.endpoint !== undefined ||
      response.serverNowUnixMs !== 0n
    ) {
      throw new LanternError("disabled receipt capability carried identity-bearing fields");
    }
    return Object.freeze({ enabled: false });
  }
  if (!response.policy || !response.endpoint) {
    throw new LanternError("enabled receipt capability omitted policy or endpoint");
  }
  if (
    response.policy.retentionMs <= 0n ||
    response.policy.maxEntries <= 0n ||
    response.policy.maxBytes <= 0n ||
    response.serverNowUnixMs > MAX_SIGNED_INT64
  ) {
    throw new LanternError("enabled receipt capability carried invalid policy or clock values");
  }
  return Object.freeze({
    enabled: true,
    continuity: Object.freeze({
      deploymentEpoch: bytesToHex(
        wireBytes("deploymentEpoch", response.policy.deploymentEpoch, RECEIPT_EPOCH_BYTES),
      ) as ReceiptDeploymentEpoch,
      policyFingerprint: bytesToHex(
        wireBytes(
          "policyFingerprint",
          response.policy.fingerprint,
          RECEIPT_POLICY_FINGERPRINT_BYTES,
        ),
      ) as ReceiptPolicyFingerprint,
      nodeId: bytesToHex(
        wireBytes("nodeId", response.endpoint.nodeId, RECEIPT_NODE_ID_BYTES),
      ) as ReceiptNodeID,
      generation: bytesToHex(
        wireBytes("generation", response.endpoint.generation, RECEIPT_GENERATION_BYTES),
      ) as ReceiptGeneration,
    }),
    retentionMs: response.policy.retentionMs,
    maxEntries: response.policy.maxEntries,
    maxBytes: response.policy.maxBytes,
    serverNowUnixMs: response.serverNowUnixMs,
  });
}

export function receiptContextForItemCount(
  value: unknown,
  itemCount: number,
): ReceiptOperationContext {
  const context = parseReceiptOperationContext(value);
  if (context.operationIds.length !== itemCount) {
    throw new InvalidArgumentError(
      `receipt context has ${context.operationIds.length} operation IDs for ${itemCount} items`,
    );
  }
  return context;
}

export function receiptContextToWire(context: ReceiptOperationContext): {
  operationIds: Uint8Array[];
  logicalCallId: Uint8Array;
  endpoint: {
    nodeId: Uint8Array;
    generation: Uint8Array;
  };
} {
  return {
    operationIds: context.operationIds.map((operationId) =>
      hexToBytes("operationId", operationId, RECEIPT_OPERATION_ID_BYTES),
    ),
    logicalCallId: hexToBytes("groupId", context.groupId, RECEIPT_GROUP_ID_BYTES),
    endpoint: {
      nodeId: hexToBytes("nodeId", context.continuity.nodeId, RECEIPT_NODE_ID_BYTES),
      generation: hexToBytes("generation", context.continuity.generation, RECEIPT_GENERATION_BYTES),
    },
  };
}

export function normalizeReceiptEdgeRefs(
  refs: readonly ReceiptEdgeRef[],
): readonly ReceiptEdgeRef[] {
  if (!Array.isArray(refs) || refs.length === 0) {
    throw new InvalidArgumentError("receipt Edge Delete requires at least one edge");
  }
  if (refs.length > MAX_UINT32) {
    throw new InvalidArgumentError(`receipt Edge Delete exceeds ${MAX_UINT32} items`);
  }
  return Object.freeze(
    refs.map((ref, index) => {
      if (
        typeof ref !== "object" ||
        ref === null ||
        typeof ref.tail !== "string" ||
        ref.tail.length === 0 ||
        typeof ref.head !== "string" ||
        ref.head.length === 0
      ) {
        throw new InvalidArgumentError(
          `receipt Edge Delete edge[${index}] requires nonempty tail and head`,
        );
      }
      return Object.freeze({ tail: ref.tail, head: ref.head });
    }),
  );
}

export function receiptContinuityDifference(
  expected: ReceiptEndpointContinuity,
  current: ReceiptEndpointContinuity,
): Exclude<
  ReceiptReconciliationReason,
  "capabilityUnavailable" | "capabilityDisabled" | "mutationOutcomeUnknown"
> | null {
  if (expected.deploymentEpoch !== current.deploymentEpoch) return "deploymentEpochChanged";
  if (expected.policyFingerprint !== current.policyFingerprint) return "policyChanged";
  if (expected.nodeId !== current.nodeId) return "nodeChanged";
  if (expected.generation !== current.generation) return "generationChanged";
  return null;
}

function receiptStatusFromWire(raw: PbReceiptStatus, expected: OperationID): ReceiptStatus {
  const operationId = operationIDFromWire(raw.operationId);
  if (operationId !== expected) {
    throw new LanternError("server returned a receipt status out of request-index alignment");
  }
  switch (raw.state) {
    case PbMutationReceiptState.CONFIRMED: {
      const receipt = raw.receipt;
      if (!receipt) {
        throw new LanternError("confirmed receipt status omitted its receipt");
      }
      if (operationIDFromWire(receipt.operationId) !== expected) {
        throw new LanternError("confirmed receipt operationId does not match its status");
      }
      if (
        receipt.itemCount === 0 ||
        receipt.itemIndex >= receipt.itemCount ||
        receipt.deadlineUnixMs > MAX_SIGNED_INT64
      ) {
        throw new LanternError("confirmed receipt carried invalid item metadata");
      }
      const intentSha256 = bytesToHex(
        wireBytes("intentSha256", receipt.intentSha256, RECEIPT_INTENT_DIGEST_BYTES, false),
      ) as ReceiptIntentDigest;
      if (receipt.originalResult?.result.case !== "deleteEdgeExisted") {
        throw new LanternError("confirmed receipt did not carry an Edge Delete original result");
      }
      const confirmed: ConfirmedEdgeDeleteReceipt = Object.freeze({
        operationId,
        groupId: groupIDFromWire(receipt.logicalCallId),
        itemIndex: receipt.itemIndex,
        itemCount: receipt.itemCount,
        intentSha256,
        deadlineUnixMs: receipt.deadlineUnixMs,
        originalResult: Object.freeze({
          kind: "deleteEdge",
          existed: receipt.originalResult.result.value,
        }),
      });
      return Object.freeze({ state: "confirmed", operationId, receipt: confirmed });
    }
    case PbMutationReceiptState.NOT_YET_OBSERVED:
      if (raw.receipt !== undefined) {
        throw new LanternError("not-yet-observed receipt status unexpectedly carried a receipt");
      }
      return Object.freeze({ state: "notYetObserved", operationId });
    case PbMutationReceiptState.NO_LONGER_PROVABLE:
      if (raw.receipt !== undefined) {
        throw new LanternError("no-longer-provable receipt status unexpectedly carried a receipt");
      }
      return Object.freeze({ state: "noLongerProvable", operationId });
    default:
      throw new LanternError(`server returned unknown receipt status ${raw.state}`);
  }
}

export function receiptStatusesFromWire(
  statuses: readonly PbReceiptStatus[],
  operationIds: readonly OperationID[],
): readonly ReceiptStatus[] {
  if (statuses.length !== operationIds.length) {
    throw new LanternError(
      `server returned ${statuses.length} receipt statuses for ${operationIds.length} operation IDs`,
    );
  }
  return Object.freeze(
    statuses.map((status, index) => receiptStatusFromWire(status, operationIds[index]!)),
  );
}
