//! Native Lantern Rust client.
//!
//! The generated gRPC clients and request envelopes remain private. Use
//! [`LanternClient::builder`] to connect to one h2c or verified-HTTPS endpoint.
//! Exact-value CRUD, bounded queries, and typed graph traversals are provided
//! by a single-endpoint client.

#![forbid(unsafe_code)]

#[allow(dead_code)]
mod generated {
    pub(crate) mod graph {
        pub(crate) mod v1 {
            include!("generated/graph.v1.rs");
        }
    }
}

mod batch;
mod contrib;
mod crud;
mod discovery;
mod error;
mod paging;
mod prefix;
mod scans;
mod search;
mod transport;
mod traversal;
mod value;

pub use batch::{AddBatch, DeleteBatch, GetBatch};
pub use contrib::{AddInput, ContribId, PreparedAdd};
pub use discovery::{
    DegreeDirection, DegreeEntry, DegreeRankingOptions, ReplicationPeer, ReplicationPeerState,
    ReplicationStatus, ServerStatus,
};
pub use error::{BatchError, LanternError, RpcErrorKind, RpcFailure, SearchDetails};
pub use generated::graph::v1::{
    Edge, MatchMode, Objective, PutOutcome, Reduction, ScanOrder, SearchCapabilities,
    SearchErrorReason, SearchHitProjectionStatus, SearchIndexHealth, SearchProjection, Vertex,
    Weighting, vertex::Value as VertexValue,
};
pub use paging::QueryStream;
pub use prost_types::{Duration as ProtoDuration, Timestamp};
pub use scans::{EdgeCursor, EdgeScanOptions, KeyCursor, ScanOptions, ScanPage, VertexCursor};
pub use search::{SearchCursor, SearchHit, SearchPage, SearchRequest};
pub use transport::{
    CallOptions, LanternClient, LanternClientBuilder, RetryPolicy, TokenError, TokenProvider,
};
pub use traversal::{
    BfsOptions, CommunityOptions, Graph, GraphEdge, PprOptions, TraversalFamily, TraversalOptions,
};
pub use value::{EdgeInput, EdgeRef, Expiration, VertexInput, VertexKind};

#[cfg(test)]
mod smoke;
#[cfg(test)]
mod test_server;
