use std::fmt;

use crate::{
    CallOptions, LanternClient, LanternError, MatchMode, QueryStream, SearchHitProjectionStatus,
    SearchProjection, Vertex,
    generated::graph::v1::{
        SearchHit as WireSearchHit, SearchOptions, SearchVerticesRequest, SearchVerticesResponse,
    },
    paging::{PageItems, page_stream},
    transport::RetryClass,
    value::validate_key,
};

/// Relevance options for one bounded search page or a lazy series of pages.
///
/// A zero `limit` delegates the page size to the server. The default
/// projection carries only keys and scores; `FullVertex` must be selected
/// explicitly to receive proven snapshot values and expirations.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SearchRequest {
    pub query: String,
    pub prefix: String,
    pub limit: u32,
    pub match_mode: MatchMode,
    pub min_should_match: u32,
    pub phrase: bool,
    pub fuzziness: u32,
    pub prefix_terms: bool,
    pub projection: SearchProjection,
}

impl SearchRequest {
    pub fn new(query: impl Into<String>) -> Self {
        Self {
            query: query.into(),
            prefix: String::new(),
            limit: 0,
            match_mode: MatchMode::Unspecified,
            min_should_match: 0,
            phrase: false,
            fuzziness: 0,
            prefix_terms: false,
            projection: SearchProjection::KeyScore,
        }
    }

    fn validate(&self) -> Result<(), LanternError> {
        if self.fuzziness > 2 {
            return Err(LanternError::InvalidInput(
                "search fuzziness must be at most 2",
            ));
        }
        if self.min_should_match != 0 && self.match_mode != MatchMode::MinShould {
            return Err(LanternError::InvalidInput(
                "search min_should_match requires MinShould match mode",
            ));
        }
        if self.phrase
            && (self.match_mode != MatchMode::Unspecified
                || self.min_should_match != 0
                || self.fuzziness != 0
                || self.prefix_terms)
        {
            return Err(LanternError::InvalidInput(
                "search phrase cannot be combined with match mode, min_should_match, fuzziness, or prefix terms",
            ));
        }
        Ok(())
    }

    fn to_wire(&self, cursor: SearchCursor) -> SearchVerticesRequest {
        let options = if self.match_mode == MatchMode::Unspecified
            && self.min_should_match == 0
            && !self.phrase
            && self.fuzziness == 0
            && !self.prefix_terms
        {
            None
        } else {
            Some(SearchOptions {
                match_mode: self.match_mode as i32,
                min_should_match: self.min_should_match,
                phrase: self.phrase,
                fuzziness: self.fuzziness,
                prefix_terms: self.prefix_terms,
            })
        };
        SearchVerticesRequest {
            query: self.query.clone(),
            prefix: self.prefix.clone(),
            limit: self.limit,
            options,
            cursor: cursor.0,
            projection: self.projection as i32,
        }
    }
}

/// Endpoint- and request-bound continuation. Store its bytes verbatim; do not
/// interpret them or reuse them with another projection, option set, or server.
#[derive(Clone, Default, Eq, Hash, PartialEq)]
pub struct SearchCursor(Vec<u8>);

impl SearchCursor {
    /// Restore opaque bytes returned by an earlier search page. Malformed,
    /// expired, and mismatched cursors retain the server's typed error.
    pub fn from_bytes(bytes: impl Into<Vec<u8>>) -> Self {
        Self(bytes.into())
    }

    pub fn as_bytes(&self) -> &[u8] {
        &self.0
    }

    pub fn into_bytes(self) -> Vec<u8> {
        self.0
    }

    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }
}

impl AsRef<[u8]> for SearchCursor {
    fn as_ref(&self) -> &[u8] {
        self.as_bytes()
    }
}

impl fmt::Debug for SearchCursor {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("SearchCursor")
            .field("bytes", &self.0.len())
            .finish()
    }
}

/// A ranked result. Only `Snapshot` contains a proven, exact value and TTL.
#[derive(Clone, Debug, PartialEq)]
pub struct SearchHit {
    pub key: String,
    pub score: f64,
    pub projection_status: SearchHitProjectionStatus,
    pub vertex: Option<Vertex>,
}

/// One bounded response, including the server's effective/clamped page size.
///
/// `truncated` says this page is not exhaustive; it is not by itself a
/// terminal error. `continuation_limited` says some matching hits could not
/// be retained. A nonempty cursor can still lead to *more retained hits* in
/// that case, so a stream follows it before reporting the incomplete tail.
#[derive(Clone, Debug, PartialEq)]
pub struct SearchPage {
    pub hits: Vec<SearchHit>,
    pub next_cursor: Option<SearchCursor>,
    pub effective_limit: u32,
    pub truncated: bool,
    pub continuation_limited: bool,
}

impl SearchPage {
    fn from_wire(
        response: SearchVerticesResponse,
        projection: SearchProjection,
    ) -> Result<Self, LanternError> {
        if response.effective_limit == 0 || response.hits.len() > response.effective_limit as usize
        {
            return Err(LanternError::Protocol(
                "search response has an invalid effective limit or hit count",
            ));
        }
        if (response.continuation_limited && !response.truncated)
            || (!response.next_cursor.is_empty()
                && (!response.truncated || response.hits.is_empty()))
        {
            return Err(LanternError::Protocol(
                "search response has inconsistent continuation flags",
            ));
        }
        let hits = response
            .hits
            .into_iter()
            .map(|hit| SearchHit::from_wire(hit, projection))
            .collect::<Result<_, _>>()?;
        Ok(Self {
            hits,
            next_cursor: (!response.next_cursor.is_empty())
                .then_some(SearchCursor(response.next_cursor)),
            effective_limit: response.effective_limit,
            truncated: response.truncated,
            continuation_limited: response.continuation_limited,
        })
    }

    fn into_stream_items(self) -> PageItems<SearchHit, SearchCursor> {
        // The Go server can mark the first page limited while issuing a cursor
        // to later retained hits. page_stream remembers this terminal error
        // across those pages and emits it when the last cursor is empty.
        let terminal_error = if self.continuation_limited {
            Some(LanternError::SearchContinuationLimited)
        } else if self.truncated && self.next_cursor.is_none() {
            Some(LanternError::Protocol(
                "truncated search page has no continuation",
            ))
        } else {
            None
        };
        PageItems {
            items: self.hits,
            next_cursor: self.next_cursor,
            terminal_error,
        }
    }
}

impl SearchHit {
    fn from_wire(hit: WireSearchHit, projection: SearchProjection) -> Result<Self, LanternError> {
        validate_key(&hit.key).map_err(LanternError::Protocol)?;
        if !hit.score.is_finite() {
            return Err(LanternError::Protocol("search hit has a nonfinite score"));
        }
        let status = SearchHitProjectionStatus::try_from(hit.projection_status)
            .map_err(|_| LanternError::Protocol("search hit has an unknown projection status"))?;
        let vertex = match (projection, status, hit.vertex) {
            (
                SearchProjection::KeyScore | SearchProjection::Unspecified,
                SearchHitProjectionStatus::KeyScore,
                None,
            )
            | (
                SearchProjection::FullVertex,
                SearchHitProjectionStatus::Missing | SearchHitProjectionStatus::Replaced,
                None,
            ) => None,
            (SearchProjection::FullVertex, SearchHitProjectionStatus::Snapshot, Some(vertex)) => {
                vertex.validate_response()?;
                if vertex.key != hit.key {
                    return Err(LanternError::Protocol(
                        "search snapshot vertex does not match the hit key",
                    ));
                }
                Some(vertex)
            }
            _ => {
                return Err(LanternError::Protocol(
                    "search hit projection, status, and vertex disagree",
                ));
            }
        };
        Ok(Self {
            key: hit.key,
            score: hit.score,
            projection_status: status,
            vertex,
        })
    }
}

impl LanternClient {
    /// Search once without implicitly draining a potentially large result set.
    pub async fn search_vertices(
        &self,
        request: SearchRequest,
    ) -> Result<SearchPage, LanternError> {
        self.search_vertices_with_options(request, CallOptions::Default)
            .await
    }

    /// Search once with an explicit per-page unary budget.
    pub async fn search_vertices_with_options(
        &self,
        request: SearchRequest,
        options: CallOptions,
    ) -> Result<SearchPage, LanternError> {
        self.search_vertices_page_with_options(request, SearchCursor::default(), options)
            .await
    }

    /// Fetch exactly one page. A nonempty cursor remains bound to the same
    /// server, query, prefix, page size, options, and projection.
    pub async fn search_vertices_page(
        &self,
        request: SearchRequest,
        cursor: SearchCursor,
    ) -> Result<SearchPage, LanternError> {
        self.search_vertices_page_with_options(request, cursor, CallOptions::Default)
            .await
    }

    /// Fetch one page with an explicit unary budget and no cursor restart.
    pub async fn search_vertices_page_with_options(
        &self,
        request: SearchRequest,
        cursor: SearchCursor,
        options: CallOptions,
    ) -> Result<SearchPage, LanternError> {
        request.validate()?;
        let projection = request.projection;
        let response = self
            .data_unary(
                request.to_wire(cursor),
                options,
                RetryClass::ReadOnly,
                |service, request| Box::pin(service.search_vertices(request)),
            )
            .await?;
        SearchPage::from_wire(response, projection)
    }

    /// Lazily fetch pages, retaining at most one page of hits. A bounded tail
    /// emits every retained hit followed by `SearchContinuationLimited`, even
    /// when the server supplies no final cursor. A truncated page without a
    /// cursor or continuation-limit signal is a protocol error, not success;
    /// an invalid/stale cursor retains its typed RPC failure. Dropping cancels
    /// active work.
    pub fn search_vertices_stream(&self, request: SearchRequest) -> QueryStream<SearchHit> {
        self.search_vertices_stream_with_options(request, CallOptions::Default)
    }

    /// Stream with an explicit budget applied afresh to each unary page.
    pub fn search_vertices_stream_with_options(
        &self,
        request: SearchRequest,
        options: CallOptions,
    ) -> QueryStream<SearchHit> {
        self.search_vertices_stream_from_with_options(request, SearchCursor::default(), options)
    }

    /// Resume a lazy stream from an opaque cursor on this same endpoint.
    pub fn search_vertices_stream_from(
        &self,
        request: SearchRequest,
        cursor: SearchCursor,
    ) -> QueryStream<SearchHit> {
        self.search_vertices_stream_from_with_options(request, cursor, CallOptions::Default)
    }

    /// Resume with frozen request options, projection, endpoint, and a fresh
    /// unary budget for each page; never restart a stale or invalid cursor.
    pub fn search_vertices_stream_from_with_options(
        &self,
        request: SearchRequest,
        cursor: SearchCursor,
        options: CallOptions,
    ) -> QueryStream<SearchHit> {
        let client = self.clone();
        page_stream(cursor, move |cursor| {
            let client = client.clone();
            let request = request.clone();
            async move {
                let page = client
                    .search_vertices_page_with_options(request, cursor, options)
                    .await?;
                Ok(page.into_stream_items())
            }
        })
    }
}

#[cfg(test)]
mod tests;
