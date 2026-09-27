use std::collections::HashSet;

use crate::{
    CallOptions, LanternClient, LanternError,
    generated::graph::v1::{
        GetReplicationStatusRequest, GetServerStatusRequest, TopVerticesByDegreeRequest,
        TopVerticesByDegreeResponse, top_vertices_by_degree_request,
    },
    transport::RetryClass,
};

pub use crate::generated::graph::v1::top_vertices_by_degree_response::Entry as DegreeEntry;
pub use top_vertices_by_degree_request::Direction as DegreeDirection;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DegreeRankingOptions {
    /// Required. The server does not offer whole-graph degree ranking.
    pub prefix: String,
    /// Zero selects the server's configured scan page default.
    pub k: u32,
    pub direction: DegreeDirection,
    pub weighted: bool,
}

impl DegreeRankingOptions {
    pub fn new(prefix: impl Into<String>) -> Self {
        Self {
            prefix: prefix.into(),
            k: 0,
            direction: DegreeDirection::Unspecified,
            weighted: false,
        }
    }
}

pub use crate::generated::graph::v1::{
    GetReplicationStatusResponse as ReplicationStatus, GetServerStatusResponse as ServerStatus,
    ReplicationPeer, replication_peer::State as ReplicationPeerState,
};

impl LanternClient {
    /// One bounded read. In/Both ranking may inspect all server-side edges
    /// even when the candidate vertex prefix is narrow.
    pub async fn top_vertices_by_degree(
        &self,
        options: DegreeRankingOptions,
    ) -> Result<Vec<DegreeEntry>, LanternError> {
        self.top_vertices_by_degree_with_options(options, CallOptions::Default)
            .await
    }

    pub async fn top_vertices_by_degree_with_options(
        &self,
        options: DegreeRankingOptions,
        call_options: CallOptions,
    ) -> Result<Vec<DegreeEntry>, LanternError> {
        if options.prefix.is_empty() {
            return Err(LanternError::InvalidInput(
                "degree ranking requires a nonempty vertex prefix",
            ));
        }
        let response = self
            .data_unary(
                TopVerticesByDegreeRequest {
                    prefix: options.prefix.clone(),
                    k: options.k,
                    direction: options.direction.into(),
                    weighted: options.weighted,
                },
                call_options,
                RetryClass::ReadOnly,
                |svc, req| Box::pin(svc.top_vertices_by_degree(req)),
            )
            .await?;
        validate_ranking(&options, response)
    }

    /// Take an explicit status snapshot. No polling or background task starts.
    pub async fn server_status(&self) -> Result<ServerStatus, LanternError> {
        self.server_status_with_options(CallOptions::Default).await
    }

    pub async fn server_status_with_options(
        &self,
        options: CallOptions,
    ) -> Result<ServerStatus, LanternError> {
        self.data_unary(
            GetServerStatusRequest {},
            options,
            RetryClass::ReadOnly,
            |svc, req| Box::pin(svc.get_server_status(req)),
        )
        .await
    }

    /// Take an explicit local replication snapshot, including the
    /// `enabled=false` single-instance case.
    pub async fn replication_status(&self) -> Result<ReplicationStatus, LanternError> {
        self.replication_status_with_options(CallOptions::Default)
            .await
    }

    pub async fn replication_status_with_options(
        &self,
        options: CallOptions,
    ) -> Result<ReplicationStatus, LanternError> {
        self.data_unary(
            GetReplicationStatusRequest {},
            options,
            RetryClass::ReadOnly,
            |svc, req| Box::pin(svc.get_replication_status(req)),
        )
        .await
    }
}

fn validate_ranking(
    options: &DegreeRankingOptions,
    response: TopVerticesByDegreeResponse,
) -> Result<Vec<DegreeEntry>, LanternError> {
    let mut keys = HashSet::with_capacity(response.entries.len());
    if options.k != 0 && response.entries.len() > options.k as usize {
        return Err(LanternError::Protocol(
            "degree ranking returned more entries than requested",
        ));
    }
    for entry in &response.entries {
        if !entry.key.starts_with(&options.prefix) || !keys.insert(&entry.key) {
            return Err(LanternError::Protocol(
                "degree ranking returned a duplicate or out-of-prefix key",
            ));
        }
    }
    Ok(response.entries)
}

#[cfg(test)]
mod tests;
