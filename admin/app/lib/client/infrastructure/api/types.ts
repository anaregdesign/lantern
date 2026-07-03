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

export interface PutVertexResponse {
  vertex?: Vertex;
}

export interface PutVerticesRequest {
  vertices?: Vertex[];
}

export interface PutVerticesResponse {
  vertices?: Vertex[];
}

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

// Content-search (BM25 keyword) value-object shapes (#627). The server
// ranks vertices by indexed string/bytes content and returns lightweight
// `{ key, score }` hits — the admin search usecase hydrates the keys back
// into full vertices via GetVertices, preserving rank order.

/** How a multi-word content-search query's words combine (#892). */
export type SearchMatchMode = "any" | "all" | "min-should";

export interface SearchVerticesRequest {
  query: string;
  limit?: number;
  prefix?: string;
  /** Word combination: "any" (OR, default), "all" (AND), or "min-should". */
  matchMode?: SearchMatchMode;
  /** Require the query's words to occur adjacently, in order. */
  phrase?: boolean;
  /** Tolerate typos and match word prefixes (edit distance 1 + prefix terms). */
  fuzzy?: boolean;
}

export interface SearchHit {
  key: string;
  score: number;
}

export interface SearchVerticesResponse {
  hits: SearchHit[];
}

export interface AddEdgeBody {
  edge?: Edge;
}

export interface AddEdgeResponse {
  edge?: Edge;
}

export interface PutEdgeBody {
  edge?: Edge;
}

export interface PutEdgeResponse {
  edge?: Edge;
}

export interface PutEdgesRequest {
  edges?: Edge[];
}

export interface PutEdgesResponse {
  edges?: Edge[];
}

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
