use std::ops::Range;

use prost::Message;

use crate::{LanternError, contrib::MAX_LOGICAL_ITEMS};

/// Unordered found/missing identity multisets from a plural read. Duplicate
/// requested identities may occur more than once in either vector.
#[derive(Clone, Debug, PartialEq)]
pub struct GetBatch<T, Id> {
    pub found: Vec<T>,
    pub missing: Vec<Id>,
}

/// Request-index-aligned Delete outcomes. `deleted` is the number of true
/// `existed` entries, not the number of distinct identities.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeleteBatch {
    pub deleted: usize,
    pub existed: Vec<bool>,
}

/// Request-index-aligned Add results from the serving node. A retained
/// duplicate may be counted in `written`; neither it nor an effective
/// weight proves how many new contributions were applied.
#[derive(Clone, Debug, PartialEq)]
pub struct AddBatch {
    pub written: usize,
    pub effective_weights: Vec<f32>,
}

pub(crate) fn validate_logical_size(len: usize) -> Result<(), LanternError> {
    if len > MAX_LOGICAL_ITEMS {
        return Err(LanternError::InvalidInput(
            "logical batch exceeds 65536 items",
        ));
    }
    Ok(())
}

/// Preflight every chunk's exact encoded request size before dispatching any
/// write. Counts and wire bytes must both fit; an oversized single item fails
/// locally without accidentally sending earlier chunks.
pub(crate) fn chunk_plan<R: Message>(
    len: usize,
    chunk_size: usize,
    encode_limit: usize,
    make_request: impl Fn(Range<usize>) -> R,
) -> Result<Vec<Range<usize>>, LanternError> {
    validate_logical_size(len)?;
    if chunk_size == 0 {
        return Err(LanternError::InvalidConfig(
            "batch chunk size must be positive",
        ));
    }
    let mut ranges = Vec::new();
    let mut start = 0;
    while start < len {
        let max = (len - start).min(chunk_size);
        let full = make_request(start..start + max).encoded_len();
        let count = if full <= encode_limit {
            max
        } else {
            let mut low = 1;
            let mut high = max - 1;
            let mut best = 0;
            while low <= high {
                let middle = low + (high - low) / 2;
                let actual = make_request(start..start + middle).encoded_len();
                if actual <= encode_limit {
                    best = middle;
                    low = middle + 1;
                } else {
                    high = middle - 1;
                }
            }
            if best == 0 {
                return Err(LanternError::MessageTooLarge {
                    actual: make_request(start..start + 1).encoded_len(),
                    limit: encode_limit,
                });
            }
            best
        };
        ranges.push(start..start + count);
        start += count;
    }
    Ok(ranges)
}

#[cfg(test)]
mod tests;
