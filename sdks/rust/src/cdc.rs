//! Demand-driven, single-endpoint replication-log subscriptions.
//!
//! Identity projection is the safe default. Full mutation delivery is an
//! explicit separate API because it includes values and receipt metadata.

use std::{collections::BTreeMap, fmt, str::FromStr};

use prost::Message;
use tonic::Code;

use crate::{
    LanternClient, LanternError,
    error::CdcGap,
    generated::graph::v1::{
        HlcTimestamp, SubscribeProjection, SubscribeRequest, SubscribeResponse,
    },
    transport::{DataStream, StreamOptions},
};

mod full;
mod identity;
mod receipt;

pub use full::{EdgeWrite, FullMutation, FullMutationOp, FullMutationStream, VertexWrite};
pub use identity::{
    IdentityCategory, IdentityCheckpoint, IdentityChunk, IdentityEvent, IdentityStream,
};
pub use receipt::{
    EdgeCreateEffect, EdgeCreateEffectItem, Receipt, ReceiptEdgeAdd, ReceiptEdgeAddItem,
    ReceiptEdgeContributionDelete, ReceiptEdgeContributionDeleteItem, ReceiptEdgeDelete,
    ReceiptEdgeDeleteItem, ReceiptMetadata, ReceiptOriginalResult, ReceiptVertexDelete,
    ReceiptVertexDeleteItem, ReceiptVertexPut, ReceiptVertexPutItem,
};

const SUBSCRIBE_PATH: &str = "/graph.v1.LanternReplicationService/Subscribe";
const IDENTITY_FRAME_LIMIT: usize = 1 << 20;
const IDENTITY_CHUNK_ITEMS: usize = 1_024;

/// A canonical, nonzero 16-byte mutation origin. Its text form is exactly
/// 32 lowercase hex digits; neither zero nor noncanonical input is accepted.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct OriginId([u8; 16]);

impl OriginId {
    pub fn new(bytes: [u8; 16]) -> Result<Self, LanternError> {
        if bytes.iter().all(|byte| *byte == 0) {
            return Err(LanternError::InvalidInput("origin ID must not be zero"));
        }
        Ok(Self(bytes))
    }

    pub fn as_bytes(&self) -> &[u8; 16] {
        &self.0
    }

    pub fn to_hex(self) -> String {
        let mut out = String::with_capacity(32);
        for byte in self.0 {
            use fmt::Write;
            write!(&mut out, "{byte:02x}").expect("writing a hex byte to a String cannot fail");
        }
        out
    }

    pub(crate) fn from_wire(bytes: &[u8]) -> Result<Self, LanternError> {
        let bytes: [u8; 16] = bytes
            .try_into()
            .map_err(|_| gap("mutation origin must be 16 bytes"))?;
        Self::new(bytes).map_err(|_| gap("mutation origin must be nonzero"))
    }
}

impl fmt::Display for OriginId {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.to_hex())
    }
}

impl FromStr for OriginId {
    type Err = LanternError;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        let bytes = value.as_bytes();
        if bytes.len() != 32 {
            return Err(LanternError::InvalidInput(
                "origin must have 32 lowercase hex digits",
            ));
        }
        let mut id = [0; 16];
        let (pairs, _) = bytes.as_chunks::<2>();
        for (index, pair) in pairs.iter().enumerate() {
            let nibble = |byte: u8| match byte {
                b'0'..=b'9' => Some(byte - b'0'),
                b'a'..=b'f' => Some(byte - b'a' + 10),
                _ => None,
            };
            let high = nibble(pair[0]).ok_or(LanternError::InvalidInput(
                "origin must have 32 lowercase hex digits",
            ))?;
            let low = nibble(pair[1]).ok_or(LanternError::InvalidInput(
                "origin must have 32 lowercase hex digits",
            ))?;
            id[index] = (high << 4) | low;
        }
        Self::new(id)
    }
}

/// Portable per-origin **next expected** sequences, not local log offsets.
/// Applications persist this vector atomically with completed mutations.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct CdcCursor {
    next: BTreeMap<OriginId, u64>,
}

impl CdcCursor {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn from_hex_map(
        entries: impl IntoIterator<Item = (String, u64)>,
    ) -> Result<Self, LanternError> {
        let mut cursor = Self::new();
        for (key, next) in entries {
            let id = OriginId::from_str(&key)?;
            if cursor.next.insert(id, next).is_some() {
                return Err(LanternError::InvalidInput("duplicate CDC origin"));
            }
            if next == 0 {
                return Err(LanternError::InvalidInput(
                    "CDC cursor must contain next expected sequence, not zero",
                ));
            }
        }
        Ok(cursor)
    }

    pub fn to_hex_map(&self) -> BTreeMap<String, u64> {
        self.next
            .iter()
            .map(|(origin, next)| (origin.to_hex(), *next))
            .collect()
    }

    pub fn next_expected(&self, origin: OriginId) -> u64 {
        self.next.get(&origin).copied().unwrap_or(1)
    }

    pub fn insert_next(&mut self, origin: OriginId, next: u64) -> Result<(), LanternError> {
        if next == 0 {
            return Err(LanternError::InvalidInput(
                "CDC cursor must contain next expected sequence, not zero",
            ));
        }
        self.next.insert(origin, next);
        Ok(())
    }

    fn complete(&mut self, origin: OriginId, seq: u64) -> Result<Self, LanternError> {
        if seq != self.next_expected(origin) {
            return Err(gap("mutation sequence is not the next expected sequence"));
        }
        let next = seq
            .checked_add(1)
            .ok_or_else(|| gap("mutation sequence overflow"))?;
        self.next.insert(origin, next);
        Ok(self.clone())
    }
}

/// The original HLC coordinate, validated against the mutation origin.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct CdcHlc {
    pub wall_ns: i64,
    pub logical: u32,
    pub node_id: OriginId,
}

impl CdcHlc {
    fn from_wire(value: Option<HlcTimestamp>, origin: OriginId) -> Result<Self, LanternError> {
        let value = value.ok_or_else(|| gap("mutation is missing its HLC"))?;
        let node_id = OriginId::from_wire(&value.node_id)?;
        if node_id != origin {
            return Err(gap("HLC node ID differs from mutation origin"));
        }
        Ok(Self {
            wall_ns: value.wall_ns,
            logical: value.logical,
            node_id,
        })
    }
}

impl LanternClient {
    /// Prepare a checkpoint-first identity tail; the request is actually
    /// opened on the first `next_event` poll (no background reader or task).
    /// The application must revalidate its resident cache at that cut.
    pub async fn bootstrap_identities(
        &self,
        options: StreamOptions,
    ) -> Result<IdentityStream, LanternError> {
        let source = SubscriptionSource::new(
            self,
            SubscribeRequest {
                from_seq_per_origin: Default::default(),
                from_local_seq: 0,
                projection: SubscribeProjection::IdentityOnly as i32,
                bootstrap: true,
                accept_receipt_envelopes: false,
                namespace_format: String::new(),
            },
            options,
        )?;
        Ok(IdentityStream::new(source, CdcCursor::new(), true))
    }

    /// Resume from application-persisted, per-origin next expected sequences.
    /// Across replicas, a lagging responder may be silent; evicted history
    /// fails with [`LanternError::CdcGap`], never a silent cursor reset.
    /// The RPC opens on the first `next_event` poll; keep that task alive
    /// while other tasks write. A pending stream never inherits the unary
    /// timeout, even when the server has no immediate mutation to send.
    pub async fn resume_identities(
        &self,
        cursor: CdcCursor,
        options: StreamOptions,
    ) -> Result<IdentityStream, LanternError> {
        let source = SubscriptionSource::new(
            self,
            SubscribeRequest {
                from_seq_per_origin: cursor.to_hex_map().into_iter().collect(),
                from_local_seq: 0,
                projection: SubscribeProjection::IdentityOnly as i32,
                bootstrap: false,
                accept_receipt_envelopes: false,
                namespace_format: String::new(),
            },
            options,
        )?;
        Ok(IdentityStream::new(source, cursor, false))
    }

    /// Explicitly opt into the complete mutation/receipt stream. This is
    /// bounded retained history, not a durable receipt write/replay API. The
    /// stream opens on the first `next_mutation` poll.
    pub async fn subscribe_full_mutations(
        &self,
        cursor: CdcCursor,
        options: StreamOptions,
    ) -> Result<FullMutationStream, LanternError> {
        let source = SubscriptionSource::new(
            self,
            SubscribeRequest {
                from_seq_per_origin: cursor.to_hex_map().into_iter().collect(),
                from_local_seq: 0,
                projection: SubscribeProjection::FullMutation as i32,
                bootstrap: false,
                accept_receipt_envelopes: true,
                namespace_format: String::new(),
            },
            options,
        )?;
        Ok(FullMutationStream::new(source, cursor))
    }
}

/// Keep the stream unstarted until first demand; Connect-Go can withhold
/// response headers until its first event. A caller can obtain a handle
/// before writers publish without spawning an SDK-owned reader.
pub(crate) struct SubscriptionSource {
    client: LanternClient,
    request: Option<SubscribeRequest>,
    options: StreamOptions,
    wire: Option<DataStream>,
}

impl SubscriptionSource {
    fn new(
        client: &LanternClient,
        request: SubscribeRequest,
        options: StreamOptions,
    ) -> Result<Self, LanternError> {
        options.validate()?;
        let actual = request.encoded_len();
        if actual > client.encode_limit() {
            return Err(LanternError::MessageTooLarge {
                actual,
                limit: client.encode_limit(),
            });
        }
        Ok(Self {
            client: client.clone(),
            request: Some(request),
            options,
            wire: None,
        })
    }

    async fn next(&mut self) -> Result<Option<Vec<u8>>, LanternError> {
        if let Some(request) = self.request.take() {
            self.wire = Some(
                self.client
                    .raw_data_stream(request, SUBSCRIBE_PATH, self.options)
                    .await
                    .map_err(cdc_rpc_error)?,
            );
        }
        self.wire
            .as_mut()
            .ok_or_else(|| gap("Subscribe stream could not be opened"))?
            .next()
            .await
            .map_err(cdc_rpc_error)
    }
}

pub(crate) fn cdc_rpc_error(error: LanternError) -> LanternError {
    match error {
        LanternError::Rpc(failure) if failure.status().code() == Code::FailedPrecondition => {
            LanternError::CdcGap(CdcGap::rpc(failure))
        }
        other => other,
    }
}

fn gap(reason: &'static str) -> LanternError {
    LanternError::CdcGap(CdcGap::protocol(reason))
}

fn require_frame(frame: Option<Vec<u8>>, reason: &'static str) -> Result<Vec<u8>, LanternError> {
    frame.ok_or_else(|| gap(reason))
}

fn decode_frame(raw: &[u8]) -> Result<SubscribeResponse, LanternError> {
    if raw.len() > 64 * 1024 * 1024 {
        return Err(gap("Subscribe frame exceeds the SDK maximum"));
    }
    let frame = SubscribeResponse::decode(raw).map_err(|_| gap("malformed Subscribe frame"))?;
    if frame.encoded_len() != raw.len() {
        return Err(gap("unknown or noncanonical Subscribe protobuf fields"));
    }
    Ok(frame)
}

#[cfg(test)]
mod tests;
