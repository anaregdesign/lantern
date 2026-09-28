use std::collections::BTreeMap;

use crate::{
    EdgeRef, LanternError,
    cdc::{
        CdcCursor, CdcHlc, IDENTITY_CHUNK_ITEMS, IDENTITY_FRAME_LIMIT, OriginId,
        SubscriptionSource, decode_frame, gap, require_frame,
    },
    generated::graph::v1::{
        IdentityCheckpoint as WireCheckpoint, IdentityChunk as WireChunk, IdentityOperation,
        subscribe_response::Event,
    },
    value::validate_key,
};

/// The exact kind of invalidation. A contribution Delete (wire category 7)
/// requires re-reading its edge; it does not assert whole-edge deletion.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[non_exhaustive]
pub enum IdentityCategory {
    PutVertex,
    DeleteVertex,
    AddEdge,
    PutEdge,
    DeleteEdge,
    ReceiptOnly,
    DeleteEdgeContribution,
}

impl TryFrom<i32> for IdentityCategory {
    type Error = LanternError;

    fn try_from(value: i32) -> Result<Self, Self::Error> {
        match IdentityOperation::try_from(value) {
            Ok(IdentityOperation::PutVertex) => Ok(Self::PutVertex),
            Ok(IdentityOperation::DeleteVertex) => Ok(Self::DeleteVertex),
            Ok(IdentityOperation::AddEdge) => Ok(Self::AddEdge),
            Ok(IdentityOperation::PutEdge) => Ok(Self::PutEdge),
            Ok(IdentityOperation::DeleteEdge) => Ok(Self::DeleteEdge),
            Ok(IdentityOperation::ReceiptOnly) => Ok(Self::ReceiptOnly),
            Ok(IdentityOperation::DeleteEdgeContribution) => Ok(Self::DeleteEdgeContribution),
            _ => Err(gap("unknown or unspecified identity operation category")),
        }
    }
}

/// A selected responder's publication cut, not a cluster-wide freshness
/// guarantee. Revalidate resident identities before persisting this cut.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct IdentityCheckpoint {
    pub last_seq_per_origin: BTreeMap<OriginId, u64>,
}

impl IdentityCheckpoint {
    /// Cursor usable *after* the application finishes resident-key
    /// revalidation against this responder and drains its registered tail.
    pub fn cursor_after_revalidation(&self) -> Result<CdcCursor, LanternError> {
        let mut cursor = CdcCursor::new();
        for (&origin, &last) in &self.last_seq_per_origin {
            let next = last
                .checked_add(1)
                .ok_or_else(|| gap("checkpoint sequence overflow"))?;
            cursor.insert_next(origin, next)?;
        }
        Ok(cursor)
    }
}

impl TryFrom<WireCheckpoint> for IdentityCheckpoint {
    type Error = LanternError;

    fn try_from(value: WireCheckpoint) -> Result<Self, Self::Error> {
        let mut last_seq_per_origin = BTreeMap::new();
        for (key, last) in value.last_seq_per_origin {
            let origin = key
                .parse()
                .map_err(|_| gap("checkpoint has noncanonical origin"))?;
            if last_seq_per_origin.insert(origin, last).is_some() {
                return Err(gap("checkpoint has duplicate origin"));
            }
        }
        let checkpoint = Self {
            last_seq_per_origin,
        };
        checkpoint.cursor_after_revalidation()?;
        Ok(checkpoint)
    }
}

/// A bounded, value-free fragment. The caller applies invalidation and
/// `next_cursor` **atomically**; only the final fragment has a cursor.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct IdentityChunk {
    pub origin: OriginId,
    pub seq: u64,
    pub hlc: CdcHlc,
    pub category: IdentityCategory,
    pub chunk_index: u32,
    pub first_item_index: u32,
    pub vertex_keys: Vec<String>,
    pub edge_keys: Vec<EdgeRef>,
    pub is_last: bool,
    pub next_cursor: Option<CdcCursor>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum IdentityEvent {
    Checkpoint(IdentityCheckpoint),
    Chunk(IdentityChunk),
}

struct Pending {
    origin: OriginId,
    seq: u64,
    hlc: CdcHlc,
    category: IdentityCategory,
    next_chunk_index: u32,
    next_item_index: u32,
}

/// A true gRPC server stream with no buffered page or automatic restart.
pub struct IdentityStream {
    wire: Option<SubscriptionSource>,
    cursor: CdcCursor,
    waiting_for_checkpoint: bool,
    pending: Option<Pending>,
}

impl IdentityStream {
    pub(crate) fn new(wire: SubscriptionSource, cursor: CdcCursor, bootstrap: bool) -> Self {
        Self {
            wire: Some(wire),
            cursor,
            waiting_for_checkpoint: bootstrap,
            pending: None,
        }
    }
}

impl IdentityStream {
    /// Wait for one checked event; an interrupted or malformed stream is a
    /// typed gap, never a clean end-of-history or implicit cursor reset.
    pub async fn next_event(&mut self) -> Result<IdentityEvent, LanternError> {
        let result = self.read_event().await;
        if result.is_err() {
            self.wire = None;
        }
        result
    }

    async fn read_event(&mut self) -> Result<IdentityEvent, LanternError> {
        let wire = self
            .wire
            .as_mut()
            .ok_or_else(|| gap("identity stream has ended"))?;
        let raw = require_frame(
            wire.next().await?,
            "identity stream ended without a terminal boundary",
        )?;
        if raw.len() > IDENTITY_FRAME_LIMIT {
            return Err(gap("identity frame exceeds 1 MiB"));
        }
        let frame = decode_frame(&raw)?;
        self.process_frame(frame)
    }

    fn process_frame(
        &mut self,
        frame: crate::generated::graph::v1::SubscribeResponse,
    ) -> Result<IdentityEvent, LanternError> {
        if self.waiting_for_checkpoint {
            let Event::Checkpoint(checkpoint) = frame
                .event
                .ok_or_else(|| gap("empty identity bootstrap frame"))?
            else {
                return Err(gap("identity bootstrap did not begin with a checkpoint"));
            };
            let checkpoint = IdentityCheckpoint::try_from(checkpoint)?;
            self.cursor = checkpoint.cursor_after_revalidation()?;
            self.waiting_for_checkpoint = false;
            return Ok(IdentityEvent::Checkpoint(checkpoint));
        }
        let Event::IdentityChunk(chunk) = frame
            .event
            .ok_or_else(|| gap("empty identity stream frame"))?
        else {
            return Err(gap(
                "identity stream received a checkpoint or full mutation",
            ));
        };
        self.decode_chunk(chunk).map(IdentityEvent::Chunk)
    }

    fn decode_chunk(&mut self, chunk: WireChunk) -> Result<IdentityChunk, LanternError> {
        let origin = OriginId::from_wire(&chunk.origin)?;
        if chunk.seq == 0 || chunk.seq != self.cursor.next_expected(origin) {
            return Err(gap("identity mutation sequence is duplicate or gapped"));
        }
        let hlc = CdcHlc::from_wire(chunk.hlc, origin)?;
        let category = IdentityCategory::try_from(chunk.operation)?;
        let count = chunk.vertex_keys.len() + chunk.edge_keys.len();
        if count > IDENTITY_CHUNK_ITEMS {
            return Err(gap("identity chunk exceeds 1024 keys"));
        }
        if count == 0 && !chunk.is_last {
            return Err(gap("non-final identity chunk is empty"));
        }
        if matches!(category, IdentityCategory::ReceiptOnly) && count != 0 {
            return Err(gap("receipt-only identity chunk contains graph keys"));
        }
        if matches!(
            category,
            IdentityCategory::PutVertex | IdentityCategory::DeleteVertex
        ) && !chunk.edge_keys.is_empty()
            || matches!(
                category,
                IdentityCategory::AddEdge
                    | IdentityCategory::PutEdge
                    | IdentityCategory::DeleteEdge
                    | IdentityCategory::DeleteEdgeContribution
            ) && !chunk.vertex_keys.is_empty()
        {
            return Err(gap("identity category contains the wrong key family"));
        }
        for key in &chunk.vertex_keys {
            validate_key(key).map_err(|_| gap("identity chunk contains an empty vertex key"))?;
        }
        let mut edge_keys = Vec::with_capacity(chunk.edge_keys.len());
        for edge in chunk.edge_keys {
            validate_key(&edge.tail).map_err(|_| gap("identity chunk has an empty edge tail"))?;
            validate_key(&edge.head).map_err(|_| gap("identity chunk has an empty edge head"))?;
            edge_keys.push(EdgeRef::new(edge.tail, edge.head));
        }
        if let Some(pending) = &self.pending {
            if (origin, chunk.seq, hlc, category)
                != (pending.origin, pending.seq, pending.hlc, pending.category)
                || chunk.chunk_index != pending.next_chunk_index
                || chunk.first_item_index != pending.next_item_index
            {
                return Err(gap("identity chunks are missing, reordered or interleaved"));
            }
        } else if chunk.chunk_index != 0 || chunk.first_item_index != 0 {
            return Err(gap("first identity chunk has a nonzero offset"));
        }
        let end = chunk
            .first_item_index
            .checked_add(u32::try_from(count).map_err(|_| gap("identity key count overflow"))?)
            .ok_or_else(|| gap("identity item offset overflow"))?;
        let next_cursor = if chunk.is_last {
            self.pending = None;
            Some(self.cursor.complete(origin, chunk.seq)?)
        } else {
            self.pending = Some(Pending {
                origin,
                seq: chunk.seq,
                hlc,
                category,
                next_chunk_index: chunk
                    .chunk_index
                    .checked_add(1)
                    .ok_or_else(|| gap("identity chunk index overflow"))?,
                next_item_index: end,
            });
            None
        };
        Ok(IdentityChunk {
            origin,
            seq: chunk.seq,
            hlc,
            category,
            chunk_index: chunk.chunk_index,
            first_item_index: chunk.first_item_index,
            vertex_keys: chunk.vertex_keys,
            edge_keys,
            is_last: chunk.is_last,
            next_cursor,
        })
    }
}

#[cfg(test)]
mod tests;
