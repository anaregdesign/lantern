use std::{
    io::{BufRead, BufReader, Read},
    time::{SystemTime, UNIX_EPOCH},
};

use base64::{Engine as _, engine::general_purpose::STANDARD};
use prost::Message;
use prost_types::Duration as ProtoDuration;
use serde_json::{Map, Value, json};
use sha2::{Digest, Sha256};

use crate::{
    Edge, LanternError, Timestamp, Vertex, VertexValue,
    backup::{ARCHIVE_VERSION, BackupFormat, BackupRecord},
    generated::graph::v1::{BackupSnapshotResponse, backup_snapshot_response::Record},
};

pub(super) fn encode(record: &BackupRecord, format: BackupFormat) -> Result<Vec<u8>, LanternError> {
    match format {
        BackupFormat::LengthDelimitedProtobuf => {
            let record = match record {
                BackupRecord::Vertex(vertex) => Record::Vertex(vertex.clone()),
                BackupRecord::Edge(edge) => Record::Edge(edge.clone()),
            };
            let frame = BackupSnapshotResponse {
                record: Some(record),
            };
            let mut data = Vec::with_capacity(frame.encoded_len() + 5);
            frame
                .encode_length_delimited(&mut data)
                .map_err(|_| LanternError::BackupFormat("cannot encode protobuf record"))?;
            Ok(data)
        }
        BackupFormat::RustNdjsonV1 => {
            let document = match record {
                BackupRecord::Vertex(vertex) => json!({
                    "version": ARCHIVE_VERSION,
                    "vertex": {
                        "key": vertex.key,
                        "value": value_json(&vertex.value),
                        "expiration": timestamp_json(&vertex.expiration),
                    }
                }),
                BackupRecord::Edge(edge) => json!({
                    "version": ARCHIVE_VERSION,
                    "edge": {
                        "tail": edge.tail,
                        "head": edge.head,
                        "weight_bits": format!("{:08x}", edge.weight.to_bits()),
                        "expiration": timestamp_json(&edge.expiration),
                    }
                }),
            };
            let mut data = serde_json::to_vec(&document).map_err(LanternError::Json)?;
            data.push(b'\n');
            Ok(data)
        }
    }
}

fn timestamp_json(value: &Option<Timestamp>) -> Value {
    match value {
        None => Value::Null,
        Some(value) => json!({
            "seconds": value.seconds.to_string(),
            "nanos": value.nanos,
        }),
    }
}

fn value_json(value: &Option<VertexValue>) -> Value {
    match value {
        None => Value::Null,
        Some(VertexValue::Nil(true)) => json!({"kind": "nil"}),
        Some(VertexValue::Nil(false)) => {
            unreachable!("backup records are validated before encoding")
        }
        Some(VertexValue::Int32(value)) => json!({"kind": "int32", "value": value.to_string()}),
        Some(VertexValue::Int64(value)) => json!({"kind": "int64", "value": value.to_string()}),
        Some(VertexValue::Uint32(value)) => json!({"kind": "uint32", "value": value.to_string()}),
        Some(VertexValue::Uint64(value)) => json!({"kind": "uint64", "value": value.to_string()}),
        Some(VertexValue::Float32(value)) => {
            json!({"kind": "float32", "bits": format!("{:08x}", value.to_bits())})
        }
        Some(VertexValue::Float64(value)) => {
            json!({"kind": "float64", "bits": format!("{:016x}", value.to_bits())})
        }
        Some(VertexValue::Bool(value)) => json!({"kind": "bool", "value": value}),
        Some(VertexValue::String(value)) => json!({"kind": "string", "value": value}),
        Some(VertexValue::Bytes(value)) => {
            json!({"kind": "bytes", "base64": STANDARD.encode(value)})
        }
        Some(VertexValue::Timestamp(value)) => {
            json!({"kind": "timestamp", "value": {
                "seconds": value.seconds.to_string(), "nanos": value.nanos,
            }})
        }
        Some(VertexValue::Duration(value)) => {
            json!({"kind": "duration", "value": {
                "seconds": value.seconds.to_string(), "nanos": value.nanos,
            }})
        }
    }
}

pub(super) struct ArchiveReader<R: Read> {
    source: BufReader<R>,
    format: BackupFormat,
    max_record_bytes: usize,
    hasher: Sha256,
    bytes: u64,
}

impl<R: Read> ArchiveReader<R> {
    pub(super) fn new(source: R, format: BackupFormat, max_record_bytes: usize) -> Self {
        Self {
            source: BufReader::new(source),
            format,
            max_record_bytes,
            hasher: Sha256::new(),
            bytes: 0,
        }
    }

    pub(super) fn next_record(
        &mut self,
        prefix: &str,
    ) -> Result<Option<BackupRecord>, LanternError> {
        let record = match self.format {
            BackupFormat::LengthDelimitedProtobuf => {
                let Some((header, length)) = self.length_prefix()? else {
                    return Ok(None);
                };
                if length > self.max_record_bytes {
                    return Err(LanternError::BackupFormat(
                        "protobuf record exceeds size limit",
                    ));
                }
                let mut payload = vec![0; length];
                self.source.read_exact(&mut payload).map_err(truncated)?;
                self.account(&header)?;
                self.account(&payload)?;
                BackupRecord::from_wire(&payload, prefix)?
            }
            BackupFormat::RustNdjsonV1 => {
                let mut line = Vec::new();
                let limit = self.max_record_bytes as u64 + 1;
                let read = self
                    .source
                    .by_ref()
                    .take(limit)
                    .read_until(b'\n', &mut line)
                    .map_err(LanternError::Io)?;
                if read == 0 {
                    return Ok(None);
                }
                if read > self.max_record_bytes {
                    return Err(LanternError::BackupFormat(
                        "NDJSON record exceeds size limit",
                    ));
                }
                if line.last() != Some(&b'\n') {
                    return Err(LanternError::BackupFormat(
                        "truncated NDJSON record without newline",
                    ));
                }
                self.account(&line)?;
                let document: Value = serde_json::from_slice(&line).map_err(LanternError::Json)?;
                let record = parse_json(&document)?;
                record.validate(prefix)?;
                if encode(&record, BackupFormat::RustNdjsonV1)?.as_slice() != line {
                    return Err(LanternError::BackupFormat(
                        "noncanonical or duplicate NDJSON fields",
                    ));
                }
                record
            }
        };
        Ok(Some(record))
    }

    fn length_prefix(&mut self) -> Result<Option<(Vec<u8>, usize)>, LanternError> {
        let mut header = Vec::with_capacity(5);
        let mut length = 0_u64;
        for shift in (0..35).step_by(7) {
            let mut byte = [0];
            let count = self.source.read(&mut byte).map_err(LanternError::Io)?;
            if count == 0 && header.is_empty() {
                return Ok(None);
            }
            if count == 0 {
                return Err(LanternError::BackupFormat("truncated protobuf length"));
            }
            header.push(byte[0]);
            length |= u64::from(byte[0] & 0x7f) << shift;
            if byte[0] & 0x80 == 0 {
                if header.len() > 1 && byte[0] == 0 {
                    return Err(LanternError::BackupFormat(
                        "noncanonical protobuf record length",
                    ));
                }
                return Ok(Some((
                    header,
                    usize::try_from(length).map_err(|_| {
                        LanternError::BackupFormat("protobuf record length overflow")
                    })?,
                )));
            }
        }
        Err(LanternError::BackupFormat(
            "protobuf record length exceeds varint bounds",
        ))
    }

    fn account(&mut self, data: &[u8]) -> Result<(), LanternError> {
        self.bytes = self
            .bytes
            .checked_add(data.len() as u64)
            .ok_or(LanternError::BackupFormat("archive length overflow"))?;
        self.hasher.update(data);
        Ok(())
    }

    pub(super) fn finish(self) -> (u64, [u8; 32]) {
        (self.bytes, self.hasher.finalize().into())
    }
}

fn truncated(error: std::io::Error) -> LanternError {
    if error.kind() == std::io::ErrorKind::UnexpectedEof {
        LanternError::BackupFormat("truncated protobuf record")
    } else {
        LanternError::Io(error)
    }
}

fn parse_json(document: &Value) -> Result<BackupRecord, LanternError> {
    let root = object(document, &["version"])?;
    if root.len() != 2 || root["version"].as_u64() != Some(u64::from(ARCHIVE_VERSION)) {
        return Err(LanternError::BackupFormat(
            "unsupported NDJSON record version",
        ));
    }
    if let Some(vertex) = root.get("vertex") {
        let fields = exact(vertex, &["key", "value", "expiration"])?;
        Ok(BackupRecord::Vertex(Vertex {
            key: text(fields, "key")?.to_owned(),
            value: parse_value(&fields["value"])?,
            expiration: parse_timestamp(&fields["expiration"])?,
        }))
    } else if let Some(edge) = root.get("edge") {
        let fields = exact(edge, &["tail", "head", "weight_bits", "expiration"])?;
        let bits = parse_bits(text(fields, "weight_bits")?, 8)?;
        Ok(BackupRecord::Edge(Edge {
            tail: text(fields, "tail")?.to_owned(),
            head: text(fields, "head")?.to_owned(),
            weight: f32::from_bits(bits as u32),
            expiration: parse_timestamp(&fields["expiration"])?,
        }))
    } else {
        Err(LanternError::BackupFormat("unknown NDJSON record kind"))
    }
}

fn object<'a>(value: &'a Value, required: &[&str]) -> Result<&'a Map<String, Value>, LanternError> {
    let map = value.as_object().ok_or(LanternError::BackupFormat(
        "NDJSON record field must be an object",
    ))?;
    if required.iter().any(|key| !map.contains_key(*key)) {
        return Err(LanternError::BackupFormat("missing NDJSON record field"));
    }
    Ok(map)
}

fn exact<'a>(value: &'a Value, keys: &[&str]) -> Result<&'a Map<String, Value>, LanternError> {
    let map = object(value, keys)?;
    if map.len() != keys.len() {
        return Err(LanternError::BackupFormat("unknown NDJSON record field"));
    }
    Ok(map)
}

fn text<'a>(map: &'a Map<String, Value>, key: &str) -> Result<&'a str, LanternError> {
    map[key]
        .as_str()
        .ok_or(LanternError::BackupFormat("NDJSON field must be a string"))
}

fn parse_bits(bits: &str, width: usize) -> Result<u64, LanternError> {
    if bits.len() != width
        || !bits
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    {
        return Err(LanternError::BackupFormat(
            "noncanonical floating-point bits",
        ));
    }
    u64::from_str_radix(bits, 16)
        .map_err(|_| LanternError::BackupFormat("invalid floating-point bits"))
}

fn parse_timestamp(value: &Value) -> Result<Option<Timestamp>, LanternError> {
    if value.is_null() {
        return Ok(None);
    }
    let map = exact(value, &["seconds", "nanos"])?;
    let seconds = text(map, "seconds")?
        .parse()
        .map_err(|_| LanternError::BackupFormat("invalid timestamp seconds"))?;
    let nanos = map["nanos"]
        .as_i64()
        .and_then(|number| number.try_into().ok())
        .ok_or(LanternError::BackupFormat("invalid timestamp nanos"))?;
    Ok(Some(Timestamp { seconds, nanos }))
}

fn parse_value(value: &Value) -> Result<Option<VertexValue>, LanternError> {
    if value.is_null() {
        return Ok(None);
    }
    let map = object(value, &["kind"])?;
    let kind = text(map, "kind")?;
    if kind == "nil" {
        if map.len() != 1 {
            return Err(LanternError::BackupFormat("invalid nil value"));
        }
        return Ok(Some(VertexValue::Nil(true)));
    }
    let field = match kind {
        "float32" | "float64" => "bits",
        "bytes" => "base64",
        "int32" | "int64" | "uint32" | "uint64" | "bool" | "string" | "timestamp" | "duration" => {
            "value"
        }
        _ => return Err(LanternError::BackupFormat("unknown Vertex value kind")),
    };
    if map.len() != 2 || !map.contains_key(field) {
        return Err(LanternError::BackupFormat("invalid Vertex value fields"));
    }
    let invalid = || LanternError::BackupFormat("invalid numeric Vertex value");
    let parsed = match kind {
        "int32" => VertexValue::Int32(text(map, field)?.parse().map_err(|_| invalid())?),
        "int64" => VertexValue::Int64(text(map, field)?.parse().map_err(|_| invalid())?),
        "uint32" => VertexValue::Uint32(text(map, field)?.parse().map_err(|_| invalid())?),
        "uint64" => VertexValue::Uint64(text(map, field)?.parse().map_err(|_| invalid())?),
        "float32" => VertexValue::Float32(f32::from_bits(parse_bits(text(map, field)?, 8)? as u32)),
        "float64" => VertexValue::Float64(f64::from_bits(parse_bits(text(map, field)?, 16)?)),
        "bool" => VertexValue::Bool(
            map[field]
                .as_bool()
                .ok_or(LanternError::BackupFormat("invalid boolean Vertex value"))?,
        ),
        "string" => VertexValue::String(text(map, field)?.to_owned()),
        "bytes" => {
            let encoded = text(map, field)?;
            let decoded = STANDARD
                .decode(encoded)
                .map_err(|_| LanternError::BackupFormat("invalid base64 Vertex bytes"))?;
            if STANDARD.encode(&decoded) != encoded {
                return Err(LanternError::BackupFormat(
                    "noncanonical base64 Vertex bytes",
                ));
            }
            VertexValue::Bytes(decoded)
        }
        "timestamp" => VertexValue::Timestamp(
            parse_timestamp(&map[field])?
                .ok_or(LanternError::BackupFormat("missing timestamp value"))?,
        ),
        "duration" => {
            let value = exact(&map[field], &["seconds", "nanos"])?;
            let seconds = text(value, "seconds")?.parse().map_err(|_| invalid())?;
            let nanos = value["nanos"]
                .as_i64()
                .and_then(|number| number.try_into().ok())
                .ok_or_else(invalid)?;
            VertexValue::Duration(ProtoDuration { seconds, nanos })
        }
        _ => unreachable!("kind already checked"),
    };
    Ok(Some(parsed))
}

pub(super) fn check_restorable(record: &BackupRecord) -> Result<(), LanternError> {
    let expiration = match record {
        BackupRecord::Vertex(vertex) => &vertex.expiration,
        BackupRecord::Edge(edge) => {
            if !edge.weight.is_finite() {
                return Err(LanternError::UnsupportedBackup(
                    "folded Edge weight is NaN or infinity; public PutEdges only accepts finite weights",
                ));
            }
            &edge.expiration
        }
    };
    if let Some(expiration) = expiration {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| LanternError::UnsupportedBackup("local clock predates Unix epoch"))?;
        if expiration.seconds < now.as_secs() as i64
            || expiration.seconds == now.as_secs() as i64
                && expiration.nanos <= now.subsec_nanos() as i32
        {
            return Err(LanternError::UnsupportedBackup(
                "record expired since capture; exact live graph restore is no longer possible",
            ));
        }
    }
    Ok(())
}
