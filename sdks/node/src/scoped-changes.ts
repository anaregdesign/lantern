/** Public scope-bound CDC. Cursors are opaque; no per-origin state is inferred. */
import { toJson, toBinary } from "@bufbuild/protobuf";
import { InvalidArgumentError, LanternError } from "./errors.js";
import { VertexSchema, EdgeSchema } from "./gen/graph/v1/graph_pb.js";
import {
  WatchChangesResponseSchema,
  type WatchChangesResponse,
} from "./gen/graph/v1/changes_pb.js";
import { fromVertexJson, fromEdgeJson, type Vertex, type Edge } from "./values.js";

export class ChangeCursor {
  private readonly value: Uint8Array;
  constructor(bytes: Uint8Array) {
    if (!(bytes instanceof Uint8Array) || !bytes.length || bytes.length > 8192)
      throw new InvalidArgumentError("invalid opaque change cursor");
    this.value = bytes.slice();
  }
  toBytes(): Uint8Array {
    return this.value.slice();
  }
}
export interface WatchChangesOptions {
  prefix?: string;
  projection?: "identity" | "value";
  bootstrap?: boolean;
  cursor?: ChangeCursor;
  timeoutMs?: number;
}
export type ChangeInvalidation =
  | { kind: "vertex"; key: string; current?: Vertex }
  | { kind: "edge"; tail: string; head: string; current?: Edge };
export interface ChangeFrame {
  invalidations: readonly ChangeInvalidation[];
  /** Save only after applying every invalidation in this frame. */
  cursor?: ChangeCursor;
  bootstrap: boolean;
}
function changeHasUnknown(value: unknown): boolean {
  if (!value || typeof value !== "object" || value instanceof Uint8Array) return false;
  if (Array.isArray(value)) return value.some(changeHasUnknown);
  const object = value as Record<string, unknown>;
  if (Array.isArray(object.$unknown) && object.$unknown.length) return true;
  return Object.values(object).some(changeHasUnknown);
}
export function decodeChangeFrame(
  frame: WatchChangesResponse,
  projection: "identity" | "value",
): ChangeFrame {
  if (
    changeHasUnknown(frame) ||
    toBinary(WatchChangesResponseSchema, frame).byteLength > 1 << 20 ||
    frame.invalidations.length > 1024 ||
    frame.cursor.length > 8192 ||
    (!frame.invalidations.length && !frame.cursor.length) ||
    (frame.bootstrap && (frame.invalidations.length !== 0 || !frame.cursor.length))
  )
    throw new LanternError("invalid public change frame");
  const invalidations: ChangeInvalidation[] = frame.invalidations.map((item) => {
    if (
      item.$unknown?.length ||
      (projection === "identity" && item.currentImage.case !== undefined)
    )
      throw new LanternError("identity CDC cannot contain a value");
    switch (item.identity.case) {
      case "vertexKey": {
        const key = item.identity.value;
        if (!key || (item.currentImage.case !== undefined && item.currentImage.case !== "vertex"))
          throw new LanternError("invalid vertex invalidation");
        const image = item.currentImage.case === "vertex" ? item.currentImage.value : undefined;
        if (image && image.key !== key)
          throw new LanternError("change image differs from committed identity");
        return {
          kind: "vertex",
          key,
          current: image
            ? fromVertexJson(toJson(VertexSchema, image) as Record<string, unknown>)
            : undefined,
        };
      }
      case "edgeKey": {
        const { tail, head } = item.identity.value;
        if (
          !tail ||
          !head ||
          (item.currentImage.case !== undefined && item.currentImage.case !== "edge")
        )
          throw new LanternError("invalid edge invalidation");
        const image = item.currentImage.case === "edge" ? item.currentImage.value : undefined;
        if (image && (image.tail !== tail || image.head !== head))
          throw new LanternError("change image differs from committed identity");
        return {
          kind: "edge",
          tail,
          head,
          current: image
            ? fromEdgeJson(toJson(EdgeSchema, image) as Record<string, unknown>)
            : undefined,
        };
      }
      default:
        throw new LanternError("unknown public change identity");
    }
  });
  return {
    invalidations,
    cursor: frame.cursor.length ? new ChangeCursor(frame.cursor) : undefined,
    bootstrap: frame.bootstrap,
  };
}
