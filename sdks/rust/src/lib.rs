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

mod backup;
mod batch;
mod cdc;
mod contrib;
mod crud;
mod discovery;
mod error;
mod paging;
mod prefix;
mod scans;
mod scoped_changes;
mod search;
mod transport;
mod traversal;
mod value;

pub use backup::{
    BackupFormat, BackupManifest, BackupRecord, BackupStream, RestoreFailure, RestoreOptions,
    RestoreReport,
};
pub use batch::{AddBatch, DeleteBatch, GetBatch};
pub use cdc::{
    CdcCursor, CdcHlc, EdgeCreateEffect, EdgeCreateEffectItem, EdgeWrite, FullMutation,
    FullMutationOp, FullMutationStream, IdentityCategory, IdentityCheckpoint, IdentityChunk,
    IdentityEvent, IdentityStream, OriginId, Receipt, ReceiptEdgeAdd, ReceiptEdgeAddItem,
    ReceiptEdgeContributionDelete, ReceiptEdgeContributionDeleteItem, ReceiptEdgeDelete,
    ReceiptEdgeDeleteItem, ReceiptMetadata, ReceiptOriginalResult, ReceiptVertexDelete,
    ReceiptVertexDeleteItem, ReceiptVertexPut, ReceiptVertexPutItem, VertexWrite,
};
pub use contrib::{AddInput, ContribId, PreparedAdd};
pub use discovery::{
    DegreeDirection, DegreeEntry, DegreeRankingOptions, ReplicationPeer, ReplicationPeerState,
    ReplicationStatus, ServerStatus,
};
pub use error::{BatchError, CdcGap, LanternError, RpcErrorKind, RpcFailure, SearchDetails};
pub use generated::graph::v1::{
    CreateEdgeOutcome, Edge, MatchMode, Objective, PutOutcome, Reduction, ScanOrder,
    SearchCapabilities, SearchErrorReason, SearchHitProjectionStatus, SearchIndexHealth,
    SearchProjection, Vertex, Weighting, vertex::Value as VertexValue,
};
pub use paging::QueryStream;
pub use prost_types::{Duration as ProtoDuration, Timestamp};
pub use scans::{EdgeCursor, EdgeScanOptions, KeyCursor, ScanOptions, ScanPage, VertexCursor};
pub use scoped_changes::{
    ChangeCursor, ChangeFrame, ChangeInvalidation, ChangeProjection, ChangeStream,
    WatchChangesOptions,
};
pub use search::{SearchCursor, SearchHit, SearchPage, SearchRequest};
pub use transport::{
    CallOptions, LanternClient, LanternClientBuilder, RetryPolicy, StreamOptions, TokenError,
    TokenProvider,
};
pub use traversal::{
    BfsOptions, CommunityOptions, Graph, GraphEdge, PprOptions, TraversalFamily, TraversalOptions,
};
pub use value::{EdgeContributionRef, EdgeInput, EdgeRef, Expiration, VertexInput, VertexKind};

#[cfg(test)]
mod smoke;
#[cfg(test)]
mod test_server;
