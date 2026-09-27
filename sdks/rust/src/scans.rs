use std::fmt;

use prost::Message;

use crate::{
    CallOptions, Edge, LanternClient, LanternError, QueryStream, ScanOrder, Vertex,
    generated::graph::v1::{
        ScanEdgesRequest, ScanEdgesResponse, ScanVertexKeysRequest, ScanVertexKeysResponse,
        ScanVerticesRequest, ScanVerticesResponse,
    },
    paging::{PageItems, page_stream},
    transport::RetryClass,
    value::validate_key,
};

const CURSOR_MAGIC: &[u8] = b"lantern-rust-scan\0";
const CURSOR_VERSION: u32 = 1;
const MAX_CURSOR_BYTES: usize = 64 * 1024 * 1024;

#[derive(Clone, PartialEq, Message)]
struct BoundCursorEnvelope {
    #[prost(uint32, tag = "1")]
    version: u32,
    #[prost(uint32, tag = "2")]
    family: u32,
    #[prost(string, tag = "3")]
    endpoint: String,
    #[prost(string, tag = "4")]
    prefix: String,
    #[prost(string, tag = "5")]
    head_prefix: String,
    #[prost(uint32, tag = "6")]
    limit: u32,
    #[prost(int32, tag = "7")]
    order: i32,
    #[prost(bytes = "vec", tag = "8")]
    server_cursor: Vec<u8>,
}

#[derive(Clone, Copy)]
#[repr(u32)]
enum CursorFamily {
    Vertices = 1,
    Keys = 2,
    Edges = 3,
}

fn normalized_scan_order(order: ScanOrder) -> i32 {
    match order {
        ScanOrder::Unspecified | ScanOrder::Asc => ScanOrder::Asc as i32,
        ScanOrder::Desc => ScanOrder::Desc as i32,
    }
}

#[derive(Clone, Copy)]
struct CursorBinding<'a> {
    family: CursorFamily,
    endpoint: &'a str,
    prefix: &'a str,
    head_prefix: &'a str,
    limit: u32,
    order: i32,
}

impl<'a> CursorBinding<'a> {
    fn vertices(endpoint: &'a str, scan: &'a ScanOptions) -> Self {
        Self {
            family: CursorFamily::Vertices,
            endpoint,
            prefix: &scan.prefix,
            head_prefix: "",
            limit: scan.limit,
            order: normalized_scan_order(scan.order),
        }
    }

    fn keys(endpoint: &'a str, scan: &'a ScanOptions) -> Self {
        Self {
            family: CursorFamily::Keys,
            endpoint,
            prefix: &scan.prefix,
            head_prefix: "",
            limit: scan.limit,
            order: normalized_scan_order(scan.order),
        }
    }

    fn edges(endpoint: &'a str, scan: &'a EdgeScanOptions) -> Self {
        Self {
            family: CursorFamily::Edges,
            endpoint,
            prefix: &scan.tail_prefix,
            head_prefix: &scan.head_prefix,
            limit: scan.limit,
            order: 0,
        }
    }

    fn server_cursor(self, bytes: &[u8]) -> Result<Vec<u8>, LanternError> {
        let envelope = decode_bound_cursor(bytes, self.family)?;
        if envelope.endpoint != self.endpoint
            || envelope.prefix != self.prefix
            || envelope.head_prefix != self.head_prefix
            || envelope.limit != self.limit
            || envelope.order != self.order
        {
            return Err(LanternError::InvalidInput(
                "scan cursor does not match this endpoint or scan options",
            ));
        }
        Ok(envelope.server_cursor)
    }

    fn issue(self, server_cursor: Vec<u8>) -> Vec<u8> {
        encode_bound_cursor(&BoundCursorEnvelope {
            version: CURSOR_VERSION,
            family: self.family as u32,
            endpoint: self.endpoint.into(),
            prefix: self.prefix.into(),
            head_prefix: self.head_prefix.into(),
            limit: self.limit,
            order: self.order,
            server_cursor,
        })
    }
}

fn encode_bound_cursor(cursor: &BoundCursorEnvelope) -> Vec<u8> {
    let mut bytes = Vec::with_capacity(CURSOR_MAGIC.len() + cursor.encoded_len());
    bytes.extend_from_slice(CURSOR_MAGIC);
    bytes.extend_from_slice(&cursor.encode_to_vec());
    bytes
}

fn decode_bound_cursor(
    bytes: &[u8],
    expected: CursorFamily,
) -> Result<BoundCursorEnvelope, LanternError> {
    if bytes.len() <= CURSOR_MAGIC.len()
        || bytes.len() > MAX_CURSOR_BYTES
        || !bytes.starts_with(CURSOR_MAGIC)
    {
        return Err(LanternError::InvalidInput(
            "expected a versioned SDK-issued scan cursor",
        ));
    }
    let envelope = BoundCursorEnvelope::decode(&bytes[CURSOR_MAGIC.len()..])
        .map_err(|_| LanternError::InvalidInput("malformed SDK scan cursor"))?;
    if envelope.version != CURSOR_VERSION
        || envelope.family != expected as u32
        || envelope.endpoint.is_empty()
        || envelope.server_cursor.is_empty()
        || (matches!(expected, CursorFamily::Keys) && envelope.prefix.is_empty())
        || (matches!(expected, CursorFamily::Edges) && envelope.order != 0)
        || (!matches!(expected, CursorFamily::Edges)
            && (!envelope.head_prefix.is_empty()
                || !matches!(
                    ScanOrder::try_from(envelope.order),
                    Ok(ScanOrder::Asc | ScanOrder::Desc)
                )))
        || encode_bound_cursor(&envelope).as_slice() != bytes
    {
        return Err(LanternError::InvalidInput(
            "invalid or mismatched SDK scan cursor",
        ));
    }
    Ok(envelope)
}

macro_rules! scan_cursor {
    ($name:ident, $family:expr, $doc:literal) => {
        #[doc = $doc]
        #[derive(Clone, Default, Eq, PartialEq)]
        pub struct $name(Vec<u8>);

        impl $name {
            /// Restore only a versioned SDK-issued continuation cursor.
            /// Bare server cursor bytes and a different RPC's cursor fail.
            pub fn from_bytes(bytes: impl Into<Vec<u8>>) -> Result<Self, LanternError> {
                let bytes = bytes.into();
                decode_bound_cursor(&bytes, $family)?;
                Ok(Self(bytes))
            }

            /// Persist these bound opaque bytes verbatim for later resumption.
            pub fn as_bytes(&self) -> &[u8] {
                &self.0
            }

            /// Take ownership of the bound opaque bytes.
            pub fn into_bytes(self) -> Vec<u8> {
                self.0
            }

            fn server_cursor(&self, binding: CursorBinding<'_>) -> Result<Vec<u8>, LanternError> {
                if self.0.is_empty() {
                    return Ok(Vec::new());
                }
                binding.server_cursor(&self.0)
            }

            fn issued(binding: CursorBinding<'_>, server_cursor: Vec<u8>) -> Self {
                Self(binding.issue(server_cursor))
            }
        }

        impl fmt::Debug for $name {
            fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                f.debug_struct(stringify!($name))
                    .field("byte_len", &self.0.len())
                    .finish()
            }
        }
    };
}

scan_cursor!(
    VertexCursor,
    CursorFamily::Vertices,
    "Cursor for `ScanVertices`, not interchangeable with keys or edge cursors."
);
scan_cursor!(
    KeyCursor,
    CursorFamily::Keys,
    "Cursor for `ScanVertexKeys`, not interchangeable with vertex or edge cursors."
);
scan_cursor!(
    EdgeCursor,
    CursorFamily::Edges,
    "Cursor for `ScanEdges`, not interchangeable with either vertex cursor."
);

/// One bounded vertex or keys-only page. A zero limit uses the server's
/// configured default; the server caps larger requests at its maximum.
/// `Unspecified` and `Asc` have the same effective cursor order.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ScanOptions {
    pub prefix: String,
    pub limit: u32,
    pub order: ScanOrder,
}

impl Default for ScanOptions {
    fn default() -> Self {
        Self {
            prefix: String::new(),
            limit: 0,
            order: ScanOrder::Unspecified,
        }
    }
}

/// One bounded edge page, in ascending `(tail, head)` order. An empty tail
/// or head prefix disables that filter; both may be empty for a scan.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct EdgeScanOptions {
    pub tail_prefix: String,
    pub head_prefix: String,
    pub limit: u32,
}

/// Items and an optional cursor for exactly one server page, never an
/// implicitly collected prefix range.
#[derive(Clone, Debug, PartialEq)]
pub struct ScanPage<T, C> {
    pub items: Vec<T>,
    pub next_cursor: Option<C>,
}

impl LanternClient {
    /// Fetch one vertex page. The empty prefix is allowed. A continuation
    /// is locally bound to its issuing endpoint, prefix, limit, and order.
    pub async fn scan_vertices_page(
        &self,
        scan: ScanOptions,
        cursor: Option<VertexCursor>,
    ) -> Result<ScanPage<Vertex, VertexCursor>, LanternError> {
        self.scan_vertices_page_with_options(scan, cursor, CallOptions::Default)
            .await
    }

    pub async fn scan_vertices_page_with_options(
        &self,
        scan: ScanOptions,
        cursor: Option<VertexCursor>,
        options: CallOptions,
    ) -> Result<ScanPage<Vertex, VertexCursor>, LanternError> {
        let binding = CursorBinding::vertices(self.endpoint_identity(), &scan);
        let request = ScanVerticesRequest {
            prefix: scan.prefix.clone(),
            limit: scan.limit,
            cursor: cursor.unwrap_or_default().server_cursor(binding)?,
            order: scan.order as i32,
        };
        let current = request.cursor.clone();
        let response = self
            .data_unary(
                request,
                options,
                RetryClass::ReadOnly,
                |service, request| Box::pin(service.scan_vertices(request)),
            )
            .await?;
        validate_vertices_page(&scan, &current, response, binding)
    }

    /// Fetch one keys-only page. Unlike a full vertex scan, the prefix must
    /// be nonempty; cursor binding and this guard precede the RPC.
    pub async fn scan_vertex_keys_page(
        &self,
        scan: ScanOptions,
        cursor: Option<KeyCursor>,
    ) -> Result<ScanPage<String, KeyCursor>, LanternError> {
        self.scan_vertex_keys_page_with_options(scan, cursor, CallOptions::Default)
            .await
    }

    pub async fn scan_vertex_keys_page_with_options(
        &self,
        scan: ScanOptions,
        cursor: Option<KeyCursor>,
        options: CallOptions,
    ) -> Result<ScanPage<String, KeyCursor>, LanternError> {
        require_key_prefix(&scan)?;
        let binding = CursorBinding::keys(self.endpoint_identity(), &scan);
        let request = ScanVertexKeysRequest {
            prefix: scan.prefix.clone(),
            limit: scan.limit,
            cursor: cursor.unwrap_or_default().server_cursor(binding)?,
            order: scan.order as i32,
        };
        let current = request.cursor.clone();
        let response = self
            .data_unary(
                request,
                options,
                RetryClass::ReadOnly,
                |service, request| Box::pin(service.scan_vertex_keys(request)),
            )
            .await?;
        validate_keys_page(&scan, &current, response, binding)
    }

    /// Fetch one edge page. Edges are ordered by `(tail, head)` ascending;
    /// its cursor is locally bound to both prefixes, limit, and endpoint.
    pub async fn scan_edges_page(
        &self,
        scan: EdgeScanOptions,
        cursor: Option<EdgeCursor>,
    ) -> Result<ScanPage<Edge, EdgeCursor>, LanternError> {
        self.scan_edges_page_with_options(scan, cursor, CallOptions::Default)
            .await
    }

    pub async fn scan_edges_page_with_options(
        &self,
        scan: EdgeScanOptions,
        cursor: Option<EdgeCursor>,
        options: CallOptions,
    ) -> Result<ScanPage<Edge, EdgeCursor>, LanternError> {
        let binding = CursorBinding::edges(self.endpoint_identity(), &scan);
        let request = ScanEdgesRequest {
            tail_prefix: scan.tail_prefix.clone(),
            head_prefix: scan.head_prefix.clone(),
            limit: scan.limit,
            cursor: cursor.unwrap_or_default().server_cursor(binding)?,
        };
        let current = request.cursor.clone();
        let response = self
            .data_unary(
                request,
                options,
                RetryClass::ReadOnly,
                |service, request| Box::pin(service.scan_edges(request)),
            )
            .await?;
        validate_edges_page(&scan, &current, response, binding)
    }

    /// Lazily scan vertices, with one page buffered and one unary RPC in
    /// flight at most. Each page receives its own default unary deadline.
    pub fn scan_vertices_stream(&self, scan: ScanOptions) -> QueryStream<Vertex> {
        self.scan_vertices_stream_with_options(scan, CallOptions::Default)
    }

    /// Apply the same per-page deadline policy to each lazy vertex RPC.
    pub fn scan_vertices_stream_with_options(
        &self,
        scan: ScanOptions,
        options: CallOptions,
    ) -> QueryStream<Vertex> {
        self.scan_vertices_stream_from_with_options(scan, VertexCursor::default(), options)
    }

    /// Resume a lazy vertex scan from a cursor returned by this RPC.
    pub fn scan_vertices_stream_from(
        &self,
        scan: ScanOptions,
        cursor: VertexCursor,
    ) -> QueryStream<Vertex> {
        self.scan_vertices_stream_from_with_options(scan, cursor, CallOptions::Default)
    }

    pub fn scan_vertices_stream_from_with_options(
        &self,
        scan: ScanOptions,
        cursor: VertexCursor,
        options: CallOptions,
    ) -> QueryStream<Vertex> {
        let client = self.clone();
        page_stream(cursor, move |cursor| {
            let client = client.clone();
            let scan = scan.clone();
            async move {
                let page = client
                    .scan_vertices_page_with_options(scan, Some(cursor), options)
                    .await?;
                Ok(PageItems {
                    items: page.items,
                    next_cursor: page.next_cursor,
                    terminal_error: None,
                })
            }
        })
    }

    /// Lazily scan keys, requiring a nonempty prefix on the first poll.
    pub fn scan_vertex_keys_stream(&self, scan: ScanOptions) -> QueryStream<String> {
        self.scan_vertex_keys_stream_with_options(scan, CallOptions::Default)
    }

    /// Apply the same per-page deadline policy to each lazy keys-only RPC.
    pub fn scan_vertex_keys_stream_with_options(
        &self,
        scan: ScanOptions,
        options: CallOptions,
    ) -> QueryStream<String> {
        self.scan_vertex_keys_stream_from_with_options(scan, KeyCursor::default(), options)
    }

    /// Resume a lazy keys-only scan from a cursor returned by this RPC.
    pub fn scan_vertex_keys_stream_from(
        &self,
        scan: ScanOptions,
        cursor: KeyCursor,
    ) -> QueryStream<String> {
        self.scan_vertex_keys_stream_from_with_options(scan, cursor, CallOptions::Default)
    }

    pub fn scan_vertex_keys_stream_from_with_options(
        &self,
        scan: ScanOptions,
        cursor: KeyCursor,
        options: CallOptions,
    ) -> QueryStream<String> {
        let client = self.clone();
        page_stream(cursor, move |cursor| {
            let client = client.clone();
            let scan = scan.clone();
            async move {
                let page = client
                    .scan_vertex_keys_page_with_options(scan, Some(cursor), options)
                    .await?;
                Ok(PageItems {
                    items: page.items,
                    next_cursor: page.next_cursor,
                    terminal_error: None,
                })
            }
        })
    }

    /// Lazily scan edges in ascending `(tail, head)` order.
    pub fn scan_edges_stream(&self, scan: EdgeScanOptions) -> QueryStream<Edge> {
        self.scan_edges_stream_with_options(scan, CallOptions::Default)
    }

    /// Apply the same per-page deadline policy to each lazy edge RPC.
    pub fn scan_edges_stream_with_options(
        &self,
        scan: EdgeScanOptions,
        options: CallOptions,
    ) -> QueryStream<Edge> {
        self.scan_edges_stream_from_with_options(scan, EdgeCursor::default(), options)
    }

    /// Resume a lazy edge scan from a cursor returned by this RPC.
    pub fn scan_edges_stream_from(
        &self,
        scan: EdgeScanOptions,
        cursor: EdgeCursor,
    ) -> QueryStream<Edge> {
        self.scan_edges_stream_from_with_options(scan, cursor, CallOptions::Default)
    }

    pub fn scan_edges_stream_from_with_options(
        &self,
        scan: EdgeScanOptions,
        cursor: EdgeCursor,
        options: CallOptions,
    ) -> QueryStream<Edge> {
        let client = self.clone();
        page_stream(cursor, move |cursor| {
            let client = client.clone();
            let scan = scan.clone();
            async move {
                let page = client
                    .scan_edges_page_with_options(scan, Some(cursor), options)
                    .await?;
                Ok(PageItems {
                    items: page.items,
                    next_cursor: page.next_cursor,
                    terminal_error: None,
                })
            }
        })
    }
}

fn require_key_prefix(scan: &ScanOptions) -> Result<(), LanternError> {
    if scan.prefix.is_empty() {
        Err(LanternError::InvalidInput(
            "keys-only scans require a nonempty prefix",
        ))
    } else {
        Ok(())
    }
}

fn check_page_shape(
    count: usize,
    limit: u32,
    current: &[u8],
    next: &[u8],
) -> Result<(), LanternError> {
    if limit != 0 && count > limit as usize {
        return Err(LanternError::Protocol("scan page exceeds requested limit"));
    }
    if count == 0 && !next.is_empty() {
        return Err(LanternError::Protocol("empty scan page cannot continue"));
    }
    if !next.is_empty() && next == current {
        return Err(LanternError::Protocol("scan page repeated its cursor"));
    }
    Ok(())
}

fn validate_vertices_page(
    scan: &ScanOptions,
    current: &[u8],
    response: ScanVerticesResponse,
    binding: CursorBinding<'_>,
) -> Result<ScanPage<Vertex, VertexCursor>, LanternError> {
    check_page_shape(
        response.vertices.len(),
        scan.limit,
        current,
        &response.next_cursor,
    )?;
    let mut last = None;
    for vertex in &response.vertices {
        vertex.validate_response()?;
        if !vertex.key.starts_with(&scan.prefix) {
            return Err(LanternError::Protocol(
                "scan vertex lies outside requested prefix",
            ));
        }
        let key = vertex.key.as_str();
        if last.is_some_and(|previous| {
            if scan.order == ScanOrder::Desc {
                previous <= key
            } else {
                previous >= key
            }
        }) {
            return Err(LanternError::Protocol("scan vertex order is invalid"));
        }
        last = Some(key);
    }
    Ok(ScanPage {
        items: response.vertices,
        next_cursor: (!response.next_cursor.is_empty())
            .then(|| VertexCursor::issued(binding, response.next_cursor)),
    })
}

fn validate_keys_page(
    scan: &ScanOptions,
    current: &[u8],
    response: ScanVertexKeysResponse,
    binding: CursorBinding<'_>,
) -> Result<ScanPage<String, KeyCursor>, LanternError> {
    check_page_shape(
        response.keys.len(),
        scan.limit,
        current,
        &response.next_cursor,
    )?;
    let mut last = None;
    for key in &response.keys {
        validate_key(key).map_err(LanternError::Protocol)?;
        if !key.starts_with(&scan.prefix) {
            return Err(LanternError::Protocol(
                "scan key lies outside requested prefix",
            ));
        }
        let key = key.as_str();
        if last.is_some_and(|previous| {
            if scan.order == ScanOrder::Desc {
                previous <= key
            } else {
                previous >= key
            }
        }) {
            return Err(LanternError::Protocol("scan key order is invalid"));
        }
        last = Some(key);
    }
    Ok(ScanPage {
        items: response.keys,
        next_cursor: (!response.next_cursor.is_empty())
            .then(|| KeyCursor::issued(binding, response.next_cursor)),
    })
}

fn validate_edges_page(
    scan: &EdgeScanOptions,
    current: &[u8],
    response: ScanEdgesResponse,
    binding: CursorBinding<'_>,
) -> Result<ScanPage<Edge, EdgeCursor>, LanternError> {
    check_page_shape(
        response.edges.len(),
        scan.limit,
        current,
        &response.next_cursor,
    )?;
    let mut last = None;
    for edge in &response.edges {
        edge.validate_response()?;
        if !edge.tail.starts_with(&scan.tail_prefix) || !edge.head.starts_with(&scan.head_prefix) {
            return Err(LanternError::Protocol(
                "scan edge lies outside requested prefix",
            ));
        }
        let pair = (edge.tail.as_str(), edge.head.as_str());
        if last.is_some_and(|previous| previous >= pair) {
            return Err(LanternError::Protocol("scan edge order is invalid"));
        }
        last = Some(pair);
    }
    Ok(ScanPage {
        items: response.edges,
        next_cursor: (!response.next_cursor.is_empty())
            .then(|| EdgeCursor::issued(binding, response.next_cursor)),
    })
}

#[cfg(test)]
mod tests;
