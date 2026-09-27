use std::time::{Duration, SystemTime, UNIX_EPOCH};

use prost_types::{Duration as ProtoDuration, Timestamp};

use crate::{Edge, LanternError, Vertex, VertexValue};

const TIMESTAMP_MIN: i64 = -62_135_596_800;
const TIMESTAMP_MAX: i64 = 253_402_300_799;
const DURATION_MAX: i64 = 315_576_000_000;

/// Omission is permanent, even when a server reports a default TTL. `At`
/// accepts past instants (born-expired writes) except Go's exact year-one
/// no-expiration sentinel. `After` is resolved once before any RPC attempt.
#[derive(Clone, Debug, Default, PartialEq)]
pub enum Expiration {
    #[default]
    Never,
    At(Timestamp),
    After(Duration),
}

impl Expiration {
    /// Convert a positive signed protobuf duration to a relative TTL.
    pub fn after_proto(duration: ProtoDuration) -> Result<Self, LanternError> {
        validate_duration(&duration).map_err(LanternError::InvalidInput)?;
        if duration.seconds < 0 || duration.seconds == 0 && duration.nanos <= 0 {
            return Err(LanternError::InvalidInput("relative TTL must be positive"));
        }
        let duration = Duration::new(duration.seconds as u64, duration.nanos as u32);
        Ok(Self::After(duration))
    }
}

/// Input for a single Vertex Put. The oneof is stored without numeric
/// coercion: `None` is unset; `Some(VertexValue::Nil(true))` is explicit nil.
#[derive(Clone, Debug, PartialEq)]
pub struct VertexInput {
    pub key: String,
    pub value: Option<VertexValue>,
    pub expiration: Expiration,
}

impl VertexInput {
    pub fn new(key: impl Into<String>, value: Option<VertexValue>) -> Self {
        Self {
            key: key.into(),
            value,
            expiration: Expiration::Never,
        }
    }

    pub fn with_expiration(mut self, expiration: Expiration) -> Self {
        self.expiration = expiration;
        self
    }

    pub fn int32(key: impl Into<String>, value: i32) -> Self {
        Self::new(key, Some(VertexValue::Int32(value)))
    }

    pub fn int64(key: impl Into<String>, value: i64) -> Self {
        Self::new(key, Some(VertexValue::Int64(value)))
    }

    pub fn uint32(key: impl Into<String>, value: u32) -> Self {
        Self::new(key, Some(VertexValue::Uint32(value)))
    }

    pub fn uint64(key: impl Into<String>, value: u64) -> Self {
        Self::new(key, Some(VertexValue::Uint64(value)))
    }

    pub fn float32(key: impl Into<String>, value: f32) -> Self {
        Self::new(key, Some(VertexValue::Float32(value)))
    }

    pub fn float64(key: impl Into<String>, value: f64) -> Self {
        Self::new(key, Some(VertexValue::Float64(value)))
    }

    pub fn boolean(key: impl Into<String>, value: bool) -> Self {
        Self::new(key, Some(VertexValue::Bool(value)))
    }

    pub fn string(key: impl Into<String>, value: impl Into<String>) -> Self {
        Self::new(key, Some(VertexValue::String(value.into())))
    }

    pub fn bytes(key: impl Into<String>, value: impl Into<Vec<u8>>) -> Self {
        Self::new(key, Some(VertexValue::Bytes(value.into())))
    }

    pub fn timestamp(key: impl Into<String>, value: Timestamp) -> Result<Self, LanternError> {
        validate_timestamp(&value).map_err(LanternError::InvalidInput)?;
        Ok(Self::new(key, Some(VertexValue::Timestamp(value))))
    }

    pub fn duration(key: impl Into<String>, value: ProtoDuration) -> Result<Self, LanternError> {
        validate_duration(&value).map_err(LanternError::InvalidInput)?;
        Ok(Self::new(key, Some(VertexValue::Duration(value))))
    }

    pub fn nil(key: impl Into<String>) -> Self {
        Self::new(key, Some(VertexValue::Nil(true)))
    }

    pub fn unset(key: impl Into<String>) -> Self {
        Self::new(key, None)
    }

    pub(crate) fn into_wire(self, now: SystemTime) -> Result<Vertex, LanternError> {
        validate_key(&self.key).map_err(LanternError::InvalidInput)?;
        if let Some(value) = &self.value {
            validate_value(value).map_err(LanternError::InvalidInput)?;
        }
        Ok(Vertex {
            key: self.key,
            value: self.value,
            expiration: resolve_expiration(&self.expiration, now)?,
        })
    }
}

/// Input for Edge Put/Add. Edge weights on either write path must be finite.
#[derive(Clone, Debug, PartialEq)]
pub struct EdgeInput {
    pub tail: String,
    pub head: String,
    pub weight: f32,
    pub expiration: Expiration,
}

impl EdgeInput {
    pub fn new(tail: impl Into<String>, head: impl Into<String>, weight: f32) -> Self {
        Self {
            tail: tail.into(),
            head: head.into(),
            weight,
            expiration: Expiration::Never,
        }
    }

    pub fn with_expiration(mut self, expiration: Expiration) -> Self {
        self.expiration = expiration;
        self
    }

    pub(crate) fn into_wire(self, now: SystemTime) -> Result<Edge, LanternError> {
        validate_key(&self.tail).map_err(LanternError::InvalidInput)?;
        validate_key(&self.head).map_err(LanternError::InvalidInput)?;
        if !self.weight.is_finite() {
            return Err(LanternError::InvalidInput(
                "edge source weight must be finite",
            ));
        }
        Ok(Edge {
            tail: self.tail,
            head: self.head,
            weight: self.weight,
            expiration: resolve_expiration(&self.expiration, now)?,
        })
    }
}

/// Identity for an Edge Get/Delete. Can be repeated within a logical batch.
#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct EdgeRef {
    pub tail: String,
    pub head: String,
}

impl EdgeRef {
    pub fn new(tail: impl Into<String>, head: impl Into<String>) -> Self {
        Self {
            tail: tail.into(),
            head: head.into(),
        }
    }

    pub(crate) fn validate(&self) -> Result<(), LanternError> {
        validate_key(&self.tail).map_err(LanternError::InvalidInput)?;
        validate_key(&self.head).map_err(LanternError::InvalidInput)
    }
}

/// Exact wire oneof discriminant, including unset versus explicit nil.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[non_exhaustive]
pub enum VertexKind {
    Unset,
    Nil,
    Int32,
    Int64,
    Uint32,
    Uint64,
    Float32,
    Float64,
    Bool,
    String,
    Bytes,
    Timestamp,
    Duration,
}

impl Vertex {
    pub fn kind(&self) -> VertexKind {
        match self.value.as_ref() {
            None => VertexKind::Unset,
            Some(VertexValue::Nil(_)) => VertexKind::Nil,
            Some(VertexValue::Int32(_)) => VertexKind::Int32,
            Some(VertexValue::Int64(_)) => VertexKind::Int64,
            Some(VertexValue::Uint32(_)) => VertexKind::Uint32,
            Some(VertexValue::Uint64(_)) => VertexKind::Uint64,
            Some(VertexValue::Float32(_)) => VertexKind::Float32,
            Some(VertexValue::Float64(_)) => VertexKind::Float64,
            Some(VertexValue::Bool(_)) => VertexKind::Bool,
            Some(VertexValue::String(_)) => VertexKind::String,
            Some(VertexValue::Bytes(_)) => VertexKind::Bytes,
            Some(VertexValue::Timestamp(_)) => VertexKind::Timestamp,
            Some(VertexValue::Duration(_)) => VertexKind::Duration,
        }
    }

    pub fn int32_value(&self) -> Option<i32> {
        match self.value {
            Some(VertexValue::Int32(value)) => Some(value),
            _ => None,
        }
    }

    pub fn int64_value(&self) -> Option<i64> {
        match self.value {
            Some(VertexValue::Int64(value)) => Some(value),
            _ => None,
        }
    }

    pub fn uint32_value(&self) -> Option<u32> {
        match self.value {
            Some(VertexValue::Uint32(value)) => Some(value),
            _ => None,
        }
    }

    pub fn uint64_value(&self) -> Option<u64> {
        match self.value {
            Some(VertexValue::Uint64(value)) => Some(value),
            _ => None,
        }
    }

    pub fn float32_value(&self) -> Option<f32> {
        match self.value {
            Some(VertexValue::Float32(value)) => Some(value),
            _ => None,
        }
    }

    pub fn float64_value(&self) -> Option<f64> {
        match self.value {
            Some(VertexValue::Float64(value)) => Some(value),
            _ => None,
        }
    }

    pub fn bool_value(&self) -> Option<bool> {
        match self.value {
            Some(VertexValue::Bool(value)) => Some(value),
            _ => None,
        }
    }

    pub fn string_value(&self) -> Option<&str> {
        match &self.value {
            Some(VertexValue::String(value)) => Some(value),
            _ => None,
        }
    }

    pub fn bytes_value(&self) -> Option<&[u8]> {
        match &self.value {
            Some(VertexValue::Bytes(value)) => Some(value),
            _ => None,
        }
    }

    pub fn timestamp_value(&self) -> Option<&Timestamp> {
        match &self.value {
            Some(VertexValue::Timestamp(value)) => Some(value),
            _ => None,
        }
    }

    pub fn duration_value(&self) -> Option<&ProtoDuration> {
        match &self.value {
            Some(VertexValue::Duration(value)) => Some(value),
            _ => None,
        }
    }

    pub fn is_nil(&self) -> bool {
        matches!(self.value, Some(VertexValue::Nil(true)))
    }

    pub fn is_unset(&self) -> bool {
        self.value.is_none()
    }

    pub(crate) fn validate_response(&self) -> Result<(), LanternError> {
        validate_key(&self.key).map_err(LanternError::Protocol)?;
        if let Some(value) = &self.value {
            validate_value(value).map_err(LanternError::Protocol)?;
        }
        if let Some(expiration) = &self.expiration {
            validate_expiration_timestamp(expiration).map_err(LanternError::Protocol)?;
        }
        Ok(())
    }
}

impl Edge {
    pub(crate) fn validate_response(&self) -> Result<(), LanternError> {
        validate_key(&self.tail).map_err(LanternError::Protocol)?;
        validate_key(&self.head).map_err(LanternError::Protocol)?;
        if let Some(expiration) = &self.expiration {
            validate_expiration_timestamp(expiration).map_err(LanternError::Protocol)?;
        }
        Ok(())
    }
}

pub(crate) fn validate_key(key: &str) -> Result<(), &'static str> {
    if key.is_empty() {
        return Err("vertex/edge key must not be empty");
    }
    Ok(())
}

pub(crate) fn validate_timestamp(value: &Timestamp) -> Result<(), &'static str> {
    if !(TIMESTAMP_MIN..=TIMESTAMP_MAX).contains(&value.seconds)
        || !(0..1_000_000_000).contains(&value.nanos)
    {
        return Err("timestamp is outside the protobuf range");
    }
    Ok(())
}

fn validate_expiration_timestamp(value: &Timestamp) -> Result<(), &'static str> {
    validate_timestamp(value)?;
    if value.seconds == TIMESTAMP_MIN && value.nanos == 0 {
        return Err("explicit expiration equals Go's no-expiration sentinel");
    }
    Ok(())
}

pub(crate) fn validate_duration(value: &ProtoDuration) -> Result<(), &'static str> {
    if !(-DURATION_MAX..=DURATION_MAX).contains(&value.seconds)
        || !(-999_999_999..=999_999_999).contains(&value.nanos)
        || value.seconds > 0 && value.nanos < 0
        || value.seconds < 0 && value.nanos > 0
    {
        return Err("duration is outside the protobuf range or has inconsistent signs");
    }
    Ok(())
}

fn validate_value(value: &VertexValue) -> Result<(), &'static str> {
    match value {
        VertexValue::Timestamp(value) => validate_timestamp(value),
        VertexValue::Duration(value) => validate_duration(value),
        VertexValue::Nil(false) => Err("Nil(false) is not a valid vertex value"),
        _ => Ok(()),
    }
}

pub(crate) fn resolve_expiration(
    expiration: &Expiration,
    now: SystemTime,
) -> Result<Option<Timestamp>, LanternError> {
    let timestamp = match expiration {
        Expiration::Never => return Ok(None),
        Expiration::At(timestamp) => *timestamp,
        Expiration::After(duration) => {
            if duration.is_zero() {
                return Err(LanternError::InvalidInput("relative TTL must be positive"));
            }
            // Windows SystemTime arithmetic discards sub-100ns durations.
            let start = system_time_timestamp(now)?;
            let nanos = start.nanos as u32 + duration.subsec_nanos();
            let seconds = i64::try_from(duration.as_secs())
                .ok()
                .and_then(|seconds| start.seconds.checked_add(seconds))
                .and_then(|seconds| seconds.checked_add(i64::from(nanos / 1_000_000_000)))
                .ok_or(LanternError::InvalidInput(
                    "relative TTL exceeds protobuf timestamp range",
                ))?;
            Timestamp {
                seconds,
                nanos: (nanos % 1_000_000_000) as i32,
            }
        }
    };
    validate_expiration_timestamp(&timestamp).map_err(LanternError::InvalidInput)?;
    Ok(Some(timestamp))
}

fn system_time_timestamp(time: SystemTime) -> Result<Timestamp, LanternError> {
    let (seconds, nanos) = match time.duration_since(UNIX_EPOCH) {
        Ok(since) => (i64::try_from(since.as_secs()).ok(), since.subsec_nanos()),
        Err(before) => {
            let before = before.duration();
            let secs = i64::try_from(before.as_secs())
                .ok()
                .and_then(|secs| secs.checked_neg());
            if before.subsec_nanos() == 0 {
                (secs, 0)
            } else {
                (
                    secs.and_then(|secs| secs.checked_sub(1)),
                    1_000_000_000 - before.subsec_nanos(),
                )
            }
        }
    };
    let seconds = seconds.ok_or(LanternError::InvalidInput(
        "expiration exceeds the protobuf timestamp range",
    ))?;
    let timestamp = Timestamp {
        seconds,
        nanos: nanos as i32,
    };
    validate_timestamp(&timestamp).map_err(LanternError::InvalidInput)?;
    Ok(timestamp)
}

/// The local clock is only an upper bound on a server-reported live Put; it
/// cannot reinterpret a conditional rejection or a causally superseded write.
pub(crate) fn locally_expired(expiration: &Option<Timestamp>) -> Result<bool, LanternError> {
    let Some(expiration) = expiration else {
        return Ok(false);
    };
    let now = system_time_timestamp(SystemTime::now())
        .map_err(|_| LanternError::InvalidConfig("system clock outside protobuf time range"))?;
    Ok((now.seconds, now.nanos) >= (expiration.seconds, expiration.nanos))
}

#[cfg(test)]
mod tests;
