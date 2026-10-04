use crate::{
    ContribId, Edge, EdgeContributionRef, EdgeRef, LanternError, PutOutcome, Timestamp, Vertex,
    cdc::{
        CdcCursor, CdcHlc, OriginId, ReceiptEdgeAdd, ReceiptEdgeContributionDelete,
        ReceiptEdgeDelete, ReceiptVertexDelete, ReceiptVertexPut, SubscriptionSource, decode_frame,
        gap, require_frame,
    },
    generated::graph::v1::{
        EdgeKey, Mutation, ReplicatedPutEdge, ReplicatedPutVertex, mutation_op::Op,
        replicated_put_edge, replicated_put_vertex, subscribe_response::Event,
    },
    value::{validate_key, validate_timestamp},
};

/// A live Vertex or the accepted-expired causal barrier for that identity.
#[derive(Clone, Debug, PartialEq)]
pub enum VertexWrite {
    Live(Vertex),
    CausalBarrier(String),
}

impl VertexWrite {
    pub(super) fn from_wire(value: ReplicatedPutVertex) -> Result<Self, LanternError> {
        match value
            .outcome
            .ok_or_else(|| gap("missing replicated Vertex outcome"))?
        {
            replicated_put_vertex::Outcome::Live(vertex) => {
                validate_vertex(&vertex)?;
                Ok(Self::Live(vertex))
            }
            replicated_put_vertex::Outcome::CausalBarrier(barrier) => {
                check_key(&barrier.key)?;
                Ok(Self::CausalBarrier(barrier.key))
            }
        }
    }
}

/// A live Edge or an accepted-expired Edge causal barrier.
#[derive(Clone, Debug, PartialEq)]
pub enum EdgeWrite {
    Live(Edge),
    CausalBarrier(EdgeRef),
}

impl EdgeWrite {
    fn from_wire(value: ReplicatedPutEdge) -> Result<Self, LanternError> {
        match value
            .outcome
            .ok_or_else(|| gap("missing replicated Edge outcome"))?
        {
            replicated_put_edge::Outcome::Live(edge) => {
                validate_edge(&edge)?;
                Ok(Self::Live(edge))
            }
            replicated_put_edge::Outcome::CausalBarrier(barrier) => Ok(Self::CausalBarrier(
                checked_edge_key(barrier.tail, barrier.head)?,
            )),
        }
    }
}

/// Every currently defined (22-arm) MutationOp payload, with no generated
/// service-client/request-envelope type exposed to the application. Original
/// Add ID vectors retain their wire length and their empty versus zero IDs.
#[derive(Clone, Debug, PartialEq)]
pub enum FullMutationOp {
    EdgeCreateEffect(super::receipt::EdgeCreateEffect),
    PutVertex {
        vertex: Vertex,
        if_absent: bool,
    },
    PutVertices {
        vertices: Vec<Vertex>,
        if_absent: bool,
    },
    DeleteVertex(String),
    DeleteVertices(Vec<String>),
    DeleteVerticesByPrefix {
        prefix: String,
        limit: u32,
        dry_run: bool,
    },
    AddEdge {
        edge: Edge,
        contrib_id: Vec<u8>,
    },
    AddEdges {
        edges: Vec<Edge>,
        contrib_ids: Vec<Vec<u8>>,
    },
    PutEdge(Edge),
    PutEdges(Vec<Edge>),
    DeleteEdge(EdgeRef),
    DeleteEdges(Vec<EdgeRef>),
    DeleteEdgesByPrefix {
        tail_prefix: String,
        head_prefix: String,
        limit: u32,
        dry_run: bool,
    },
    ReplicatedPutVertices(Vec<VertexWrite>),
    ReplicatedPutEdges(Vec<EdgeWrite>),
    ReceiptEdgeDelete(ReceiptEdgeDelete),
    ReceiptVertexPut(ReceiptVertexPut),
    ReceiptVertexDelete(ReceiptVertexDelete),
    ReceiptEdgeAdd(ReceiptEdgeAdd),
    DeleteEdgeContribution(EdgeContributionRef),
    DeleteEdgeContributions(Vec<EdgeContributionRef>),
    ReceiptEdgeContributionDelete(ReceiptEdgeContributionDelete),
}

impl FullMutationOp {
    fn from_wire(value: Op) -> Result<Self, LanternError> {
        Ok(match value {
            Op::EdgeCreateEffect(effect) => {
                Self::EdgeCreateEffect(super::receipt::EdgeCreateEffect::from_wire(effect)?)
            }
            Op::PutVertex(request) => {
                no_receipt(&request.receipt_context)?;
                Self::PutVertex {
                    vertex: checked_vertex(request.vertex)?,
                    if_absent: request.if_absent,
                }
            }
            Op::PutVertices(request) => {
                no_receipt(&request.receipt_context)?;
                Self::PutVertices {
                    vertices: request
                        .vertices
                        .into_iter()
                        .map(|vertex| checked_vertex(Some(vertex)))
                        .collect::<Result<_, _>>()?,
                    if_absent: request.if_absent,
                }
            }
            Op::DeleteVertex(request) => {
                no_receipt(&request.receipt_context)?;
                check_key(&request.key)?;
                Self::DeleteVertex(request.key)
            }
            Op::DeleteVertices(request) => {
                no_receipt(&request.receipt_context)?;
                check_keys(&request.keys)?;
                Self::DeleteVertices(request.keys)
            }
            Op::DeleteVerticesByPrefix(request) => Self::DeleteVerticesByPrefix {
                prefix: request.prefix,
                limit: request.limit,
                dry_run: request.dry_run,
            },
            Op::AddEdge(request) => {
                no_receipt(&request.receipt_context)?;
                validate_optional_contrib_id(&request.contrib_id)?;
                Self::AddEdge {
                    edge: checked_edge(request.edge)?,
                    contrib_id: request.contrib_id,
                }
            }
            Op::AddEdges(request) => {
                no_receipt(&request.receipt_context)?;
                if request.contrib_ids.len() > request.edges.len() {
                    return Err(gap("AddEdges has more contribution IDs than edges"));
                }
                for id in &request.contrib_ids {
                    validate_optional_contrib_id(id)?;
                }
                Self::AddEdges {
                    edges: request
                        .edges
                        .into_iter()
                        .map(|edge| checked_edge(Some(edge)))
                        .collect::<Result<_, _>>()?,
                    contrib_ids: request.contrib_ids,
                }
            }
            Op::PutEdge(request) => Self::PutEdge(checked_edge(request.edge)?),
            Op::PutEdges(request) => Self::PutEdges(
                request
                    .edges
                    .into_iter()
                    .map(|edge| checked_edge(Some(edge)))
                    .collect::<Result<_, _>>()?,
            ),
            Op::DeleteEdge(request) => {
                no_receipt(&request.receipt_context)?;
                Self::DeleteEdge(checked_edge_key(request.tail, request.head)?)
            }
            Op::DeleteEdges(request) => {
                no_receipt(&request.receipt_context)?;
                Self::DeleteEdges(
                    request
                        .edges
                        .into_iter()
                        .map(checked_wire_edge_key)
                        .collect::<Result<_, _>>()?,
                )
            }
            Op::DeleteEdgesByPrefix(request) => Self::DeleteEdgesByPrefix {
                tail_prefix: request.tail_prefix,
                head_prefix: request.head_prefix,
                limit: request.limit,
                dry_run: request.dry_run,
            },
            Op::ReplicatedPutVertices(request) => Self::ReplicatedPutVertices(
                request
                    .entries
                    .into_iter()
                    .map(VertexWrite::from_wire)
                    .collect::<Result<_, _>>()?,
            ),
            Op::ReplicatedPutEdges(request) => Self::ReplicatedPutEdges(
                request
                    .entries
                    .into_iter()
                    .map(EdgeWrite::from_wire)
                    .collect::<Result<_, _>>()?,
            ),
            Op::ReplicatedReceiptEdgeDelete(envelope) => {
                Self::ReceiptEdgeDelete(ReceiptEdgeDelete::from_wire(envelope)?)
            }
            Op::ReplicatedReceiptVertexPut(envelope) => {
                Self::ReceiptVertexPut(ReceiptVertexPut::from_wire(envelope)?)
            }
            Op::ReplicatedReceiptVertexDelete(envelope) => {
                Self::ReceiptVertexDelete(ReceiptVertexDelete::from_wire(envelope)?)
            }
            Op::ReplicatedReceiptEdgeAdd(envelope) => {
                Self::ReceiptEdgeAdd(ReceiptEdgeAdd::from_wire(envelope)?)
            }
            Op::DeleteEdgeContribution(request) => {
                no_receipt(&request.receipt_context)?;
                Self::DeleteEdgeContribution(checked_contribution(
                    request.tail,
                    request.head,
                    &request.contrib_id,
                )?)
            }
            Op::DeleteEdgeContributions(request) => {
                no_receipt(&request.receipt_context)?;
                Self::DeleteEdgeContributions(
                    request
                        .contributions
                        .into_iter()
                        .map(|key| checked_contribution(key.tail, key.head, &key.contrib_id))
                        .collect::<Result<_, _>>()?,
                )
            }
            Op::ReplicatedReceiptEdgeContributionDelete(envelope) => {
                Self::ReceiptEdgeContributionDelete(ReceiptEdgeContributionDelete::from_wire(
                    envelope,
                )?)
            }
        })
    }
}

/// One complete, sequenced mutation. Persist the decoded operation and its
/// next cursor in the same application transaction; never persist the cursor
/// first. A full stream does not offer bootstrap or all-history guarantees.
#[derive(Clone, Debug, PartialEq)]
pub struct FullMutation {
    pub origin: OriginId,
    pub seq: u64,
    pub hlc: CdcHlc,
    pub tombstone_expiration: Option<Timestamp>,
    pub op: FullMutationOp,
    pub next_cursor: CdcCursor,
}

pub struct FullMutationStream {
    wire: Option<SubscriptionSource>,
    cursor: CdcCursor,
}

impl FullMutationStream {
    pub(super) fn new(wire: SubscriptionSource, cursor: CdcCursor) -> Self {
        Self {
            wire: Some(wire),
            cursor,
        }
    }

    pub async fn next_mutation(&mut self) -> Result<FullMutation, LanternError> {
        let result = self.read_mutation().await;
        if result.is_err() {
            self.wire = None;
        }
        result
    }

    async fn read_mutation(&mut self) -> Result<FullMutation, LanternError> {
        let raw = require_frame(
            self.wire
                .as_mut()
                .ok_or_else(|| gap("full-mutation stream has ended"))?
                .next()
                .await?,
            "full-mutation stream ended unexpectedly",
        )?;
        let frame = decode_frame(&raw)?;
        let Event::Mutation(mutation) = frame
            .event
            .ok_or_else(|| gap("full-mutation frame is empty"))?
        else {
            return Err(gap("full-mutation stream received an identity frame"));
        };
        self.decode_mutation(mutation)
    }

    fn decode_mutation(&mut self, mutation: Mutation) -> Result<FullMutation, LanternError> {
        let origin = OriginId::from_wire(&mutation.origin)?;
        if mutation.seq == 0 || mutation.seq != self.cursor.next_expected(origin) {
            return Err(gap("full-mutation sequence is duplicate or gapped"));
        }
        let hlc = CdcHlc::from_wire(mutation.hlc, origin)?;
        let op = mutation
            .op
            .and_then(|op| op.op)
            .ok_or_else(|| gap("full mutation has no known operation arm"))?;
        let op = FullMutationOp::from_wire(op)?;
        if let Some(expiration) = &mutation.tombstone_expiration {
            validate_timestamp(expiration).map_err(|_| gap("invalid tombstone deadline"))?;
        }
        let next_cursor = self.cursor.complete(origin, mutation.seq)?;
        Ok(FullMutation {
            origin,
            seq: mutation.seq,
            hlc,
            tombstone_expiration: mutation.tombstone_expiration,
            op,
            next_cursor,
        })
    }
}

pub(super) fn check_key(key: &str) -> Result<(), LanternError> {
    validate_key(key).map_err(|_| gap("mutation has an empty identity"))
}

pub(super) fn check_keys(keys: &[String]) -> Result<(), LanternError> {
    for key in keys {
        check_key(key)?;
    }
    Ok(())
}

pub(super) fn validate_vertex(vertex: &Vertex) -> Result<(), LanternError> {
    vertex
        .validate_response()
        .map_err(|_| gap("mutation contains an invalid Vertex"))
}

pub(super) fn validate_edge(edge: &Edge) -> Result<(), LanternError> {
    edge.validate_response()
        .map_err(|_| gap("mutation contains an invalid Edge"))?;
    if !edge.weight.is_finite() {
        return Err(gap("mutation contains a nonfinite source Edge weight"));
    }
    Ok(())
}

fn checked_vertex(vertex: Option<Vertex>) -> Result<Vertex, LanternError> {
    let vertex = vertex.ok_or_else(|| gap("mutation is missing its Vertex"))?;
    validate_vertex(&vertex)?;
    Ok(vertex)
}

fn checked_edge(edge: Option<Edge>) -> Result<Edge, LanternError> {
    let edge = edge.ok_or_else(|| gap("mutation is missing its Edge"))?;
    validate_edge(&edge)?;
    Ok(edge)
}

pub(super) fn checked_wire_edge_key(key: EdgeKey) -> Result<EdgeRef, LanternError> {
    checked_edge_key(key.tail, key.head)
}

pub(super) fn checked_edge_key(tail: String, head: String) -> Result<EdgeRef, LanternError> {
    check_key(&tail)?;
    check_key(&head)?;
    Ok(EdgeRef::new(tail, head))
}

pub(super) fn checked_contribution(
    tail: String,
    head: String,
    raw_id: &[u8],
) -> Result<EdgeContributionRef, LanternError> {
    let edge = checked_edge_key(tail, head)?;
    let id = ContribId::try_from(raw_id)
        .map_err(|_| gap("contribution Delete requires a nonzero 24-byte ID"))?;
    Ok(EdgeContributionRef::new(edge.tail, edge.head, id))
}

fn validate_optional_contrib_id(raw: &[u8]) -> Result<(), LanternError> {
    if !raw.is_empty() && raw.len() != 24 {
        return Err(gap("Add contribution ID has an invalid length"));
    }
    Ok(())
}

fn no_receipt<T>(context: &Option<T>) -> Result<(), LanternError> {
    if context.is_some() {
        return Err(gap(
            "graph-only mutation unexpectedly contains receipt context",
        ));
    }
    Ok(())
}

pub(super) fn checked_put_outcome(value: i32) -> Result<PutOutcome, LanternError> {
    match PutOutcome::try_from(value) {
        Ok(outcome) if outcome != PutOutcome::Unspecified => Ok(outcome),
        _ => Err(gap("receipt contains unknown or unspecified Put outcome")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        VertexValue,
        generated::graph::v1::{
            AddEdgeRequest, AddEdgesRequest, DeleteEdgeContributionRequest,
            DeleteEdgeContributionsRequest, DeleteEdgeRequest, DeleteEdgesByPrefixRequest,
            DeleteEdgesRequest, DeleteVertexRequest, DeleteVerticesByPrefixRequest,
            DeleteVerticesRequest, HlcTimestamp, MutationOp, MutationReceipt, PutEdgeRequest,
            PutEdgesRequest, PutVertexRequest, PutVerticesRequest, ReceiptResult,
            ReplicatedPutEdges, ReplicatedPutVertices, ReplicatedReceiptEdgeAdd as WireReceiptAdd,
            ReplicatedReceiptEdgeAddItem,
            ReplicatedReceiptEdgeContributionDelete as WireReceiptContribDelete,
            ReplicatedReceiptEdgeContributionDeleteItem,
            ReplicatedReceiptEdgeDelete as WireReceiptEdgeDelete, ReplicatedReceiptEdgeDeleteItem,
            ReplicatedReceiptVertexDelete as WireReceiptVertexDelete,
            ReplicatedReceiptVertexDeleteItem, ReplicatedReceiptVertexPut as WireReceiptVertexPut,
            ReplicatedReceiptVertexPutItem, receipt_result,
        },
    };

    fn origin() -> OriginId {
        OriginId::new([0x47; 16]).unwrap()
    }

    fn vertex() -> Vertex {
        Vertex {
            key: "rust:vertex".into(),
            value: Some(VertexValue::Float64(-0.0)),
            expiration: Some(Timestamp {
                seconds: 2_000_000_000,
                nanos: 123,
            }),
        }
    }

    fn edge() -> Edge {
        Edge {
            tail: "rust:tail".into(),
            head: "rust:head".into(),
            weight: 2.5,
            expiration: vertex().expiration,
        }
    }

    fn key() -> EdgeKey {
        EdgeKey {
            tail: "rust:tail".into(),
            head: "rust:head".into(),
        }
    }

    fn receipt(result: receipt_result::Result) -> MutationReceipt {
        let mut operation_id = vec![0; 49];
        operation_id[0] = 1;
        operation_id[1..17].copy_from_slice(origin().as_bytes());
        operation_id[25] = 1;
        MutationReceipt {
            operation_id,
            logical_call_id: vec![0x31; 16],
            item_index: 0,
            item_count: 1,
            intent_sha256: vec![1; 32],
            deadline_unix_ms: 2_000_000_000_000,
            original_result: Some(ReceiptResult {
                result: Some(result),
            }),
        }
    }

    fn expiration() -> Option<Timestamp> {
        Some(Timestamp {
            seconds: 2_000_000_000,
            nanos: 0,
        })
    }

    fn valid_arms() -> Vec<Op> {
        let epoch = origin().as_bytes().to_vec();
        let fingerprint = vec![9; 32];
        let contribution = vec![3; 24];
        vec![
            Op::PutVertex(PutVertexRequest {
                vertex: Some(vertex()),
                if_absent: true,
                receipt_context: None,
            }),
            Op::PutVertices(PutVerticesRequest {
                vertices: vec![vertex()],
                if_absent: false,
                receipt_context: None,
            }),
            Op::DeleteVertex(DeleteVertexRequest {
                key: vertex().key,
                receipt_context: None,
            }),
            Op::DeleteVertices(DeleteVerticesRequest {
                keys: vec!["rust:vertex".into()],
                receipt_context: None,
            }),
            Op::DeleteVerticesByPrefix(DeleteVerticesByPrefixRequest {
                prefix: "rust:".into(),
                limit: 1,
                dry_run: false,
            }),
            Op::AddEdge(AddEdgeRequest {
                edge: Some(edge()),
                contrib_id: contribution.clone(),
                receipt_context: None,
            }),
            Op::AddEdges(AddEdgesRequest {
                edges: vec![edge()],
                contrib_ids: vec![vec![0; 24]],
                receipt_context: None,
            }),
            Op::PutEdge(PutEdgeRequest { edge: Some(edge()) }),
            Op::PutEdges(PutEdgesRequest {
                edges: vec![edge()],
            }),
            Op::DeleteEdge(DeleteEdgeRequest {
                tail: "rust:tail".into(),
                head: "rust:head".into(),
                receipt_context: None,
            }),
            Op::DeleteEdges(DeleteEdgesRequest {
                edges: vec![key()],
                receipt_context: None,
            }),
            Op::DeleteEdgesByPrefix(DeleteEdgesByPrefixRequest {
                tail_prefix: "rust:".into(),
                head_prefix: "rust:".into(),
                limit: 1,
                dry_run: false,
            }),
            Op::ReplicatedPutVertices(ReplicatedPutVertices {
                entries: vec![ReplicatedPutVertex {
                    outcome: Some(replicated_put_vertex::Outcome::Live(vertex())),
                }],
            }),
            Op::ReplicatedPutEdges(ReplicatedPutEdges {
                entries: vec![ReplicatedPutEdge {
                    outcome: Some(replicated_put_edge::Outcome::Live(edge())),
                }],
            }),
            Op::ReplicatedReceiptEdgeDelete(WireReceiptEdgeDelete {
                deployment_epoch: epoch.clone(),
                policy_fingerprint: fingerprint.clone(),
                tombstone_expiration: expiration(),
                items: vec![ReplicatedReceiptEdgeDeleteItem {
                    key: Some(key()),
                    receipt: Some(receipt(receipt_result::Result::DeleteEdgeExisted(false))),
                    causally_accepted: true,
                }],
            }),
            Op::ReplicatedReceiptVertexPut(WireReceiptVertexPut {
                deployment_epoch: epoch.clone(),
                policy_fingerprint: fingerprint.clone(),
                if_absent: true,
                items: vec![ReplicatedReceiptVertexPutItem {
                    original: Some(vertex()),
                    receipt: Some(receipt(receipt_result::Result::PutVertexOutcome(
                        PutOutcome::ConditionNotMet as i32,
                    ))),
                    accepted: None,
                    lifecycle_reduced: false,
                }],
            }),
            Op::ReplicatedReceiptVertexDelete(WireReceiptVertexDelete {
                deployment_epoch: epoch.clone(),
                policy_fingerprint: fingerprint.clone(),
                tombstone_expiration: expiration(),
                items: vec![ReplicatedReceiptVertexDeleteItem {
                    key: "rust:vertex".into(),
                    receipt: Some(receipt(receipt_result::Result::DeleteVertexExisted(false))),
                    causally_accepted: true,
                }],
            }),
            Op::ReplicatedReceiptEdgeAdd(WireReceiptAdd {
                deployment_epoch: epoch.clone(),
                policy_fingerprint: fingerprint.clone(),
                items: vec![ReplicatedReceiptEdgeAddItem {
                    original: Some(edge()),
                    contrib_id: contribution.clone(),
                    receipt: Some(receipt(receipt_result::Result::AddEdgeEffectiveWeight(
                        f32::INFINITY,
                    ))),
                    causally_accepted: true,
                }],
            }),
            Op::DeleteEdgeContribution(DeleteEdgeContributionRequest {
                tail: "rust:tail".into(),
                head: "rust:head".into(),
                contrib_id: contribution.clone(),
                receipt_context: None,
            }),
            Op::DeleteEdgeContributions(DeleteEdgeContributionsRequest {
                contributions: vec![crate::generated::graph::v1::EdgeContributionKey {
                    tail: "rust:tail".into(),
                    head: "rust:head".into(),
                    contrib_id: contribution.clone(),
                }],
                receipt_context: None,
            }),
            Op::ReplicatedReceiptEdgeContributionDelete(WireReceiptContribDelete {
                deployment_epoch: epoch,
                policy_fingerprint: fingerprint,
                tombstone_expiration: expiration(),
                items: vec![ReplicatedReceiptEdgeContributionDeleteItem {
                    key: Some(crate::generated::graph::v1::EdgeContributionKey {
                        tail: "rust:tail".into(),
                        head: "rust:head".into(),
                        contrib_id: contribution,
                    }),
                    receipt: Some(receipt(
                        receipt_result::Result::DeleteEdgeContributionExisted(false),
                    )),
                    causally_accepted: true,
                }],
            }),
            Op::EdgeCreateEffect(crate::generated::graph::v1::EdgeCreateEffect {
                items: vec![crate::generated::graph::v1::EdgeCreateEffectItem {
                    original: Some(edge()),
                    outcome: crate::CreateEdgeOutcome::CreatedAndLive as i32,
                    receipt: None,
                }],
                deployment_epoch: vec![],
                policy_fingerprint: vec![],
            }),
        ]
    }

    fn mutation(op: Op) -> Mutation {
        Mutation {
            seq: 1,
            hlc: Some(HlcTimestamp {
                wall_ns: 2_000_000_000_000,
                logical: 7,
                node_id: origin().as_bytes().to_vec(),
            }),
            origin: origin().as_bytes().to_vec(),
            op: Some(MutationOp {
                op: Some(op),
                ..Default::default()
            }),
            tombstone_expiration: None,
            namespace_format: String::new(),
        }
    }

    fn stream() -> FullMutationStream {
        FullMutationStream {
            wire: None,
            cursor: CdcCursor::new(),
        }
    }

    #[test]
    fn all_twenty_two_arms_preserve_distinctions_and_advance_once() {
        let arms = valid_arms();
        assert_eq!(arms.len(), 22);
        for (index, arm) in arms.into_iter().enumerate() {
            let event = stream().decode_mutation(mutation(arm)).unwrap();
            assert_eq!(event.origin, origin(), "oneof arm {index}");
            assert_eq!(event.hlc.logical, 7);
            assert_eq!(event.next_cursor.next_expected(origin()), 2);
            assert_eq!(arm_number(&event.op), index + 1, "oneof arm {index}");
            if let FullMutationOp::AddEdges { contrib_ids, .. } = &event.op {
                assert_eq!(contrib_ids, &[vec![0; 24]]);
            }
            if let FullMutationOp::ReceiptEdgeAdd(envelope) = event.op {
                assert!(matches!(
                    envelope.items[0].receipt.original_result,
                    crate::ReceiptOriginalResult::AddEdgeEffectiveWeight(weight)
                    if weight == f32::INFINITY
                ));
            }
        }
    }

    fn arm_number(op: &FullMutationOp) -> usize {
        match op {
            FullMutationOp::PutVertex { .. } => 1,
            FullMutationOp::PutVertices { .. } => 2,
            FullMutationOp::DeleteVertex(_) => 3,
            FullMutationOp::DeleteVertices(_) => 4,
            FullMutationOp::DeleteVerticesByPrefix { .. } => 5,
            FullMutationOp::AddEdge { .. } => 6,
            FullMutationOp::AddEdges { .. } => 7,
            FullMutationOp::PutEdge(_) => 8,
            FullMutationOp::PutEdges(_) => 9,
            FullMutationOp::DeleteEdge(_) => 10,
            FullMutationOp::DeleteEdges(_) => 11,
            FullMutationOp::DeleteEdgesByPrefix { .. } => 12,
            FullMutationOp::ReplicatedPutVertices(_) => 13,
            FullMutationOp::ReplicatedPutEdges(_) => 14,
            FullMutationOp::ReceiptEdgeDelete(_) => 15,
            FullMutationOp::ReceiptVertexPut(_) => 16,
            FullMutationOp::ReceiptVertexDelete(_) => 17,
            FullMutationOp::ReceiptEdgeAdd(_) => 18,
            FullMutationOp::DeleteEdgeContribution(_) => 19,
            FullMutationOp::DeleteEdgeContributions(_) => 20,
            FullMutationOp::ReceiptEdgeContributionDelete(_) => 21,
            FullMutationOp::EdgeCreateEffect(_) => 22,
        }
    }

    #[test]
    fn malformed_create_evidence_never_advances_the_cursor() {
        let valid = valid_arms().pop().unwrap();
        let Op::EdgeCreateEffect(valid) = valid else {
            panic!("Create arm missing")
        };
        let mut variants = Vec::new();
        let mut bad = valid.clone();
        bad.items[0].outcome = 0;
        variants.push(bad);
        let mut bad = valid.clone();
        bad.items[0].outcome = 999;
        variants.push(bad);
        let mut bad = valid.clone();
        bad.items[0].original = None;
        variants.push(bad);
        let mut bad = valid.clone();
        bad.items[0].original.as_mut().unwrap().weight = 0.0;
        variants.push(bad);
        let mut bad = valid.clone();
        bad.items[0].original.as_mut().unwrap().weight = f32::NAN;
        variants.push(bad);
        let mut bad = valid.clone();
        bad.items.push(bad.items[0].clone());
        variants.push(bad);
        let mut bad = valid.clone();
        bad.deployment_epoch = origin().as_bytes().to_vec();
        variants.push(bad);
        let mut bad = valid.clone();
        bad.items[0].receipt = Some(receipt(receipt_result::Result::CreateEdgeOutcome(1)));
        variants.push(bad);
        let mut bad = valid.clone();
        bad.deployment_epoch = origin().as_bytes().to_vec();
        bad.policy_fingerprint = vec![9; 32];
        variants.push(bad);
        for bad in variants {
            let mut stream = stream();
            assert!(matches!(
                stream.decode_mutation(mutation(Op::EdgeCreateEffect(bad))),
                Err(LanternError::CdcGap(_))
            ));
            assert_eq!(stream.cursor.next_expected(origin()), 1);
        }
        let mut accepted = valid;
        accepted.deployment_epoch = origin().as_bytes().to_vec();
        accepted.policy_fingerprint = vec![9; 32];
        accepted.items[0].receipt = Some(receipt(receipt_result::Result::CreateEdgeOutcome(1)));
        assert!(
            stream()
                .decode_mutation(mutation(Op::EdgeCreateEffect(accepted.clone())))
                .is_ok()
        );
        accepted.items[0]
            .receipt
            .as_mut()
            .unwrap()
            .original_result
            .as_mut()
            .unwrap()
            .result = Some(receipt_result::Result::CreateEdgeOutcome(2));
        assert!(
            stream()
                .decode_mutation(mutation(Op::EdgeCreateEffect(accepted)))
                .is_err()
        );
    }

    #[test]
    fn full_gap_invalid_receipt_and_unknown_op_never_advance_cursor() {
        let mut stream = stream();
        let original = mutation(valid_arms()[14].clone());
        let mut invalid = original.clone();
        if let Some(MutationOp {
            op: Some(Op::ReplicatedReceiptEdgeDelete(envelope)),
            ..
        }) = &mut invalid.op
        {
            envelope.items[0].receipt = None;
        }
        assert!(matches!(
            stream.decode_mutation(invalid),
            Err(LanternError::CdcGap(_))
        ));
        assert_eq!(stream.cursor.next_expected(origin()), 1);

        let mut invalid = original;
        invalid.seq = 2;
        assert!(matches!(
            stream.decode_mutation(invalid),
            Err(LanternError::CdcGap(_))
        ));
        assert_eq!(stream.cursor.next_expected(origin()), 1);

        let mut invalid = mutation(valid_arms()[18].clone());
        invalid.op = Some(MutationOp::default());
        assert!(matches!(
            stream.decode_mutation(invalid),
            Err(LanternError::CdcGap(_))
        ));
        let mut invalid = mutation(valid_arms()[19].clone());
        if let Some(MutationOp {
            op: Some(Op::DeleteEdgeContributions(request)),
            ..
        }) = &mut invalid.op
        {
            request.contributions[0].contrib_id = vec![0; 24];
        }
        assert!(matches!(
            stream.decode_mutation(invalid),
            Err(LanternError::CdcGap(_))
        ));
    }
}
