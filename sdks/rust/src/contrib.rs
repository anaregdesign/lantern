use std::sync::atomic::{AtomicU64, Ordering};

use crate::{Edge, EdgeInput, LanternError};

const MAX_SEQUENCE: u64 = (1 << 48) - 1;
pub(crate) const MAX_LOGICAL_ITEMS: usize = 65_536;

/// An exact 24-byte live-contribution identity. Callers supplying IDs must
/// avoid reuse across distinct logical calls, including other clients.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub struct ContribId([u8; 24]);

impl ContribId {
    pub fn new(bytes: [u8; 24]) -> Result<Self, LanternError> {
        if bytes.iter().all(|byte| *byte == 0) {
            return Err(LanternError::InvalidInput(
                "contribution ID must not be all zero",
            ));
        }
        Ok(Self(bytes))
    }

    pub fn as_bytes(&self) -> &[u8; 24] {
        &self.0
    }
}

impl TryFrom<&[u8]> for ContribId {
    type Error = LanternError;

    fn try_from(bytes: &[u8]) -> Result<Self, Self::Error> {
        let bytes: [u8; 24] = bytes
            .try_into()
            .map_err(|_| LanternError::InvalidInput("contribution ID must be 24 bytes"))?;
        Self::new(bytes)
    }
}

/// Add-only input with an optional caller-owned live-contribution ID.
/// Put never consumes or silently ignores an Add ID.
#[derive(Clone, Debug, PartialEq)]
pub struct AddInput {
    pub edge: EdgeInput,
    pub contrib_id: Option<ContribId>,
}

impl AddInput {
    pub fn new(edge: EdgeInput) -> Self {
        Self {
            edge,
            contrib_id: None,
        }
    }

    pub fn with_contrib_id(mut self, id: ContribId) -> Self {
        self.contrib_id = Some(id);
        self
    }
}

impl From<EdgeInput> for AddInput {
    fn from(edge: EdgeInput) -> Self {
        Self::new(edge)
    }
}

/// Snapshot of a logical Add: relative TTLs and IDs will never be reminted.
/// Retain it for caller-directed manual action after uncertain response loss;
/// even a stable ID is not safe for automatic receipt-less retry after Delete
/// or expiration has ended its live deduplication horizon.
#[derive(Clone, Debug, PartialEq)]
pub struct PreparedAdd {
    pub(crate) edges: Vec<Edge>,
    pub(crate) contrib_ids: Vec<Option<ContribId>>,
}

impl PreparedAdd {
    pub fn edges(&self) -> &[Edge] {
        &self.edges
    }

    pub fn contrib_ids(&self) -> &[Option<ContribId>] {
        &self.contrib_ids
    }

    pub fn len(&self) -> usize {
        self.edges.len()
    }

    pub fn is_empty(&self) -> bool {
        self.edges.is_empty()
    }
}

pub(crate) struct ContribIdGenerator {
    nonce: [u8; 16],
    sequence: AtomicU64,
}

impl ContribIdGenerator {
    pub(crate) fn new() -> Result<Self, LanternError> {
        let mut nonce = [0; 16];
        getrandom::fill(&mut nonce).map_err(LanternError::Entropy)?;
        Ok(Self {
            nonce,
            sequence: AtomicU64::new(0),
        })
    }

    pub(crate) fn next_ids(&self, indices: &[usize]) -> Result<Vec<ContribId>, LanternError> {
        if indices.is_empty() {
            return Ok(Vec::new());
        }
        if indices.iter().any(|index| *index >= MAX_LOGICAL_ITEMS) {
            return Err(LanternError::InvalidInput(
                "contribution index exceeds 16 bits",
            ));
        }
        let previous = self
            .sequence
            .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |seq| {
                (seq < MAX_SEQUENCE).then(|| seq + 1)
            })
            .map_err(|_| LanternError::InvalidInput("contribution sequence exhausted"))?;
        let sequence = previous + 1;
        Ok(indices
            .iter()
            .map(|index| {
                let mut bytes = [0; 24];
                bytes[..16].copy_from_slice(&self.nonce);
                bytes[16..].copy_from_slice(&((sequence << 16) | (*index as u64)).to_be_bytes());
                ContribId(bytes)
            })
            .collect())
    }
}

#[cfg(test)]
mod tests;
