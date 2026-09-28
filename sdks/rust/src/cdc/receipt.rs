use crate::{
    ContribId, Edge, EdgeContributionRef, EdgeRef, LanternError, PutOutcome, Timestamp, Vertex,
    cdc::{
        OriginId, VertexWrite,
        full::{
            check_key, checked_contribution, checked_put_outcome, checked_wire_edge_key,
            validate_edge, validate_vertex,
        },
        gap,
    },
    generated::graph::v1::{
        MutationReceipt, ReplicatedReceiptEdgeAdd, ReplicatedReceiptEdgeContributionDelete,
        ReplicatedReceiptEdgeDelete, ReplicatedReceiptVertexDelete, ReplicatedReceiptVertexPut,
        receipt_result,
    },
    value::validate_timestamp,
};

/// Receipt policy identity and optional absolute D4 deadline. These are
/// observations only, not a Rust receipt-write or recovery capability.
#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptMetadata {
    pub deployment_epoch: OriginId,
    pub policy_fingerprint: [u8; 32],
    pub tombstone_expiration: Option<Timestamp>,
}

impl ReceiptMetadata {
    fn from_wire(
        epoch: Vec<u8>,
        fingerprint: Vec<u8>,
        expiration: Option<Timestamp>,
        delete: bool,
    ) -> Result<Self, LanternError> {
        let deployment_epoch =
            OriginId::from_wire(&epoch).map_err(|_| gap("receipt deployment epoch is invalid"))?;
        let policy_fingerprint: [u8; 32] = fingerprint
            .try_into()
            .map_err(|_| gap("receipt policy fingerprint must be 32 bytes"))?;
        if policy_fingerprint.iter().all(|byte| *byte == 0) {
            return Err(gap("receipt policy fingerprint must be nonzero"));
        }
        if delete && expiration.is_none() || !delete && expiration.is_some() {
            return Err(gap("receipt tombstone deadline is missing or misplaced"));
        }
        if let Some(expiration) = &expiration {
            validate_timestamp(expiration)
                .map_err(|_| gap("invalid receipt tombstone deadline"))?;
        }
        Ok(Self {
            deployment_epoch,
            policy_fingerprint,
            tombstone_expiration: expiration,
        })
    }
}

/// An original, presence-preserving authoritative result, including false
/// Delete results and nonfinite *effective* Add weights.
#[derive(Clone, Copy, Debug, PartialEq)]
pub enum ReceiptOriginalResult {
    DeleteEdgeExisted(bool),
    PutVertexOutcome(PutOutcome),
    DeleteVertexExisted(bool),
    AddEdgeEffectiveWeight(f32),
    DeleteEdgeContributionExisted(bool),
}

#[derive(Clone, Copy)]
enum ReceiptFamily {
    EdgeDelete,
    VertexPut,
    VertexDelete,
    EdgeAdd,
    EdgeContributionDelete,
}

/// One request-index-aligned receipt with its original result. IDs remain
/// separate from a contribution's 24-byte target identity.
#[derive(Clone, Debug, PartialEq)]
pub struct Receipt {
    pub operation_id: [u8; 49],
    pub logical_call_id: [u8; 16],
    pub item_index: u32,
    pub item_count: u32,
    pub intent_sha256: [u8; 32],
    pub deadline_unix_ms: u64,
    pub original_result: ReceiptOriginalResult,
}

impl Receipt {
    fn from_wire(
        wire: Option<MutationReceipt>,
        meta: &ReceiptMetadata,
        family: ReceiptFamily,
        index: usize,
        count: usize,
        group: &mut Option<[u8; 16]>,
    ) -> Result<Self, LanternError> {
        let wire = wire.ok_or_else(|| gap("receipt envelope is missing an item receipt"))?;
        let operation_id: [u8; 49] = wire
            .operation_id
            .try_into()
            .map_err(|_| gap("receipt operation ID must be 49 bytes"))?;
        if operation_id[0] != 1
            || operation_id[1..17] != *meta.deployment_epoch.as_bytes()
            || operation_id[25..].iter().all(|byte| *byte == 0)
            || u64::from_be_bytes(operation_id[17..25].try_into().expect("8 byte range"))
                > i64::MAX as u64
        {
            return Err(gap(
                "receipt operation ID does not match the deployment epoch",
            ));
        }
        let logical_call_id: [u8; 16] = wire
            .logical_call_id
            .try_into()
            .map_err(|_| gap("receipt logical call ID must be 16 bytes"))?;
        if logical_call_id.iter().all(|byte| *byte == 0) {
            return Err(gap("receipt logical call ID is zero"));
        }
        if let Some(previous) = group {
            if previous != &logical_call_id {
                return Err(gap("receipt logical call IDs differ within one mutation"));
            }
        } else {
            *group = Some(logical_call_id);
        }
        let item_index = u32::try_from(index).map_err(|_| gap("receipt item index overflow"))?;
        let item_count = u32::try_from(count).map_err(|_| gap("receipt item count overflow"))?;
        if count == 0 || wire.item_index != item_index || wire.item_count != item_count {
            return Err(gap("receipt item index/count does not match request order"));
        }
        let intent_sha256: [u8; 32] = wire
            .intent_sha256
            .try_into()
            .map_err(|_| gap("receipt intent SHA-256 must be 32 bytes"))?;
        if wire.deadline_unix_ms == 0 || wire.deadline_unix_ms > i64::MAX as u64 {
            return Err(gap("receipt deadline is invalid"));
        }
        let result = wire
            .original_result
            .and_then(|result| result.result)
            .ok_or_else(|| gap("receipt has no original result"))?;
        let original_result = match (family, result) {
            (ReceiptFamily::EdgeDelete, receipt_result::Result::DeleteEdgeExisted(value)) => {
                ReceiptOriginalResult::DeleteEdgeExisted(value)
            }
            (ReceiptFamily::VertexPut, receipt_result::Result::PutVertexOutcome(value)) => {
                ReceiptOriginalResult::PutVertexOutcome(checked_put_outcome(value)?)
            }
            (ReceiptFamily::VertexDelete, receipt_result::Result::DeleteVertexExisted(value)) => {
                ReceiptOriginalResult::DeleteVertexExisted(value)
            }
            (ReceiptFamily::EdgeAdd, receipt_result::Result::AddEdgeEffectiveWeight(value)) => {
                ReceiptOriginalResult::AddEdgeEffectiveWeight(value)
            }
            (
                ReceiptFamily::EdgeContributionDelete,
                receipt_result::Result::DeleteEdgeContributionExisted(value),
            ) => ReceiptOriginalResult::DeleteEdgeContributionExisted(value),
            _ => return Err(gap("receipt original result has the wrong mutation family")),
        };
        Ok(Self {
            operation_id,
            logical_call_id,
            item_index,
            item_count,
            intent_sha256,
            deadline_unix_ms: wire.deadline_unix_ms,
            original_result,
        })
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptEdgeDeleteItem {
    pub key: EdgeRef,
    pub receipt: Receipt,
    pub causally_accepted: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptEdgeDelete {
    pub metadata: ReceiptMetadata,
    pub items: Vec<ReceiptEdgeDeleteItem>,
}

impl ReceiptEdgeDelete {
    pub(super) fn from_wire(wire: ReplicatedReceiptEdgeDelete) -> Result<Self, LanternError> {
        let metadata = ReceiptMetadata::from_wire(
            wire.deployment_epoch,
            wire.policy_fingerprint,
            wire.tombstone_expiration,
            true,
        )?;
        let count = wire.items.len();
        if count == 0 {
            return Err(gap("receipt Edge Delete has no original items"));
        }
        let mut group = None;
        let items = wire
            .items
            .into_iter()
            .enumerate()
            .map(|(index, item)| {
                let key = item
                    .key
                    .ok_or_else(|| gap("receipt Edge Delete item has no key"))?;
                Ok(ReceiptEdgeDeleteItem {
                    key: checked_wire_edge_key(key)?,
                    receipt: Receipt::from_wire(
                        item.receipt,
                        &metadata,
                        ReceiptFamily::EdgeDelete,
                        index,
                        count,
                        &mut group,
                    )?,
                    causally_accepted: item.causally_accepted,
                })
            })
            .collect::<Result<_, LanternError>>()?;
        Ok(Self { metadata, items })
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptVertexPutItem {
    pub original: Vertex,
    pub receipt: Receipt,
    /// None is a no-graph-effect item; Some is a live value or causal barrier.
    pub accepted: Option<VertexWrite>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptVertexPut {
    pub metadata: ReceiptMetadata,
    pub if_absent: bool,
    pub items: Vec<ReceiptVertexPutItem>,
}

impl ReceiptVertexPut {
    pub(super) fn from_wire(wire: ReplicatedReceiptVertexPut) -> Result<Self, LanternError> {
        let metadata = ReceiptMetadata::from_wire(
            wire.deployment_epoch,
            wire.policy_fingerprint,
            None,
            false,
        )?;
        let count = wire.items.len();
        if count == 0 {
            return Err(gap("receipt Vertex Put has no original items"));
        }
        let mut group = None;
        let items = wire
            .items
            .into_iter()
            .enumerate()
            .map(|(index, item)| {
                let original = item
                    .original
                    .ok_or_else(|| gap("receipt Vertex Put item has no original"))?;
                validate_vertex(&original)?;
                let accepted = item.accepted.map(VertexWrite::from_wire).transpose()?;
                if let Some(accepted) = &accepted {
                    let key = match accepted {
                        VertexWrite::Live(vertex) => &vertex.key,
                        VertexWrite::CausalBarrier(key) => key,
                    };
                    if key != &original.key {
                        return Err(gap("receipt Vertex Put accepted the wrong identity"));
                    }
                }
                Ok(ReceiptVertexPutItem {
                    original,
                    receipt: Receipt::from_wire(
                        item.receipt,
                        &metadata,
                        ReceiptFamily::VertexPut,
                        index,
                        count,
                        &mut group,
                    )?,
                    accepted,
                })
            })
            .collect::<Result<_, LanternError>>()?;
        Ok(Self {
            metadata,
            if_absent: wire.if_absent,
            items,
        })
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptVertexDeleteItem {
    pub key: String,
    pub receipt: Receipt,
    pub causally_accepted: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptVertexDelete {
    pub metadata: ReceiptMetadata,
    pub items: Vec<ReceiptVertexDeleteItem>,
}

impl ReceiptVertexDelete {
    pub(super) fn from_wire(wire: ReplicatedReceiptVertexDelete) -> Result<Self, LanternError> {
        let metadata = ReceiptMetadata::from_wire(
            wire.deployment_epoch,
            wire.policy_fingerprint,
            wire.tombstone_expiration,
            true,
        )?;
        let count = wire.items.len();
        if count == 0 {
            return Err(gap("receipt Vertex Delete has no original items"));
        }
        let mut group = None;
        let items = wire
            .items
            .into_iter()
            .enumerate()
            .map(|(index, item)| {
                check_key(&item.key)?;
                Ok(ReceiptVertexDeleteItem {
                    key: item.key,
                    receipt: Receipt::from_wire(
                        item.receipt,
                        &metadata,
                        ReceiptFamily::VertexDelete,
                        index,
                        count,
                        &mut group,
                    )?,
                    causally_accepted: item.causally_accepted,
                })
            })
            .collect::<Result<_, LanternError>>()?;
        Ok(Self { metadata, items })
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptEdgeAddItem {
    pub original: Edge,
    pub contrib_id: ContribId,
    pub receipt: Receipt,
    pub causally_accepted: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptEdgeAdd {
    pub metadata: ReceiptMetadata,
    pub items: Vec<ReceiptEdgeAddItem>,
}

impl ReceiptEdgeAdd {
    pub(super) fn from_wire(wire: ReplicatedReceiptEdgeAdd) -> Result<Self, LanternError> {
        let metadata = ReceiptMetadata::from_wire(
            wire.deployment_epoch,
            wire.policy_fingerprint,
            None,
            false,
        )?;
        let count = wire.items.len();
        if count == 0 {
            return Err(gap("receipt Edge Add has no original items"));
        }
        let mut group = None;
        let items = wire
            .items
            .into_iter()
            .enumerate()
            .map(|(index, item)| {
                let original = item
                    .original
                    .ok_or_else(|| gap("receipt Edge Add item has no original"))?;
                validate_edge(&original)?;
                Ok(ReceiptEdgeAddItem {
                    original,
                    contrib_id: ContribId::try_from(item.contrib_id.as_slice())
                        .map_err(|_| gap("receipt Edge Add has an invalid contribution ID"))?,
                    receipt: Receipt::from_wire(
                        item.receipt,
                        &metadata,
                        ReceiptFamily::EdgeAdd,
                        index,
                        count,
                        &mut group,
                    )?,
                    causally_accepted: item.causally_accepted,
                })
            })
            .collect::<Result<_, LanternError>>()?;
        Ok(Self { metadata, items })
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptEdgeContributionDeleteItem {
    /// The target Add row, distinct from the deletion's receipt operation ID.
    pub key: EdgeContributionRef,
    pub receipt: Receipt,
    pub causally_accepted: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReceiptEdgeContributionDelete {
    pub metadata: ReceiptMetadata,
    pub items: Vec<ReceiptEdgeContributionDeleteItem>,
}

impl ReceiptEdgeContributionDelete {
    pub(super) fn from_wire(
        wire: ReplicatedReceiptEdgeContributionDelete,
    ) -> Result<Self, LanternError> {
        let metadata = ReceiptMetadata::from_wire(
            wire.deployment_epoch,
            wire.policy_fingerprint,
            wire.tombstone_expiration,
            true,
        )?;
        let count = wire.items.len();
        if count == 0 {
            return Err(gap("receipt contribution Delete has no original items"));
        }
        let mut group = None;
        let items = wire
            .items
            .into_iter()
            .enumerate()
            .map(|(index, item)| {
                let key = item
                    .key
                    .ok_or_else(|| gap("receipt contribution Delete item has no target"))?;
                Ok(ReceiptEdgeContributionDeleteItem {
                    key: checked_contribution(key.tail, key.head, &key.contrib_id)?,
                    receipt: Receipt::from_wire(
                        item.receipt,
                        &metadata,
                        ReceiptFamily::EdgeContributionDelete,
                        index,
                        count,
                        &mut group,
                    )?,
                    causally_accepted: item.causally_accepted,
                })
            })
            .collect::<Result<_, LanternError>>()?;
        Ok(Self { metadata, items })
    }
}
