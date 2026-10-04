use std::{
    collections::{HashMap, HashSet},
    ops::Range,
    time::SystemTime,
};

use crate::{
    AddBatch, AddInput, BatchError, CallOptions, CreateEdgeOutcome, DeleteBatch, Edge,
    EdgeContributionRef, EdgeInput, EdgeRef, GetBatch, LanternClient, LanternError, PreparedAdd,
    PutOutcome, Vertex, VertexInput,
    batch::chunk_plan,
    generated::graph::v1::{
        AddEdgesRequest, AddEdgesResponse, CreateEdgesRequest, DeleteEdgeContributionsRequest,
        DeleteEdgeContributionsResponse, DeleteEdgesRequest, DeleteEdgesResponse,
        DeleteVerticesRequest, DeleteVerticesResponse, EdgeContributionKey, EdgeKey,
        GetEdgesRequest, GetEdgesResponse, GetVerticesRequest, GetVerticesResponse,
        PutEdgesRequest, PutVerticesRequest,
    },
    mutation::accepted_undisclosed,
    transport::RetryClass,
    value::{locally_expired, validate_key},
};

impl LanternClient {
    /// Fetch an unordered found/missing multiset. Duplicate requested keys
    /// remain distinct occurrences, not deduplicated map entries.
    pub async fn get_vertices<I, K>(
        &self,
        keys: I,
    ) -> Result<GetBatch<Vertex, String>, LanternError>
    where
        I: IntoIterator<Item = K>,
        K: Into<String>,
    {
        self.get_vertices_with_options(keys, CallOptions::Default)
            .await
    }

    pub async fn get_vertices_with_options<I, K>(
        &self,
        keys: I,
        options: CallOptions,
    ) -> Result<GetBatch<Vertex, String>, LanternError>
    where
        I: IntoIterator<Item = K>,
        K: Into<String>,
    {
        let deadline = self.deadline_for(options)?;
        let keys = collect_bounded(keys.into_iter().map(Into::into))?;
        for key in &keys {
            validate_key(key).map_err(LanternError::InvalidInput)?;
        }
        let make = |range: Range<usize>| GetVerticesRequest {
            keys: keys[range].to_vec(),
        };
        let ranges = chunk_plan(
            keys.len(),
            self.batch_chunk_size(),
            self.encode_limit(),
            make,
        )?;
        let mut found = Vec::new();
        let mut missing = Vec::new();
        for range in ranges {
            let response = self
                .data_unary_at(
                    make(range.clone()),
                    deadline,
                    RetryClass::ReadOnly,
                    |svc, req| Box::pin(svc.get_vertices(req)),
                )
                .await?;
            validate_get_vertices(&keys[range], &response)?;
            found.extend(response.vertices);
            missing.extend(response.missing);
        }
        Ok(GetBatch { found, missing })
    }

    /// A one-key facade over `get_vertices`; a present explicit nil is not
    /// missing and a missing key returns the typed `LanternError::NotFound`.
    pub async fn get_vertex(&self, key: impl Into<String>) -> Result<Vertex, LanternError> {
        self.get_vertex_with_options(key, CallOptions::Default)
            .await
    }

    pub async fn get_vertex_with_options(
        &self,
        key: impl Into<String>,
        options: CallOptions,
    ) -> Result<Vertex, LanternError> {
        let result = self
            .get_vertices_with_options([key.into()], options)
            .await?;
        match (result.found.len(), result.missing.len()) {
            (1, 0) => only(result.found),
            (0, 1) => Err(LanternError::NotFound),
            _ => Err(LanternError::Protocol("invalid single-vertex read result")),
        }
    }

    /// Unconditional replacement, retried only on classified UNAVAILABLE
    /// when configured. All relative TTLs resolve before the first attempt.
    pub async fn put_vertices<I>(&self, vertices: I) -> Result<Vec<PutOutcome>, LanternError>
    where
        I: IntoIterator<Item = VertexInput>,
    {
        self.put_vertices_with_options(vertices, CallOptions::Default)
            .await
    }

    pub async fn put_vertices_with_options<I>(
        &self,
        vertices: I,
        options: CallOptions,
    ) -> Result<Vec<PutOutcome>, LanternError>
    where
        I: IntoIterator<Item = VertexInput>,
    {
        self.put_vertices_inner(vertices, false, options).await
    }

    /// Conditional replacement is never automatically retried: a response
    /// loss may hide an applied write and another attempt may reject it.
    pub async fn put_vertices_if_absent<I>(
        &self,
        vertices: I,
    ) -> Result<Vec<PutOutcome>, LanternError>
    where
        I: IntoIterator<Item = VertexInput>,
    {
        self.put_vertices_if_absent_with_options(vertices, CallOptions::Default)
            .await
    }

    pub async fn put_vertices_if_absent_with_options<I>(
        &self,
        vertices: I,
        options: CallOptions,
    ) -> Result<Vec<PutOutcome>, LanternError>
    where
        I: IntoIterator<Item = VertexInput>,
    {
        self.put_vertices_inner(vertices, true, options).await
    }

    async fn put_vertices_inner<I>(
        &self,
        vertices: I,
        if_absent: bool,
        options: CallOptions,
    ) -> Result<Vec<PutOutcome>, LanternError>
    where
        I: IntoIterator<Item = VertexInput>,
    {
        let deadline = self.deadline_for(options)?;
        let inputs = collect_bounded(vertices)?;
        let now = SystemTime::now();
        let vertices: Vec<Vertex> = inputs
            .into_iter()
            .map(|input| input.into_wire(now))
            .collect::<Result<_, _>>()?;
        let make = |range: Range<usize>| PutVerticesRequest {
            vertices: vertices[range].to_vec(),
            if_absent,
            receipt_context: None,
        };
        let ranges = chunk_plan(
            vertices.len(),
            self.batch_chunk_size(),
            self.encode_limit(),
            make,
        )?;
        let retry = if if_absent {
            RetryClass::Never
        } else {
            RetryClass::UnconditionalPut
        };
        let mut outcomes = Vec::with_capacity(vertices.len());
        for range in ranges {
            let response = self
                .data_unary_at(make(range.clone()), deadline, retry, |svc, req| {
                    Box::pin(svc.put_vertices(req))
                })
                .await
                .map_err(|source| failed_chunk(range.start, source))?;
            let chunk = validate_put_outcomes(&vertices[range.clone()], response.outcomes)
                .map_err(|source| failed_chunk(range.start, source))?;
            outcomes.extend(chunk);
        }
        Ok(outcomes)
    }

    pub async fn put_vertex(&self, vertex: VertexInput) -> Result<PutOutcome, LanternError> {
        self.put_vertex_with_options(vertex, CallOptions::Default)
            .await
    }

    pub async fn put_vertex_with_options(
        &self,
        vertex: VertexInput,
        options: CallOptions,
    ) -> Result<PutOutcome, LanternError> {
        only(self.put_vertices_with_options([vertex], options).await?)
    }

    pub async fn put_vertex_if_absent(
        &self,
        vertex: VertexInput,
    ) -> Result<PutOutcome, LanternError> {
        self.put_vertex_if_absent_with_options(vertex, CallOptions::Default)
            .await
    }

    pub async fn put_vertex_if_absent_with_options(
        &self,
        vertex: VertexInput,
        options: CallOptions,
    ) -> Result<PutOutcome, LanternError> {
        only(
            self.put_vertices_if_absent_with_options([vertex], options)
                .await?,
        )
    }

    /// Each Delete `existed` entry observes its own request index, even for
    /// duplicate keys. A lost response cannot be safely interpreted as false.
    pub async fn delete_vertices<I, K>(&self, keys: I) -> Result<DeleteBatch, LanternError>
    where
        I: IntoIterator<Item = K>,
        K: Into<String>,
    {
        self.delete_vertices_with_options(keys, CallOptions::Default)
            .await
    }

    pub async fn delete_vertices_with_options<I, K>(
        &self,
        keys: I,
        options: CallOptions,
    ) -> Result<DeleteBatch, LanternError>
    where
        I: IntoIterator<Item = K>,
        K: Into<String>,
    {
        let deadline = self.deadline_for(options)?;
        let keys = collect_bounded(keys.into_iter().map(Into::into))?;
        for key in &keys {
            validate_key(key).map_err(LanternError::InvalidInput)?;
        }
        let make = |range: Range<usize>| DeleteVerticesRequest {
            keys: keys[range].to_vec(),
            receipt_context: None,
        };
        let ranges = chunk_plan(
            keys.len(),
            self.batch_chunk_size(),
            self.encode_limit(),
            make,
        )?;
        let mut existed = Vec::with_capacity(keys.len());
        let mut deleted = 0;
        for range in ranges {
            let response = self
                .data_unary_at(
                    make(range.clone()),
                    deadline,
                    RetryClass::Never,
                    |svc, req| Box::pin(svc.delete_vertices(req)),
                )
                .await
                .map_err(|source| failed_chunk(range.start, source))?;
            let chunk = validate_delete_vertices(range.len(), response)
                .map_err(|source| failed_chunk(range.start, source))?;
            deleted += chunk.deleted;
            existed.extend(chunk.existed);
        }
        Ok(DeleteBatch { deleted, existed })
    }

    pub async fn delete_vertex(&self, key: impl Into<String>) -> Result<bool, LanternError> {
        self.delete_vertex_with_options(key, CallOptions::Default)
            .await
    }

    pub async fn delete_vertex_with_options(
        &self,
        key: impl Into<String>,
        options: CallOptions,
    ) -> Result<bool, LanternError> {
        only(
            self.delete_vertices_with_options([key.into()], options)
                .await?
                .existed,
        )
    }

    pub async fn get_edges<I>(&self, edges: I) -> Result<GetBatch<Edge, EdgeRef>, LanternError>
    where
        I: IntoIterator<Item = EdgeRef>,
    {
        self.get_edges_with_options(edges, CallOptions::Default)
            .await
    }

    pub async fn get_edges_with_options<I>(
        &self,
        edges: I,
        options: CallOptions,
    ) -> Result<GetBatch<Edge, EdgeRef>, LanternError>
    where
        I: IntoIterator<Item = EdgeRef>,
    {
        let deadline = self.deadline_for(options)?;
        let edges = collect_bounded(edges)?;
        for edge in &edges {
            edge.validate()?;
        }
        let make = |range: Range<usize>| GetEdgesRequest {
            edges: edges[range].iter().map(wire_ref).collect(),
        };
        let ranges = chunk_plan(
            edges.len(),
            self.batch_chunk_size(),
            self.encode_limit(),
            make,
        )?;
        let mut found = Vec::new();
        let mut missing = Vec::new();
        for range in ranges {
            let response = self
                .data_unary_at(
                    make(range.clone()),
                    deadline,
                    RetryClass::ReadOnly,
                    |svc, req| Box::pin(svc.get_edges(req)),
                )
                .await?;
            validate_get_edges(&edges[range], &response)?;
            found.extend(response.edges);
            missing.extend(
                response
                    .missing
                    .into_iter()
                    .map(|pair| EdgeRef::new(pair.tail, pair.head)),
            );
        }
        Ok(GetBatch { found, missing })
    }

    pub async fn get_edge(
        &self,
        tail: impl Into<String>,
        head: impl Into<String>,
    ) -> Result<Edge, LanternError> {
        self.get_edge_with_options(tail, head, CallOptions::Default)
            .await
    }

    pub async fn get_edge_with_options(
        &self,
        tail: impl Into<String>,
        head: impl Into<String>,
        options: CallOptions,
    ) -> Result<Edge, LanternError> {
        let result = self
            .get_edges_with_options([EdgeRef::new(tail, head)], options)
            .await?;
        match (result.found.len(), result.missing.len()) {
            (1, 0) => only(result.found),
            (0, 1) => Err(LanternError::NotFound),
            _ => Err(LanternError::Protocol("invalid single-edge read result")),
        }
    }

    /// Creates only between existing live endpoints. Failed chunks are never retried.
    pub async fn create_edges<I>(&self, edges: I) -> Result<Vec<CreateEdgeOutcome>, LanternError>
    where
        I: IntoIterator<Item = EdgeInput>,
    {
        self.create_edges_with_options(edges, CallOptions::Default)
            .await
    }
    pub async fn create_edges_with_options<I>(
        &self,
        edges: I,
        options: CallOptions,
    ) -> Result<Vec<CreateEdgeOutcome>, LanternError>
    where
        I: IntoIterator<Item = EdgeInput>,
    {
        let deadline = self.deadline_for(options)?;
        let now = SystemTime::now();
        let edges: Vec<Edge> = collect_bounded(edges)?
            .into_iter()
            .map(|input| input.into_wire(now))
            .collect::<Result<_, _>>()?;
        if edges
            .iter()
            .any(|edge| edge.weight == 0.0 || !edge.weight.is_finite())
        {
            return Err(LanternError::InvalidInput(
                "Create requires a finite nonzero weight",
            ));
        }
        let make = |range: Range<usize>| CreateEdgesRequest {
            edges: edges[range].to_vec(),
            receipt_context: None,
        };
        let ranges = chunk_plan(
            edges.len(),
            self.batch_chunk_size(),
            self.encode_limit(),
            make,
        )?;
        let mut outcomes = Vec::with_capacity(edges.len());
        let mut undisclosed = false;
        for range in ranges {
            let response = self
                .data_unary_at(
                    make(range.clone()),
                    deadline,
                    RetryClass::Never,
                    |svc, req| Box::pin(svc.create_edges(req)),
                )
                .await
                .map_err(|source| failed_chunk(range.start, source))?;
            if accepted_undisclosed(response.acceptance.as_ref(), !response.outcomes.is_empty())
                .map_err(|source| failed_chunk(range.start, source))?
            {
                undisclosed = true;
                continue;
            }
            if response.outcomes.len() != range.len() {
                return Err(failed_chunk(
                    range.start,
                    LanternError::Protocol("misaligned Create outcomes"),
                ));
            }
            let decoded = response
                .outcomes
                .into_iter()
                .map(|value| {
                    CreateEdgeOutcome::try_from(value)
                        .ok()
                        .filter(|outcome| *outcome != CreateEdgeOutcome::Unspecified)
                        .ok_or_else(|| LanternError::Protocol("invalid Create outcome"))
                })
                .collect::<Result<Vec<_>, _>>()
                .map_err(|source| failed_chunk(range.start, source))?;
            outcomes.extend(decoded);
        }
        if undisclosed {
            return Err(LanternError::MutationAcceptedUndisclosed);
        }
        Ok(outcomes)
    }
    pub async fn create_edge(&self, edge: EdgeInput) -> Result<CreateEdgeOutcome, LanternError> {
        self.create_edge_with_options(edge, CallOptions::Default)
            .await
    }

    pub async fn create_edge_with_options(
        &self,
        edge: EdgeInput,
        options: CallOptions,
    ) -> Result<CreateEdgeOutcome, LanternError> {
        only(self.create_edges_with_options([edge], options).await?)
    }

    /// Idempotent replacement of each edge's weight and expiration. This
    /// does not accumulate contributions; use `add_edges` for additive writes.
    pub async fn put_edges<I>(&self, edges: I) -> Result<Vec<PutOutcome>, LanternError>
    where
        I: IntoIterator<Item = EdgeInput>,
    {
        self.put_edges_with_options(edges, CallOptions::Default)
            .await
    }

    pub async fn put_edges_with_options<I>(
        &self,
        edges: I,
        options: CallOptions,
    ) -> Result<Vec<PutOutcome>, LanternError>
    where
        I: IntoIterator<Item = EdgeInput>,
    {
        let deadline = self.deadline_for(options)?;
        let inputs = collect_bounded(edges)?;
        let now = SystemTime::now();
        let edges: Vec<Edge> = inputs
            .into_iter()
            .map(|input| input.into_wire(now))
            .collect::<Result<_, _>>()?;
        let make = |range: Range<usize>| PutEdgesRequest {
            edges: edges[range].to_vec(),
        };
        let ranges = chunk_plan(
            edges.len(),
            self.batch_chunk_size(),
            self.encode_limit(),
            make,
        )?;
        let mut outcomes = Vec::with_capacity(edges.len());
        let mut undisclosed = false;
        for range in ranges {
            let response = self
                .data_unary_at(
                    make(range.clone()),
                    deadline,
                    RetryClass::UnconditionalPut,
                    |svc, req| Box::pin(svc.put_edges(req)),
                )
                .await
                .map_err(|source| failed_chunk(range.start, source))?;
            if accepted_undisclosed(response.acceptance.as_ref(), !response.outcomes.is_empty())
                .map_err(|source| failed_chunk(range.start, source))?
            {
                undisclosed = true;
                continue;
            }
            let chunk = validate_edge_put_outcomes(&edges[range.clone()], response.outcomes)
                .map_err(|source| failed_chunk(range.start, source))?;
            outcomes.extend(chunk);
        }
        if undisclosed {
            return Err(LanternError::MutationAcceptedUndisclosed);
        }
        Ok(outcomes)
    }

    pub async fn put_edge(&self, edge: EdgeInput) -> Result<PutOutcome, LanternError> {
        self.put_edge_with_options(edge, CallOptions::Default).await
    }

    pub async fn put_edge_with_options(
        &self,
        edge: EdgeInput,
        options: CallOptions,
    ) -> Result<PutOutcome, LanternError> {
        only(self.put_edges_with_options([edge], options).await?)
    }

    pub async fn delete_edges<I>(&self, edges: I) -> Result<DeleteBatch, LanternError>
    where
        I: IntoIterator<Item = EdgeRef>,
    {
        self.delete_edges_with_options(edges, CallOptions::Default)
            .await
    }

    pub async fn delete_edges_with_options<I>(
        &self,
        edges: I,
        options: CallOptions,
    ) -> Result<DeleteBatch, LanternError>
    where
        I: IntoIterator<Item = EdgeRef>,
    {
        let deadline = self.deadline_for(options)?;
        let edges = collect_bounded(edges)?;
        for edge in &edges {
            edge.validate()?;
        }
        let make = |range: Range<usize>| DeleteEdgesRequest {
            edges: edges[range].iter().map(wire_ref).collect(),
            receipt_context: None,
        };
        let ranges = chunk_plan(
            edges.len(),
            self.batch_chunk_size(),
            self.encode_limit(),
            make,
        )?;
        let mut existed = Vec::with_capacity(edges.len());
        let mut deleted = 0;
        let mut undisclosed = false;
        for range in ranges {
            let response = self
                .data_unary_at(
                    make(range.clone()),
                    deadline,
                    RetryClass::Never,
                    |svc, req| Box::pin(svc.delete_edges(req)),
                )
                .await
                .map_err(|source| failed_chunk(range.start, source))?;
            if accepted_undisclosed(
                response.acceptance.as_ref(),
                response.deleted != 0 || !response.existed.is_empty(),
            )
            .map_err(|source| failed_chunk(range.start, source))?
            {
                undisclosed = true;
                continue;
            }
            let chunk = validate_delete_edges(range.len(), response)
                .map_err(|source| failed_chunk(range.start, source))?;
            deleted += chunk.deleted;
            existed.extend(chunk.existed);
        }
        if undisclosed {
            return Err(LanternError::MutationAcceptedUndisclosed);
        }
        Ok(DeleteBatch { deleted, existed })
    }

    pub async fn delete_edge(
        &self,
        tail: impl Into<String>,
        head: impl Into<String>,
    ) -> Result<bool, LanternError> {
        self.delete_edge_with_options(tail, head, CallOptions::Default)
            .await
    }

    pub async fn delete_edge_with_options(
        &self,
        tail: impl Into<String>,
        head: impl Into<String>,
        options: CallOptions,
    ) -> Result<bool, LanternError> {
        only(
            self.delete_edges_with_options([EdgeRef::new(tail, head)], options)
                .await?
                .existed,
        )
    }

    /// Remove individual Add rows, preserving other contributions and any
    /// Put base. Results are request-index aligned, including duplicate
    /// triples and missing or expired IDs. No receipt-less response loss is
    /// replayed automatically, even when a retry policy is configured.
    pub async fn delete_edge_contributions<I>(
        &self,
        contributions: I,
    ) -> Result<DeleteBatch, LanternError>
    where
        I: IntoIterator<Item = EdgeContributionRef>,
    {
        self.delete_edge_contributions_with_options(contributions, CallOptions::Default)
            .await
    }

    pub async fn delete_edge_contributions_with_options<I>(
        &self,
        contributions: I,
        options: CallOptions,
    ) -> Result<DeleteBatch, LanternError>
    where
        I: IntoIterator<Item = EdgeContributionRef>,
    {
        let deadline = self.deadline_for(options)?;
        let contributions = collect_bounded(contributions)?;
        for contribution in &contributions {
            contribution.validate()?;
        }
        let make = |range: Range<usize>| DeleteEdgeContributionsRequest {
            contributions: contributions[range]
                .iter()
                .map(|ref_| EdgeContributionKey {
                    tail: ref_.tail.clone(),
                    head: ref_.head.clone(),
                    contrib_id: ref_.contrib_id.as_bytes().to_vec(),
                })
                .collect(),
            receipt_context: None,
        };
        let ranges = chunk_plan(
            contributions.len(),
            self.batch_chunk_size(),
            self.encode_limit(),
            make,
        )?;
        let mut existed = Vec::with_capacity(contributions.len());
        let mut deleted = 0;
        let mut undisclosed = false;
        for range in ranges {
            let response = self
                .data_unary_at(
                    make(range.clone()),
                    deadline,
                    RetryClass::Never,
                    |svc, req| Box::pin(svc.delete_edge_contributions(req)),
                )
                .await
                .map_err(|source| failed_chunk(range.start, source))?;
            if accepted_undisclosed(
                response.acceptance.as_ref(),
                response.deleted != 0 || !response.existed.is_empty(),
            )
            .map_err(|source| failed_chunk(range.start, source))?
            {
                undisclosed = true;
                continue;
            }
            let chunk = validate_delete_contributions(range.len(), response)
                .map_err(|source| failed_chunk(range.start, source))?;
            deleted += chunk.deleted;
            existed.extend(chunk.existed);
        }
        if undisclosed {
            return Err(LanternError::MutationAcceptedUndisclosed);
        }
        Ok(DeleteBatch { deleted, existed })
    }

    /// One-item facade over `delete_edge_contributions`; `delete_edge`
    /// remains the separate whole-edge operation.
    pub async fn delete_edge_contribution(
        &self,
        contribution: EdgeContributionRef,
    ) -> Result<bool, LanternError> {
        self.delete_edge_contribution_with_options(contribution, CallOptions::Default)
            .await
    }

    pub async fn delete_edge_contribution_with_options(
        &self,
        contribution: EdgeContributionRef,
        options: CallOptions,
    ) -> Result<bool, LanternError> {
        only(
            self.delete_edge_contributions_with_options([contribution], options)
                .await?
                .existed,
        )
    }

    /// Resolve expiration and mint optional IDs exactly once before sending.
    /// The immutable snapshot lets callers retain IDs for manual decisions
    /// after an uncertain response; it is not a receipt/replay guarantee.
    pub fn prepare_add<I, A>(&self, inputs: I) -> Result<PreparedAdd, LanternError>
    where
        I: IntoIterator<Item = A>,
        A: Into<AddInput>,
    {
        let inputs = collect_bounded(inputs.into_iter().map(Into::into))?;
        let now = SystemTime::now();
        let mut edges = Vec::with_capacity(inputs.len());
        let mut contrib_ids = Vec::with_capacity(inputs.len());
        let mut missing = Vec::new();
        let mut seen = HashSet::new();
        for (index, input) in inputs.into_iter().enumerate() {
            edges.push(input.edge.into_wire(now)?);
            if let Some(id) = input.contrib_id {
                if !seen.insert(id) {
                    return Err(LanternError::InvalidInput(
                        "duplicate contribution ID within logical Add",
                    ));
                }
            } else {
                missing.push(index);
            }
            contrib_ids.push(input.contrib_id);
        }
        if let Some(generated) = self.mint_contribution_ids(&missing)? {
            for (index, id) in missing.into_iter().zip(generated) {
                if !seen.insert(id) {
                    return Err(LanternError::InvalidInput(
                        "duplicate contribution ID within logical Add",
                    ));
                }
                contrib_ids[index] = Some(id);
            }
        }
        Ok(PreparedAdd { edges, contrib_ids })
    }

    /// Additive, receipt-less contributions. No automatic retry, including
    /// when IDs were generated or supplied: Delete/expiry ends deduplication.
    pub async fn add_edges<I, A>(&self, inputs: I) -> Result<AddBatch, LanternError>
    where
        I: IntoIterator<Item = A>,
        A: Into<AddInput>,
    {
        self.add_edges_with_options(inputs, CallOptions::Default)
            .await
    }

    pub async fn add_edges_with_options<I, A>(
        &self,
        inputs: I,
        options: CallOptions,
    ) -> Result<AddBatch, LanternError>
    where
        I: IntoIterator<Item = A>,
        A: Into<AddInput>,
    {
        let deadline = self.deadline_for(options)?;
        let prepared = self.prepare_add(inputs)?;
        self.add_prepared_at(&prepared, deadline).await
    }

    pub async fn add_prepared_edges(
        &self,
        prepared: &PreparedAdd,
    ) -> Result<AddBatch, LanternError> {
        self.add_prepared_edges_with_options(prepared, CallOptions::Default)
            .await
    }

    pub async fn add_prepared_edges_with_options(
        &self,
        prepared: &PreparedAdd,
        options: CallOptions,
    ) -> Result<AddBatch, LanternError> {
        self.add_prepared_at(prepared, self.deadline_for(options)?)
            .await
    }

    async fn add_prepared_at(
        &self,
        prepared: &PreparedAdd,
        deadline: Option<tokio::time::Instant>,
    ) -> Result<AddBatch, LanternError> {
        let make = |range: Range<usize>| wire_add(prepared, range);
        let ranges = chunk_plan(
            prepared.len(),
            self.batch_chunk_size(),
            self.encode_limit(),
            make,
        )?;
        let mut written = 0;
        let mut effective_weights = Vec::with_capacity(prepared.len());
        let mut undisclosed = false;
        for range in ranges {
            let response = self
                .data_unary_at(
                    make(range.clone()),
                    deadline,
                    RetryClass::Never,
                    |svc, req| Box::pin(svc.add_edges(req)),
                )
                .await
                .map_err(|source| failed_chunk(range.start, source))?;
            if accepted_undisclosed(
                response.acceptance.as_ref(),
                response.written != 0 || !response.effective_weights.is_empty(),
            )
            .map_err(|source| failed_chunk(range.start, source))?
            {
                undisclosed = true;
                continue;
            }
            let chunk = validate_add(range.len(), response)
                .map_err(|source| failed_chunk(range.start, source))?;
            written += chunk.written;
            effective_weights.extend(chunk.effective_weights);
        }
        if undisclosed {
            return Err(LanternError::MutationAcceptedUndisclosed);
        }
        Ok(AddBatch {
            written,
            effective_weights,
        })
    }

    pub async fn add_edge(&self, input: impl Into<AddInput>) -> Result<f32, LanternError> {
        self.add_edge_with_options(input, CallOptions::Default)
            .await
    }

    pub async fn add_edge_with_options(
        &self,
        input: impl Into<AddInput>,
        options: CallOptions,
    ) -> Result<f32, LanternError> {
        only(
            self.add_edges_with_options([input.into()], options)
                .await?
                .effective_weights,
        )
    }
}

fn only<T>(values: Vec<T>) -> Result<T, LanternError> {
    let [value]: [T; 1] = values
        .try_into()
        .map_err(|_| LanternError::Protocol("singular facade expected exactly one result"))?;
    Ok(value)
}

fn collect_bounded<T>(items: impl IntoIterator<Item = T>) -> Result<Vec<T>, LanternError> {
    let mut result = Vec::new();
    for item in items {
        if result.len() == crate::contrib::MAX_LOGICAL_ITEMS {
            return Err(LanternError::InvalidInput(
                "logical batch exceeds 65536 items",
            ));
        }
        result.push(item);
    }
    Ok(result)
}

fn failed_chunk(completed_items: usize, source: LanternError) -> LanternError {
    LanternError::Batch(BatchError {
        completed_items,
        source: Box::new(source),
    })
}

fn wire_ref(pair: &EdgeRef) -> EdgeKey {
    EdgeKey {
        tail: pair.tail.clone(),
        head: pair.head.clone(),
    }
}

fn wire_add(prepared: &PreparedAdd, range: Range<usize>) -> AddEdgesRequest {
    let ids: Vec<Vec<u8>> = prepared.contrib_ids[range.clone()]
        .iter()
        .map(|id| id.map_or_else(Vec::new, |id| id.as_bytes().to_vec()))
        .collect();
    let contrib_ids = if ids.iter().all(Vec::is_empty) {
        Vec::new()
    } else {
        ids
    };
    AddEdgesRequest {
        edges: prepared.edges[range].to_vec(),
        contrib_ids,
        receipt_context: None,
    }
}

fn validate_get_vertices(
    keys: &[String],
    response: &GetVerticesResponse,
) -> Result<(), LanternError> {
    let mut expected = HashMap::new();
    for key in keys {
        *expected.entry(key.clone()).or_insert(0_usize) += 1;
    }
    for vertex in &response.vertices {
        vertex.validate_response()?;
        consume(&mut expected, &vertex.key)?;
    }
    for key in &response.missing {
        validate_key(key).map_err(LanternError::Protocol)?;
        consume(&mut expected, key)?;
    }
    if expected.values().any(|count| *count != 0) {
        return Err(LanternError::Protocol(
            "GetVertices response omitted requested identities",
        ));
    }
    Ok(())
}

fn validate_get_edges(edges: &[EdgeRef], response: &GetEdgesResponse) -> Result<(), LanternError> {
    let mut expected = HashMap::new();
    for edge in edges {
        *expected.entry(edge.clone()).or_insert(0_usize) += 1;
    }
    for edge in &response.edges {
        edge.validate_response()?;
        consume(&mut expected, &EdgeRef::new(&edge.tail, &edge.head))?;
    }
    for missing in &response.missing {
        validate_key(&missing.tail).map_err(LanternError::Protocol)?;
        validate_key(&missing.head).map_err(LanternError::Protocol)?;
        consume(&mut expected, &EdgeRef::new(&missing.tail, &missing.head))?;
    }
    if expected.values().any(|count| *count != 0) {
        return Err(LanternError::Protocol(
            "GetEdges response omitted requested identities",
        ));
    }
    Ok(())
}

fn consume<K: Eq + std::hash::Hash>(
    expected: &mut HashMap<K, usize>,
    key: &K,
) -> Result<(), LanternError> {
    let Some(count) = expected.get_mut(key) else {
        return Err(LanternError::Protocol(
            "Get response contained an unrequested identity",
        ));
    };
    if *count == 0 {
        return Err(LanternError::Protocol(
            "Get response repeated an identity too many times",
        ));
    }
    *count -= 1;
    Ok(())
}

fn validate_put_outcomes(
    items: &[Vertex],
    outcomes: Vec<i32>,
) -> Result<Vec<PutOutcome>, LanternError> {
    validate_outcomes(items.len(), outcomes, |index| &items[index].expiration)
}

fn validate_edge_put_outcomes(
    items: &[Edge],
    outcomes: Vec<i32>,
) -> Result<Vec<PutOutcome>, LanternError> {
    validate_outcomes(items.len(), outcomes, |index| &items[index].expiration)
}

fn validate_outcomes<'a>(
    expected: usize,
    outcomes: Vec<i32>,
    expiration: impl Fn(usize) -> &'a Option<crate::Timestamp>,
) -> Result<Vec<PutOutcome>, LanternError> {
    if outcomes.len() != expected {
        return Err(LanternError::Protocol(
            "Put outcome vector length differs from request",
        ));
    }
    outcomes
        .into_iter()
        .enumerate()
        .map(|(index, wire)| {
            let outcome = PutOutcome::try_from(wire)
                .map_err(|_| LanternError::Protocol("unknown Put outcome"))?;
            if outcome == PutOutcome::Unspecified {
                return Err(LanternError::Protocol("unspecified Put outcome"));
            }
            if outcome == PutOutcome::AppliedAndLive && locally_expired(expiration(index))? {
                Ok(PutOutcome::Expired)
            } else {
                Ok(outcome)
            }
        })
        .collect()
}

fn validate_delete_vertices(
    expected: usize,
    response: DeleteVerticesResponse,
) -> Result<DeleteBatch, LanternError> {
    validate_delete(expected, response.deleted, response.existed)
}

fn validate_delete_edges(
    expected: usize,
    response: DeleteEdgesResponse,
) -> Result<DeleteBatch, LanternError> {
    validate_delete(expected, response.deleted, response.existed)
}

fn validate_delete_contributions(
    expected: usize,
    response: DeleteEdgeContributionsResponse,
) -> Result<DeleteBatch, LanternError> {
    validate_delete(expected, response.deleted, response.existed)
}

fn validate_delete(
    expected: usize,
    deleted: i32,
    existed: Vec<bool>,
) -> Result<DeleteBatch, LanternError> {
    if existed.len() != expected
        || deleted < 0
        || deleted as usize != existed.iter().filter(|item| **item).count()
    {
        return Err(LanternError::Protocol(
            "Delete count or existed vector differs from request",
        ));
    }
    Ok(DeleteBatch {
        deleted: deleted as usize,
        existed,
    })
}

fn validate_add(expected: usize, response: AddEdgesResponse) -> Result<AddBatch, LanternError> {
    if response.effective_weights.len() != expected
        || response.written < 0
        || response.written as usize > expected
    {
        return Err(LanternError::Protocol(
            "Add count or effective-weights vector differs from request",
        ));
    }
    Ok(AddBatch {
        written: response.written as usize,
        effective_weights: response.effective_weights,
    })
}

#[cfg(test)]
mod tests;
