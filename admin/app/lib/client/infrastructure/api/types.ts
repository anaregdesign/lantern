// Public value-object shapes the admin SPA's usecase layer consumes.
//
// These mirror the JSON-flat shape the protobuf JSON spec defines so
// the usecase layer (browse-vertices, edit-vertex, illuminate, …)
// stays decoupled from the connect-es message classes. The adapter
// layer (`infrastructure/api/*.ts`) marshals between this flat shape
// and the connect-es message classes via the message's `fromJson` /
// `toJson` helpers — those use exactly this shape per the protobuf
// JSON mapping spec, so the marshalling is a direct round trip with
// no field-by-field code.
//
// Why duplicate types instead of consuming the generated classes:
//   1. The connect-es v1 oneof representation
//      (`{ case: "string", value: "..." }`) is non-trivial to map
//      across the entire UI. Keeping the flat shape at the adapter
//      boundary lets edit-vertex/value-codec.ts (and every other
//      consumer) stay untouched.
//   2. Timestamps travel as ISO strings in protobuf JSON, matching the
//      legacy `expiration?: string` field — so `new Date(expiration)`
//
// Why duplicate types instead of consuming the generated classes:
//   1. The connect-es v1 oneof representation
//      (`{ case: "string", value: "..." }`) is non-trivial to map
//      across the entire UI. Keeping the flat shape at the adapter
//      boundary lets edit-vertex/value-codec.ts (and every other
//      consumer) stay untouched.
//   2. Timestamps travel as ISO strings in protobuf JSON, matching the
//      legacy `expiration?: string` field — so `new Date(expiration)`
//      calls keep working.
//   3. `bytes` is base64-encoded in protobuf JSON — matches the legacy
//      `string` representation byte-for-byte.

export interface Vertex {
  key?: string;
  expiration?: string;
  float64?: number;
  float32?: number;
  int32?: number;
  int64?: string;
  uint32?: number;
  uint64?: string;
  bool?: boolean;
  string?: string;
  bytes?: string;
  timestamp?: string;
  duration?: string;
  // Proto Empty message → JSON `{}`; some legacy code paths still set
  // `true` so we accept both.
  nil?: Record<string, never> | true;
}

export interface Edge {
  tail?: string;
  head?: string;
  weight?: number;
  expiration?: string;
}

export interface Graph {
  vertices?: Vertex[];
  edges?: Edge[];
}

// Request/response value-object shapes that the legacy adapters
// returned. The usecase layer imports them by name (PutVertexBody,
// ScanVerticesRequest, ...) so re-declaring here keeps the imports
// stable post-migration.

export interface PutVertexBody {
  vertex?: Vertex;
}

export type PutOutcome =
  | "appliedAndLive"
  | "expired"
  | "conditionNotMet"
  | "superseded";

export type VertexPutResult = {
  key: string;
  outcome: PutOutcome;
};

export type PutVertexResponse = {
  outcome: PutOutcome;
};

export interface PutVerticesRequest {
  vertices?: Vertex[];
}

export type PutVerticesResponse = {
  results: VertexPutResult[];
};

export interface DeleteVertexResponse {
  existed?: boolean;
}

export interface ScanVerticesRequest {
  prefix?: string;
  limit?: number;
  cursor?: string;
}

export interface ScanVerticesResponse {
  vertices?: Vertex[];
  nextCursor?: string;
}

// Content-search (BM25 keyword) value-object shapes (#627/#1065).

/** How a multi-word content-search query's words combine (#892). */
export type SearchMatchMode = "server" | "any" | "all" | "min-should";
export type SearchProjection = "key-score" | "full-vertex";
export type SearchHitProjectionStatus =
  | "key-score"
  | "snapshot"
  | "missing"
  | "replaced";

export interface SearchVerticesRequest {
  query: string;
  limit?: number;
  prefix?: string;
  /** Word combination, or "server" to preserve the server default. */
  matchMode?: SearchMatchMode;
  /** Explicit threshold for "min-should". */
  minShouldMatch?: number;
  /** Require the query's words to occur adjacently, in order. */
  phrase?: boolean;
  /** Maximum fuzzy edit distance (0, 1, or 2). */
  fuzziness?: 0 | 1 | 2;
  /** Match dictionary terms that extend a query word. */
  prefixTerms?: boolean;
  /** Opaque endpoint-sticky cursor returned by the prior page. */
  cursor?: Uint8Array;
  /** Include exact value/TTL snapshots instead of key+score only. */
  projection?: SearchProjection;
}

export interface SearchHit {
  key: string;
  score: number;
  vertex?: Vertex;
  projectionStatus?: SearchHitProjectionStatus;
}

export interface SearchVerticesResponse {
  hits: SearchHit[];
  nextCursor: Uint8Array;
  effectiveLimit: number;
  truncated: boolean;
  continuationLimited: boolean;
}

export interface AddEdgeBody {
  edge?: Edge;
}

export interface AddEdgeResponse {
  edge?: Edge;
}

export interface AddDecayingEdgeBody {
  initialWeight: number;
  ratio: number;
  steps: number;
  intervalSeconds: number;
}

export interface AddDecayingEdgeResponse {
  /**
   * Effective (live-sum) weight on (tail, head) immediately after the
   * decaying add — any preexisting live weight plus `initialWeight`.
   * Returned by the SDK's `addDecayingEdge` and surfaced in the echo.
   */
  effectiveWeight: number;
}

export interface PutEdgeBody {
  edge?: Edge;
}

export type EdgePutResult = {
  tail: string;
  head: string;
  outcome: PutOutcome;
};

export type PutEdgeResponse = {
  outcome: PutOutcome;
};

export interface PutEdgesRequest {
  edges?: Edge[];
}

export type PutEdgesResponse = {
  results: EdgePutResult[];
};

export interface ScanEdgesRequest {
  tailPrefix?: string;
  headPrefix?: string;
  limit?: number;
  cursor?: string;
}

export interface ScanEdgesResponse {
  edges?: Edge[];
  nextCursor?: string;
}

export interface IlluminateResponse {
  graph?: Graph;
}
