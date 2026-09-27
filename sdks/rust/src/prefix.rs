use crate::{
    CallOptions, LanternClient, LanternError,
    generated::graph::v1::{
        CountVerticesByPrefixRequest, DeleteEdgesByPrefixRequest, DeleteVerticesByPrefixRequest,
    },
    transport::RetryClass,
};

impl LanternClient {
    /// Count indexed vertex keys matching a prefix in one read-only RPC.
    /// Empty prefix counts the entire indexed keyspace. Like the server's
    /// prefix index, this can briefly include expired, not-yet-GC'd keys.
    pub async fn count_vertices_by_prefix(
        &self,
        prefix: impl Into<String>,
    ) -> Result<u64, LanternError> {
        self.count_vertices_by_prefix_with_options(prefix, CallOptions::Default)
            .await
    }

    pub async fn count_vertices_by_prefix_with_options(
        &self,
        prefix: impl Into<String>,
        options: CallOptions,
    ) -> Result<u64, LanternError> {
        let response = self
            .data_unary(
                CountVerticesByPrefixRequest {
                    prefix: prefix.into(),
                },
                options,
                RetryClass::ReadOnly,
                |service, request| Box::pin(service.count_vertices_by_prefix(request)),
            )
            .await?;
        Ok(response.count)
    }

    /// Delete at most one bounded page of matching vertices, or count the
    /// would-be victims with `dry_run`. An empty prefix is allowed by the
    /// server and targets the whole vertex keyspace; there is no drain loop
    /// or automatic retry, even for a dry run.
    pub async fn delete_vertices_by_prefix(
        &self,
        prefix: impl Into<String>,
        limit: u32,
        dry_run: bool,
    ) -> Result<u64, LanternError> {
        self.delete_vertices_by_prefix_with_options(prefix, limit, dry_run, CallOptions::Default)
            .await
    }

    pub async fn delete_vertices_by_prefix_with_options(
        &self,
        prefix: impl Into<String>,
        limit: u32,
        dry_run: bool,
        options: CallOptions,
    ) -> Result<u64, LanternError> {
        let response = self
            .data_unary(
                DeleteVerticesByPrefixRequest {
                    prefix: prefix.into(),
                    limit,
                    dry_run,
                },
                options,
                RetryClass::Never,
                |service, request| Box::pin(service.delete_vertices_by_prefix(request)),
            )
            .await?;
        checked_deleted(response.deleted, limit)
    }

    /// Delete at most one bounded page of live edges matching both prefixes,
    /// or count would-be victims with `dry_run`. At least one prefix must be
    /// nonempty; response loss cannot safely be retried.
    pub async fn delete_edges_by_prefix(
        &self,
        tail_prefix: impl Into<String>,
        head_prefix: impl Into<String>,
        limit: u32,
        dry_run: bool,
    ) -> Result<u64, LanternError> {
        self.delete_edges_by_prefix_with_options(
            tail_prefix,
            head_prefix,
            limit,
            dry_run,
            CallOptions::Default,
        )
        .await
    }

    pub async fn delete_edges_by_prefix_with_options(
        &self,
        tail_prefix: impl Into<String>,
        head_prefix: impl Into<String>,
        limit: u32,
        dry_run: bool,
        options: CallOptions,
    ) -> Result<u64, LanternError> {
        let tail_prefix = tail_prefix.into();
        let head_prefix = head_prefix.into();
        require_edge_prefix(&tail_prefix, &head_prefix)?;
        let response = self
            .data_unary(
                DeleteEdgesByPrefixRequest {
                    tail_prefix,
                    head_prefix,
                    limit,
                    dry_run,
                },
                options,
                RetryClass::Never,
                |service, request| Box::pin(service.delete_edges_by_prefix(request)),
            )
            .await?;
        checked_deleted(response.deleted, limit)
    }
}

fn require_edge_prefix(tail: &str, head: &str) -> Result<(), LanternError> {
    if tail.is_empty() && head.is_empty() {
        Err(LanternError::InvalidInput(
            "edge prefix delete requires a nonempty tail or head prefix",
        ))
    } else {
        Ok(())
    }
}

fn checked_deleted(deleted: u64, limit: u32) -> Result<u64, LanternError> {
    // A zero limit delegates the default/cap to the server. For a positive
    // limit the server may clamp down, but must never delete more than asked.
    if limit != 0 && deleted > u64::from(limit) {
        Err(LanternError::Protocol(
            "prefix delete count exceeds requested limit",
        ))
    } else {
        Ok(deleted)
    }
}

#[cfg(test)]
mod tests;
