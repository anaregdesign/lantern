/**
 * Generated OpenAPI types for the Lantern gateway.
 *
 * Source: pb/openapiv2/graph/v1/graph.swagger.json
 * Regenerate with `pnpm codegen`. Do not edit by hand.
 */

export interface paths {
    "/v1/edges/add": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** AddEdges is non-idempotent (accumulates weight). POST per REST conventions. */
        post: operations["LanternService_AddEdges"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/edges/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** DeleteEdges removes several edges in one round trip. */
        post: operations["LanternService_DeleteEdges"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/edges/get": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** GetEdges reads several edges in one round trip. */
        post: operations["LanternService_GetEdges"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/edges/put": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** PutEdges is idempotent (replaces weight). PUT per REST conventions. */
        put: operations["LanternService_PutEdges"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/edges/scan": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * ScanEdges streams edges whose tail key starts with `tail_prefix` AND
         *     whose head key starts with `head_prefix`, in ascending (tail, head)
         *     order, page by page. Plural-only — prefix scan is inherently plural.
         */
        post: operations["LanternService_ScanEdges"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/edges/{edge.tail}/{edge.head}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** PutEdge is idempotent (replaces weight). Thin facade over PutEdges. */
        put: operations["LanternService_PutEdge"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/edges/{edge.tail}/{edge.head}/add": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** AddEdge is non-idempotent (accumulates weight). Thin facade over AddEdges. */
        post: operations["LanternService_AddEdge"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/edges/{tail}/{head}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["LanternService_GetEdge"];
        put?: never;
        post?: never;
        delete: operations["LanternService_DeleteEdge"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/illuminate/{seed}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["LanternService_Illuminate"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/replication/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * GetReplicationStatus returns a flat snapshot of the local node's
         *     outbound peer-replication state. Read-only — no peer add/remove
         *     surface is exposed (see #315 out-of-scope). Cheap to call from a
         *     dashboard at any cadence the operator finds useful. On
         *     single-instance deployments enabled=false and peers is empty.
         */
        get: operations["LanternService_GetReplicationStatus"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * GetServerStatus returns a flat snapshot of the server's identity,
         *     build info, configuration ceilings, and current live vertex/edge
         *     counts. Read-only and cheap — intended for the admin UI's "Ops"
         *     tab and lightweight smoke-test tooling. Auth is the caller's
         *     responsibility; no PII is returned.
         */
        get: operations["LanternService_GetServerStatus"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/vertices": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put: operations["LanternService_PutVertices"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/vertices/count/{prefix}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * CountVerticesByPrefix returns the number of live vertices whose key
         *     starts with the given prefix.
         */
        get: operations["LanternService_CountVerticesByPrefix"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/vertices/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** DeleteVertices removes several vertices in one round trip. */
        post: operations["LanternService_DeleteVertices"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/vertices/delete-by-prefix": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * DeleteVerticesByPrefix deletes up to `limit` vertices whose key starts
         *     with the given prefix. Pass `dry_run = true` to preview the count
         *     without mutating state.
         */
        post: operations["LanternService_DeleteVerticesByPrefix"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/vertices/get": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** GetVertices reads several vertices in one round trip. */
        post: operations["LanternService_GetVertices"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/vertices/scan": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * ScanVertices streams vertices whose key starts with the given prefix in
         *     ascending order, page by page. Plural-only — prefix scan is inherently
         *     plural.
         */
        post: operations["LanternService_ScanVertices"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/vertices/{key}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get: operations["LanternService_GetVertex"];
        put?: never;
        post?: never;
        delete: operations["LanternService_DeleteVertex"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/vertices/{vertex.key}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** PutVertex writes a single vertex. Thin facade over PutVertices. */
        put: operations["LanternService_PutVertex"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        /**
         * @description AddEdgeRequest accumulates weight onto a single (tail, head) pair: repeated
         *     calls with the same endpoints sum their weights. This is the singular
         *     convenience wrapper over AddEdges and shares its non-idempotent semantics.
         */
        LanternServiceAddEdgeBody: {
            edge?: {
                /** Format: float */
                weight?: number;
                /** Format: date-time */
                expiration?: string;
            };
        };
        /**
         * @description PutEdgeRequest overwrites a single (tail, head) pair, replacing any
         *     existing weight and expiration. This is the singular convenience wrapper
         *     over PutEdges and shares its idempotent semantics.
         */
        LanternServicePutEdgeBody: {
            edge?: {
                /** Format: float */
                weight?: number;
                /** Format: date-time */
                expiration?: string;
            };
        };
        /**
         * @description PutVertexRequest writes a single vertex with upsert semantics. The
         *     Vertex.key field selects the target row and Vertex.expiration controls
         *     the TTL (absolute time). This is the singular convenience wrapper over
         *     PutVertices and shares its guard rails.
         */
        LanternServicePutVertexBody: {
            vertex?: {
                /** Format: date-time */
                expiration?: string;
                /** Format: double */
                float64?: number;
                /** Format: float */
                float32?: number;
                /** Format: int32 */
                int32?: number;
                /** Format: int64 */
                int64?: string;
                /** Format: int64 */
                uint32?: number;
                /** Format: uint64 */
                uint64?: string;
                bool?: boolean;
                string?: string;
                /** Format: byte */
                bytes?: string;
                /** Format: date-time */
                timestamp?: string;
                duration?: string;
                /**
                 * @description nil signals that the vertex carries no value (an "existence-only"
                 *     marker). The bool itself is always true when present; the variant
                 *     exists so the oneof can distinguish "explicitly nil" from "unset".
                 */
                nil?: boolean;
            };
        };
        /**
         * @description - STATE_CONNECTING: Pump is dialing or has dialed but Subscribe has not yet
         *     returned a frame.
         *      - STATE_STREAMING: Subscribe stream is open and frames have been received.
         *      - STATE_BACKOFF: Last session errored; the pump is sleeping before the next
         *     reconnect attempt.
         *      - STATE_CLOSED: Per-peer goroutine has exited (typically because DNS discovery
         *     removed the address). Reported transiently — the row drops out
         *     of subsequent snapshots once the supervisor has reaped it.
         * @default STATE_UNSPECIFIED
         * @enum {string}
         */
        ReplicationPeerState: "STATE_UNSPECIFIED" | "STATE_CONNECTING" | "STATE_STREAMING" | "STATE_BACKOFF" | "STATE_CLOSED";
        protobufAny: {
            "@type"?: string;
        } & {
            [key: string]: unknown;
        };
        rpcStatus: {
            /** Format: int32 */
            code?: number;
            message?: string;
            details?: components["schemas"]["protobufAny"][];
        };
        v1AddEdgeResponse: Record<string, never>;
        /**
         * @description AddEdgesRequest accumulates weight onto each (tail, head) pair: repeated
         *     calls with the same endpoints sum their weights. This operation is
         *     non-idempotent.
         */
        v1AddEdgesRequest: {
            edges?: components["schemas"]["v1Edge"][];
        };
        v1AddEdgesResponse: {
            /**
             * Format: int32
             * @description Number of edges whose weight contributions were accepted.
             */
            written?: number;
        };
        v1CountVerticesByPrefixResponse: {
            /** Format: uint64 */
            count?: string;
        };
        v1DeleteEdgeResponse: {
            /** @description existed is true if the edge was present and removed by this call. */
            existed?: boolean;
        };
        /**
         * @description DeleteEdgesRequest removes several edges in one round trip. Subject to the
         *     MaxBatchSize / MaxKeyLen guard rails.
         */
        v1DeleteEdgesRequest: {
            edges?: components["schemas"]["v1EdgeKey"][];
        };
        v1DeleteEdgesResponse: {
            /**
             * Format: int32
             * @description Number of edges the server attempted to delete (equals len(edges) on success).
             */
            deleted?: number;
        };
        v1DeleteVertexResponse: {
            /** @description existed is true if the vertex was present and removed by this call. */
            existed?: boolean;
        };
        /**
         * @description DeleteVerticesByPrefixRequest deletes up to `limit` vertices whose key
         *     starts with `prefix`. `limit == 0` lets the server apply its configured
         *     default (see RateLimitConfig / ScanConfig). When `dry_run` is true, no
         *     deletion is performed and the response reports the number that *would*
         *     be deleted.
         */
        v1DeleteVerticesByPrefixRequest: {
            prefix?: string;
            /** Format: int64 */
            limit?: number;
            dryRun?: boolean;
        };
        v1DeleteVerticesByPrefixResponse: {
            /** Format: uint64 */
            deleted?: string;
        };
        /**
         * @description DeleteVerticesRequest removes several vertices in one round trip. Same
         *     cascade semantics as DeleteVertex (edges reaped lazily by the GC loop).
         *     Subject to the same MaxBatchSize / MaxKeyLen guard rails as the put RPCs.
         */
        v1DeleteVerticesRequest: {
            keys?: string[];
        };
        v1DeleteVerticesResponse: {
            /**
             * Format: int32
             * @description Number of keys the server attempted to delete (equals len(keys) on success).
             */
            deleted?: number;
        };
        v1Edge: {
            tail?: string;
            head?: string;
            /** Format: float */
            weight?: number;
            /** Format: date-time */
            expiration?: string;
        };
        /** @description EdgeKey identifies an edge by its (tail, head) pair without weight. */
        v1EdgeKey: {
            tail?: string;
            head?: string;
        };
        v1GetEdgeResponse: {
            edge?: components["schemas"]["v1Edge"];
        };
        /**
         * @description GetEdgesRequest reads several edges in one round trip. Subject to the
         *     same MaxBatchSize / MaxKeyLen guard rails as the write RPCs.
         */
        v1GetEdgesRequest: {
            edges?: components["schemas"]["v1EdgeKey"][];
        };
        v1GetEdgesResponse: {
            /**
             * @description edges contains every (tail, head) pair that was present at the time of
             *     the call. Order is unspecified; clients should match by (Edge.tail,
             *     Edge.head).
             */
            edges?: components["schemas"]["v1Edge"][];
            /**
             * @description missing lists the requested (tail, head) pairs that did not exist (or
             *     had expired).
             */
            missing?: components["schemas"]["v1EdgeKey"][];
        };
        v1GetReplicationStatusResponse: {
            /**
             * @description node_id is the local HLC NodeID rendered as lowercase hex (32 chars).
             *     Stable for the lifetime of the process; either configured via
             *     LANTERN_NODE_ID or randomly generated at startup.
             */
            nodeId?: string;
            /**
             * Format: date-time
             * @description local_now is the server's wall-clock at the moment the snapshot
             *     was taken. Provided so clients can compute per-peer staleness
             *     (local_now - last_event_at) without trusting their own clock.
             */
            localNow?: string;
            /**
             * @description enabled is false on a single-instance deployment (no peers
             *     configured AND no DNS discovery). When false, peers is empty but
             *     the response is still well-formed — handlers never return
             *     Unimplemented for this RPC.
             */
            enabled?: boolean;
            /**
             * @description peers is the per-peer slice. Order is the supervisor's iteration
             *     order and is intentionally unspecified; clients sort by address
             *     for display.
             */
            peers?: components["schemas"]["v1ReplicationPeer"][];
        };
        v1GetServerStatusResponse: {
            /**
             * @description Build/version stamp. Falls back to "dev" when the binary was built
             *     without VCS info or without LANTERN_VERSION set.
             */
            version?: string;
            /** @description Reports `runtime.Version()` (e.g. "go1.26.4"). */
            goVersion?: string;
            /**
             * Format: date-time
             * @description Wall-clock instant the server process started serving requests.
             *     Captured at gRPC server start, not at process start, so the value
             *     reflects "ready to serve" rather than "wire init done".
             */
            startedAt?: string;
            /**
             * @description Convenience field: now - started_at on the server side, so clients
             *     do not have to know the server's clock to display uptime.
             */
            uptime?: string;
            /**
             * @description The default TTL applied to vertices/edges when the caller does not
             *     specify Expiration (LANTERN_DEFAULT_TTL_SECONDS).
             */
            defaultTtl?: string;
            /**
             * Format: int64
             * @description Validation ceiling for batch RPCs (LANTERN_MAX_BATCH_SIZE).
             */
            maxBatchSize?: number;
            /**
             * Format: int64
             * @description Validation ceiling for vertex/edge keys (LANTERN_MAX_KEY_BYTES).
             */
            maxKeyBytes?: number;
            /**
             * Format: int64
             * @description Per-call defaults / hard caps for prefix-scan pagination
             *     (LANTERN_SCAN_DEFAULT_LIMIT / LANTERN_SCAN_MAX_LIMIT).
             */
            scanDefaultLimit?: number;
            /** Format: int64 */
            scanMaxLimit?: number;
            /**
             * @description True when the gRPC server is terminating TLS
             *     (LANTERN_TLS_CERT_FILE + LANTERN_TLS_KEY_FILE both set).
             */
            tlsEnabled?: boolean;
            /**
             * @description True when this server is wired to a mutation log + HLC clock and is
             *     therefore a member of a replication group. False on single-node
             *     deployments.
             */
            replicationEnabled?: boolean;
            /**
             * Format: uint64
             * @description Live counts pulled from the in-memory graph cache. Cheap to compute
             *     (index sizes, no scan). Intended for at-a-glance dashboards — these
             *     are not transactional snapshots and may include not-yet-collected
             *     expired entries bounded by the GC tick.
             */
            vertexCount?: string;
            /** Format: uint64 */
            edgeCount?: string;
        };
        v1GetVertexResponse: {
            vertex?: components["schemas"]["v1Vertex"];
        };
        /**
         * @description GetVerticesRequest reads several vertices in one round trip. Subject to
         *     the same MaxBatchSize / MaxKeyLen guard rails as the write RPCs.
         */
        v1GetVerticesRequest: {
            keys?: string[];
        };
        v1GetVerticesResponse: {
            /**
             * @description vertices contains every key that was present at the time of the call.
             *     Order is unspecified; clients should match by Vertex.key.
             */
            vertices?: components["schemas"]["v1Vertex"][];
            /** @description missing lists the requested keys that did not exist (or had expired). */
            missing?: string[];
        };
        v1Graph: {
            vertices?: components["schemas"]["v1Vertex"][];
            edges?: components["schemas"]["v1Edge"][];
        };
        v1IlluminateResponse: {
            graph?: components["schemas"]["v1Graph"];
        };
        /**
         * @default OPTIMIZATION_UNSPECIFIED
         * @enum {string}
         */
        v1Optimization: "OPTIMIZATION_UNSPECIFIED" | "OPTIMIZATION_MINIMUM_SPANNING_TREE" | "OPTIMIZATION_MAXIMUM_SPANNING_TREE" | "OPTIMIZATION_SHORTEST_PATH_TREE" | "OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE";
        v1PutEdgeResponse: Record<string, never>;
        /**
         * @description PutEdgesRequest overwrites each (tail, head) pair, replacing any existing
         *     weight and expiration. This operation is idempotent.
         */
        v1PutEdgesRequest: {
            edges?: components["schemas"]["v1Edge"][];
        };
        v1PutEdgesResponse: {
            /**
             * Format: int32
             * @description Number of edges accepted (overwritten or created).
             */
            written?: number;
        };
        v1PutVertexResponse: Record<string, never>;
        /**
         * @description PutVerticesRequest writes vertices with upsert semantics: each Vertex.key
         *     replaces any existing value at that key. Use the Vertex.expiration field
         *     (absolute time) to control TTL.
         */
        v1PutVerticesRequest: {
            vertices?: components["schemas"]["v1Vertex"][];
        };
        v1PutVerticesResponse: {
            /**
             * Format: int32
             * @description Number of vertices accepted by the server. Currently always equals the
             *     request size on success; reserved for future per-item validation that may
             *     report partial writes.
             */
            written?: number;
        };
        /**
         * @description ReplicationPeer is one row of the GetReplicationStatus snapshot.
         *     Each row models the local node's view of a single outbound peer
         *     connection owned by the replication pump (#185). Fields are
         *     best-effort point-in-time samples; clients should treat consecutive
         *     snapshots as monotonically advancing rather than transactional.
         */
        v1ReplicationPeer: {
            /**
             * @description address is the dial target as configured in LANTERN_PEERS (or
             *     resolved from DNS discovery). Stable across reconnects for a given
             *     peer, so it doubles as the row identity for admin UI displays.
             */
            address?: string;
            state?: components["schemas"]["ReplicationPeerState"];
            /**
             * Format: date-time
             * @description last_event_at is the wall-clock instant the pump last received any
             *     frame (Subscribe or Snapshot) from this peer. Unset when nothing
             *     has been received yet. Combined with the response-level local_now
             *     it yields the "how stale is this stream" diagnostic the admin UI
             *     shows as a lag bar.
             */
            lastEventAt?: string;
            /**
             * Format: uint64
             * @description applied_seq is the highest peer-local sequence number the pump has
             *     successfully consumed from this peer. Zero on a fresh connection.
             */
            appliedSeq?: string;
            /**
             * @description error carries the last non-recoverable error message reported by
             *     the per-peer session loop. Cleared on the next successful frame.
             */
            error?: string;
        };
        /**
         * @description ScanEdgesRequest streams edges whose tail key starts with `tail_prefix`
         *     AND whose head key starts with `head_prefix`, in ascending (tail, head)
         *     order. Either prefix may be empty to disable the corresponding filter
         *     (both empty scans every edge). `limit` caps the number returned in one
         *     call; the server enforces a default when `limit == 0` and a hard maximum
         *     (see ScanConfig on the server). `cursor` MUST be treated as opaque bytes
         *     by clients — pass back exactly what the previous response returned in
         *     `next_cursor`. An empty `cursor` starts from the beginning.
         *
         *     Implementation note: v1 walks the tail-side prefix index and applies
         *     `head_prefix` as a post-filter; for highly selective `head_prefix`
         *     queries the server may visit many tails to fill one page. Latency is
         *     reported in the standard scan histogram so operators can spot
         *     pathological filters.
         */
        v1ScanEdgesRequest: {
            tailPrefix?: string;
            headPrefix?: string;
            /** Format: int64 */
            limit?: number;
            /**
             * Format: byte
             * @description NOTE: opaque to the caller; not interchangeable with cursors from
             *     other Scan* RPCs (e.g. ScanVertices). Cross-feeding is rejected with
             *     INVALID_ARGUMENT rather than silently restarting the scan.
             */
            cursor?: string;
        };
        v1ScanEdgesResponse: {
            /**
             * @description edges returned in ascending (tail, head) order. May be shorter than
             *     `limit` when the underlying range is exhausted.
             */
            edges?: components["schemas"]["v1Edge"][];
            /**
             * Format: byte
             * @description next_cursor is non-empty when more results are available. An empty
             *     value signals end of stream.
             */
            nextCursor?: string;
        };
        /**
         * @description ScanVerticesRequest streams vertices whose key starts with `prefix` in
         *     lexicographic order. `limit` caps the number returned in one call; the
         *     server enforces a default when `limit == 0` and a hard maximum (see
         *     RateLimitConfig / ScanConfig on the server). `cursor` MUST be treated as
         *     opaque bytes by clients — pass back exactly what the previous response
         *     returned in `next_cursor`. An empty `cursor` starts from the beginning.
         */
        v1ScanVerticesRequest: {
            prefix?: string;
            /** Format: int64 */
            limit?: number;
            /**
             * Format: byte
             * @description NOTE: opaque to the caller; not interchangeable with cursors from
             *     other Scan* RPCs (e.g. ScanEdges). Cross-feeding is rejected with
             *     INVALID_ARGUMENT rather than silently restarting the scan.
             */
            cursor?: string;
        };
        v1ScanVerticesResponse: {
            /**
             * @description vertices returned in ascending key order. May be shorter than `limit`
             *     when the underlying range is exhausted.
             */
            vertices?: components["schemas"]["v1Vertex"][];
            /**
             * Format: byte
             * @description next_cursor is non-empty when more results are available. An empty
             *     value signals end of stream.
             */
            nextCursor?: string;
        };
        v1Vertex: {
            key?: string;
            /** Format: date-time */
            expiration?: string;
            /** Format: double */
            float64?: number;
            /** Format: float */
            float32?: number;
            /** Format: int32 */
            int32?: number;
            /** Format: int64 */
            int64?: string;
            /** Format: int64 */
            uint32?: number;
            /** Format: uint64 */
            uint64?: string;
            bool?: boolean;
            string?: string;
            /** Format: byte */
            bytes?: string;
            /** Format: date-time */
            timestamp?: string;
            duration?: string;
            /**
             * @description nil signals that the vertex carries no value (an "existence-only"
             *     marker). The bool itself is always true when present; the variant
             *     exists so the oneof can distinguish "explicitly nil" from "unset".
             */
            nil?: boolean;
        };
    };
    responses: never;
    parameters: never;
    requestBodies: never;
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    LanternService_AddEdges: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description AddEdgesRequest accumulates weight onto each (tail, head) pair: repeated
         *     calls with the same endpoints sum their weights. This operation is
         *     non-idempotent.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1AddEdgesRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1AddEdgesResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_DeleteEdges: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description DeleteEdgesRequest removes several edges in one round trip. Subject to the
         *     MaxBatchSize / MaxKeyLen guard rails.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1DeleteEdgesRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1DeleteEdgesResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_GetEdges: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description GetEdgesRequest reads several edges in one round trip. Subject to the
         *     same MaxBatchSize / MaxKeyLen guard rails as the write RPCs.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1GetEdgesRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1GetEdgesResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_PutEdges: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description PutEdgesRequest overwrites each (tail, head) pair, replacing any existing
         *     weight and expiration. This operation is idempotent.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1PutEdgesRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1PutEdgesResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_ScanEdges: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description ScanEdgesRequest streams edges whose tail key starts with `tail_prefix`
         *     AND whose head key starts with `head_prefix`, in ascending (tail, head)
         *     order. Either prefix may be empty to disable the corresponding filter
         *     (both empty scans every edge). `limit` caps the number returned in one
         *     call; the server enforces a default when `limit == 0` and a hard maximum
         *     (see ScanConfig on the server). `cursor` MUST be treated as opaque bytes
         *     by clients — pass back exactly what the previous response returned in
         *     `next_cursor`. An empty `cursor` starts from the beginning.
         *
         *     Implementation note: v1 walks the tail-side prefix index and applies
         *     `head_prefix` as a post-filter; for highly selective `head_prefix`
         *     queries the server may visit many tails to fill one page. Latency is
         *     reported in the standard scan histogram so operators can spot
         *     pathological filters.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1ScanEdgesRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1ScanEdgesResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_PutEdge: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                "edge.tail": string;
                "edge.head": string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["LanternServicePutEdgeBody"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1PutEdgeResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_AddEdge: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                "edge.tail": string;
                "edge.head": string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["LanternServiceAddEdgeBody"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1AddEdgeResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_GetEdge: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                tail: string;
                head: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1GetEdgeResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_DeleteEdge: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                tail: string;
                head: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1DeleteEdgeResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_Illuminate: {
        parameters: {
            query?: {
                step?: number;
                k?: number;
                tfidf?: boolean;
                optimization?: "OPTIMIZATION_UNSPECIFIED" | "OPTIMIZATION_MINIMUM_SPANNING_TREE" | "OPTIMIZATION_MAXIMUM_SPANNING_TREE" | "OPTIMIZATION_SHORTEST_PATH_TREE" | "OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE";
            };
            header?: never;
            path: {
                seed: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1IlluminateResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_GetReplicationStatus: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1GetReplicationStatusResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_GetServerStatus: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1GetServerStatusResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_PutVertices: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description PutVerticesRequest writes vertices with upsert semantics: each Vertex.key
         *     replaces any existing value at that key. Use the Vertex.expiration field
         *     (absolute time) to control TTL.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1PutVerticesRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1PutVerticesResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_CountVerticesByPrefix: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                prefix: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1CountVerticesByPrefixResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_DeleteVertices: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description DeleteVerticesRequest removes several vertices in one round trip. Same
         *     cascade semantics as DeleteVertex (edges reaped lazily by the GC loop).
         *     Subject to the same MaxBatchSize / MaxKeyLen guard rails as the put RPCs.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1DeleteVerticesRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1DeleteVerticesResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_DeleteVerticesByPrefix: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description DeleteVerticesByPrefixRequest deletes up to `limit` vertices whose key
         *     starts with `prefix`. `limit == 0` lets the server apply its configured
         *     default (see RateLimitConfig / ScanConfig). When `dry_run` is true, no
         *     deletion is performed and the response reports the number that *would*
         *     be deleted.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1DeleteVerticesByPrefixRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1DeleteVerticesByPrefixResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_GetVertices: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description GetVerticesRequest reads several vertices in one round trip. Subject to
         *     the same MaxBatchSize / MaxKeyLen guard rails as the write RPCs.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1GetVerticesRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1GetVerticesResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_ScanVertices: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * @description ScanVerticesRequest streams vertices whose key starts with `prefix` in
         *     lexicographic order. `limit` caps the number returned in one call; the
         *     server enforces a default when `limit == 0` and a hard maximum (see
         *     RateLimitConfig / ScanConfig on the server). `cursor` MUST be treated as
         *     opaque bytes by clients — pass back exactly what the previous response
         *     returned in `next_cursor`. An empty `cursor` starts from the beginning.
         */
        requestBody: {
            content: {
                "application/json": components["schemas"]["v1ScanVerticesRequest"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1ScanVerticesResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_GetVertex: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                key: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1GetVertexResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_DeleteVertex: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                key: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1DeleteVertexResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
    LanternService_PutVertex: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                "vertex.key": string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["LanternServicePutVertexBody"];
            };
        };
        responses: {
            /** @description A successful response. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["v1PutVertexResponse"];
                };
            };
            /** @description An unexpected error response. */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["rpcStatus"];
                };
            };
        };
    };
}
