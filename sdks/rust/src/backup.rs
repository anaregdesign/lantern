//! Graph-only point-in-time backup and checked, bounded public-Put restore.
//!
//! A graph backup is not a replication Snapshot, a receipt image, or an
//! atomic replace-all operation. The caller owns the output and its separately
//! persisted manifest, and must keep a restore source immutable while in use.

use std::{
    error::Error,
    fmt,
    io::{Read, Seek, SeekFrom, Write},
};

use prost::Message;
use serde_json::json;
use sha2::{Digest, Sha256};

use crate::{
    Edge, EdgeInput, Expiration, LanternClient, LanternError, PutOutcome, StreamOptions, Vertex,
    VertexInput,
    generated::graph::v1::{
        BackupSnapshotRequest, BackupSnapshotResponse, backup_snapshot_response::Record,
    },
    transport::DataStream,
};

mod format;

const BACKUP_PATH: &str = "/graph.v1.LanternService/BackupSnapshot";
const ARCHIVE_VERSION: u32 = 1;
const MAX_RECORD_BYTES: usize = 64 * 1024 * 1024;
const DEFAULT_ARCHIVE_LIMIT: u64 = 16 * 1024 * 1024 * 1024;

/// SDK-owned archive encoding. NDJSON v1 is not Go/Node's NDJSON format.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum BackupFormat {
    LengthDelimitedProtobuf,
    RustNdjsonV1,
}

impl BackupFormat {
    fn as_str(self) -> &'static str {
        match self {
            Self::LengthDelimitedProtobuf => "protobuf-v1",
            Self::RustNdjsonV1 => "rust-ndjson-v1",
        }
    }

    fn parse(value: &str) -> Result<Self, LanternError> {
        match value {
            "protobuf-v1" => Ok(Self::LengthDelimitedProtobuf),
            "rust-ndjson-v1" => Ok(Self::RustNdjsonV1),
            _ => Err(LanternError::BackupFormat("unknown backup format")),
        }
    }
}

/// One live Vertex or one folded live Edge; it carries no contribution IDs,
/// receipt rows, causal metadata, removal floors or decay history.
#[derive(Clone, Debug, PartialEq)]
pub enum BackupRecord {
    Vertex(Vertex),
    Edge(Edge),
}

impl BackupRecord {
    fn from_wire(raw: &[u8], prefix: &str) -> Result<Self, LanternError> {
        let frame = BackupSnapshotResponse::decode(raw)
            .map_err(|_| LanternError::BackupFormat("malformed protobuf backup frame"))?;
        if frame.encoded_len() != raw.len() {
            return Err(LanternError::BackupFormat(
                "unknown or noncanonical protobuf backup fields",
            ));
        }
        let record = match frame.record {
            Some(Record::Vertex(vertex)) => Self::Vertex(vertex),
            Some(Record::Edge(edge)) => Self::Edge(edge),
            None => return Err(LanternError::BackupFormat("backup frame has no record")),
        };
        record.validate(prefix)?;
        Ok(record)
    }

    fn validate(&self, prefix: &str) -> Result<(), LanternError> {
        match self {
            Self::Vertex(vertex) => {
                vertex
                    .validate_response()
                    .map_err(|_| LanternError::BackupFormat("invalid backup Vertex"))?;
                if !vertex.key.starts_with(prefix) {
                    return Err(LanternError::BackupFormat(
                        "backup Vertex lies outside the requested prefix",
                    ));
                }
            }
            Self::Edge(edge) => {
                edge.validate_response()
                    .map_err(|_| LanternError::BackupFormat("invalid backup Edge"))?;
                if !edge.tail.starts_with(prefix) || !edge.head.starts_with(prefix) {
                    return Err(LanternError::BackupFormat(
                        "backup Edge lies outside the induced subgraph",
                    ));
                }
            }
        }
        Ok(())
    }
}

/// Successful, normal EOF is possible for this server stream; its wire format
/// has no footer/count to independently certify the serving graph's size.
pub struct BackupStream {
    wire: Option<DataStream>,
    prefix: String,
    ended: bool,
}

impl BackupStream {
    /// Receive exactly one checked frame with demand-driven backpressure.
    /// A malformed frame poisons the stream; dropping it cancels server work.
    pub async fn next_record(&mut self) -> Result<Option<BackupRecord>, LanternError> {
        if self.ended {
            return Ok(None);
        }
        let Some(wire) = self.wire.as_mut() else {
            return Err(LanternError::BackupFormat("backup stream failed"));
        };
        let result = match wire.next().await {
            Ok(Some(raw)) if raw.len() <= MAX_RECORD_BYTES => {
                BackupRecord::from_wire(&raw, &self.prefix).map(Some)
            }
            Ok(Some(_)) => Err(LanternError::BackupFormat("backup frame exceeds 64 MiB")),
            Ok(None) => {
                self.ended = true;
                Ok(None)
            }
            Err(error) => Err(error),
        };
        if result.is_err() {
            self.wire = None;
        }
        result
    }
}

/// External integrity declaration for the complete SDK archive bytes.
/// Store/transport this **separately** from the archive and authenticate it
/// as appropriate; a checksum cannot prove that the remote server sent its
/// whole graph because BackupSnapshot has no server footer.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct BackupManifest {
    format: BackupFormat,
    vertex_prefix: String,
    vertices: u64,
    edges: u64,
    bytes: u64,
    sha256: [u8; 32],
}

impl BackupManifest {
    pub fn format(&self) -> BackupFormat {
        self.format
    }

    pub fn vertex_prefix(&self) -> &str {
        &self.vertex_prefix
    }

    pub fn vertex_count(&self) -> u64 {
        self.vertices
    }

    pub fn edge_count(&self) -> u64 {
        self.edges
    }

    pub fn byte_length(&self) -> u64 {
        self.bytes
    }

    pub fn sha256_hex(&self) -> String {
        self.sha256
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect()
    }

    /// Portable, explicitly versioned manifest. Counts and length are u64
    /// JSON numbers; the digest is 64 lowercase hex digits.
    pub fn to_json(&self) -> String {
        json!({
            "version": ARCHIVE_VERSION,
            "format": self.format.as_str(),
            "vertex_prefix": self.vertex_prefix,
            "vertices": self.vertices,
            "edges": self.edges,
            "bytes": self.bytes,
            "sha256": self.sha256_hex(),
        })
        .to_string()
    }

    pub fn from_json(text: &str) -> Result<Self, LanternError> {
        #[derive(serde::Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Document<'a> {
            version: u32,
            format: &'a str,
            vertex_prefix: String,
            vertices: u64,
            edges: u64,
            bytes: u64,
            sha256: &'a str,
        }

        let document: Document<'_> = serde_json::from_str(text).map_err(LanternError::Json)?;
        if document.version != ARCHIVE_VERSION {
            return Err(LanternError::BackupFormat("unsupported manifest version"));
        }
        let format = BackupFormat::parse(document.format)?;
        let digest = document.sha256;
        if digest.len() != 64 {
            return Err(LanternError::BackupFormat("SHA-256 must be 64 hex digits"));
        }
        let mut sha256 = [0; 32];
        let (pairs, _) = digest.as_bytes().as_chunks::<2>();
        for (index, pair) in pairs.iter().enumerate() {
            let nibble = |byte: u8| match byte {
                b'0'..=b'9' => Some(byte - b'0'),
                b'a'..=b'f' => Some(byte - b'a' + 10),
                _ => None,
            };
            let hi = nibble(pair[0]).ok_or(LanternError::BackupFormat("noncanonical SHA-256"))?;
            let lo = nibble(pair[1]).ok_or(LanternError::BackupFormat("noncanonical SHA-256"))?;
            sha256[index] = (hi << 4) | lo;
        }
        Ok(Self {
            format,
            vertex_prefix: document.vertex_prefix,
            vertices: document.vertices,
            edges: document.edges,
            bytes: document.bytes,
            sha256,
        })
    }
}

/// Memory, file-size and per-Put bounds for a checked seekable restore.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct RestoreOptions {
    batch_size: usize,
    max_record_bytes: usize,
    max_archive_bytes: u64,
}

impl Default for RestoreOptions {
    fn default() -> Self {
        Self {
            batch_size: 1_000,
            max_record_bytes: MAX_RECORD_BYTES,
            max_archive_bytes: DEFAULT_ARCHIVE_LIMIT,
        }
    }
}

impl RestoreOptions {
    pub fn with_batch_size(mut self, count: usize) -> Result<Self, LanternError> {
        if !(1..=65_536).contains(&count) {
            return Err(LanternError::InvalidConfig(
                "restore batch size must be between 1 and 65536",
            ));
        }
        self.batch_size = count;
        Ok(self)
    }

    pub fn with_max_record_bytes(mut self, bytes: usize) -> Result<Self, LanternError> {
        if !(1..=MAX_RECORD_BYTES).contains(&bytes) {
            return Err(LanternError::InvalidConfig(
                "restore record limit must be between 1 byte and 64 MiB",
            ));
        }
        self.max_record_bytes = bytes;
        Ok(self)
    }

    pub fn with_max_archive_bytes(mut self, bytes: u64) -> Result<Self, LanternError> {
        if bytes == 0 {
            return Err(LanternError::InvalidConfig(
                "restore archive limit must be positive",
            ));
        }
        self.max_archive_bytes = bytes;
        Ok(self)
    }
}

/// Fully validated Put response counts, *not* a transactional commit proof.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct RestoreReport {
    pub completed_vertices: u64,
    pub completed_edges: u64,
}

/// The failed batch may have been applied or may have expired since capture.
/// Counts exclude that uncertain batch; the original failure is retained.
#[derive(Debug)]
pub struct RestoreFailure {
    pub progress: RestoreReport,
    pub source: LanternError,
}

impl fmt::Display for RestoreFailure {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "Lantern restore failed after {} validated Vertex and {} Edge responses: {}",
            self.progress.completed_vertices, self.progress.completed_edges, self.source
        )
    }
}

impl Error for RestoreFailure {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(&self.source)
    }
}

impl LanternClient {
    /// Open an optional prefix-induced graph snapshot on the current serving
    /// endpoint. Empty prefix captures the whole graph. There is no footer
    /// or remote-side checksum on this server RPC.
    pub async fn backup_snapshot(
        &self,
        vertex_prefix: impl Into<String>,
        options: StreamOptions,
    ) -> Result<BackupStream, LanternError> {
        let prefix = vertex_prefix.into();
        let wire = self
            .raw_data_stream(
                BackupSnapshotRequest {
                    vertex_prefix: prefix.clone(),
                },
                BACKUP_PATH,
                options,
            )
            .await?;
        Ok(BackupStream {
            wire: Some(wire),
            prefix,
            ended: false,
        })
    }

    /// Stream directly into caller-owned storage without buffering the graph.
    /// Persist the returned manifest separately; discard any partial output
    /// when this call fails (including a writer failure or a dropped stream).
    pub async fn write_backup<W: Write>(
        &self,
        writer: &mut W,
        vertex_prefix: impl Into<String>,
        format: BackupFormat,
        options: StreamOptions,
    ) -> Result<BackupManifest, LanternError> {
        let prefix = vertex_prefix.into();
        let mut stream = self.backup_snapshot(prefix.clone(), options).await?;
        let mut manifest = BackupManifest {
            format,
            vertex_prefix: prefix,
            vertices: 0,
            edges: 0,
            bytes: 0,
            sha256: [0; 32],
        };
        let mut hasher = Sha256::new();
        while let Some(record) = stream.next_record().await? {
            let data = format::encode(&record, format)?;
            if data.len() > MAX_RECORD_BYTES {
                return Err(LanternError::MessageTooLarge {
                    actual: data.len(),
                    limit: MAX_RECORD_BYTES,
                });
            }
            writer.write_all(&data).map_err(LanternError::Io)?;
            hasher.update(&data);
            manifest.bytes = manifest
                .bytes
                .checked_add(data.len() as u64)
                .ok_or(LanternError::BackupFormat("archive length overflow"))?;
            let count = match record {
                BackupRecord::Vertex(_) => &mut manifest.vertices,
                BackupRecord::Edge(_) => &mut manifest.edges,
            };
            *count = count
                .checked_add(1)
                .ok_or(LanternError::BackupFormat("backup record count overflow"))?;
        }
        writer.flush().map_err(LanternError::Io)?;
        manifest.sha256 = hasher.finalize().into();
        Ok(manifest)
    }

    /// Preflight the entire immutable, seekable source and trusted manifest
    /// (including all edge weights) **before any Put**. Then perform two
    /// bounded application passes: all vertices, then all edges, regardless
    /// of record order. Upserts only; no deletion or atomic rollback.
    pub async fn restore_backup<R: Read + Seek>(
        &self,
        source: &mut R,
        manifest: &BackupManifest,
        options: RestoreOptions,
    ) -> Result<RestoreReport, RestoreFailure> {
        let mut progress = RestoreReport::default();
        let start = source.stream_position().map_err(|error| RestoreFailure {
            progress,
            source: LanternError::Io(error),
        })?;
        let result = self
            .restore_inner(source, start, manifest, options, &mut progress)
            .await;
        result.map_err(|source| RestoreFailure { progress, source })
    }

    async fn restore_inner<R: Read + Seek>(
        &self,
        source: &mut R,
        start: u64,
        manifest: &BackupManifest,
        options: RestoreOptions,
        progress: &mut RestoreReport,
    ) -> Result<RestoreReport, LanternError> {
        if manifest.bytes > options.max_archive_bytes {
            return Err(LanternError::BackupFormat(
                "archive exceeds configured limit",
            ));
        }
        let end = source.seek(SeekFrom::End(0)).map_err(LanternError::Io)?;
        if end.checked_sub(start) != Some(manifest.bytes) {
            return Err(LanternError::BackupIntegrity(
                "archive length differs from the manifest",
            ));
        }
        self.visit_archive(source, start, manifest, options, |record| {
            format::check_restorable(record)?;
            self.put_record_size(record)?;
            Ok(())
        })?;
        self.apply_pass(source, start, manifest, options, true, progress)
            .await?;
        self.apply_pass(source, start, manifest, options, false, progress)
            .await?;
        Ok(*progress)
    }

    fn visit_archive<R: Read + Seek>(
        &self,
        source: &mut R,
        start: u64,
        manifest: &BackupManifest,
        options: RestoreOptions,
        mut visit: impl FnMut(&BackupRecord) -> Result<(), LanternError>,
    ) -> Result<(), LanternError> {
        source
            .seek(SeekFrom::Start(start))
            .map_err(LanternError::Io)?;
        let mut reader = format::ArchiveReader::new(
            source.take(manifest.bytes),
            manifest.format,
            options.max_record_bytes,
        );
        let mut vertices = 0_u64;
        let mut edges = 0_u64;
        while let Some(record) = reader.next_record(&manifest.vertex_prefix)? {
            visit(&record)?;
            let count = match record {
                BackupRecord::Vertex(_) => &mut vertices,
                BackupRecord::Edge(_) => &mut edges,
            };
            *count = count
                .checked_add(1)
                .ok_or(LanternError::BackupFormat("archive record count overflow"))?;
        }
        let (bytes, digest) = reader.finish();
        if bytes != manifest.bytes
            || vertices != manifest.vertices
            || edges != manifest.edges
            || digest != manifest.sha256
        {
            return Err(LanternError::BackupIntegrity(
                "archive checksum, byte length or record counts differ",
            ));
        }
        Ok(())
    }

    async fn apply_pass<R: Read + Seek>(
        &self,
        source: &mut R,
        start: u64,
        manifest: &BackupManifest,
        options: RestoreOptions,
        vertices_first: bool,
        progress: &mut RestoreReport,
    ) -> Result<(), LanternError> {
        source
            .seek(SeekFrom::Start(start))
            .map_err(LanternError::Io)?;
        let mut reader = format::ArchiveReader::new(
            source.take(manifest.bytes),
            manifest.format,
            options.max_record_bytes,
        );
        let mut vertex_batch = Vec::new();
        let mut edge_batch = Vec::new();
        let mut batch_bytes = 0_usize;
        let mut vertices = 0_u64;
        let mut edges = 0_u64;
        while let Some(record) = reader.next_record(&manifest.vertex_prefix)? {
            let matches_pass = match &record {
                BackupRecord::Vertex(_) => {
                    vertices += 1;
                    vertices_first
                }
                BackupRecord::Edge(_) => {
                    edges += 1;
                    !vertices_first
                }
            };
            if !matches_pass {
                continue;
            }
            format::check_restorable(&record)?;
            let size = self.put_record_size(&record)?;
            let count = if vertices_first {
                vertex_batch.len()
            } else {
                edge_batch.len()
            };
            if count > 0
                && (count >= options.batch_size
                    || batch_bytes.saturating_add(size) > self.encode_limit())
            {
                self.flush_restore(&mut vertex_batch, &mut edge_batch, progress)
                    .await?;
                batch_bytes = 0;
            }
            batch_bytes += size;
            match record {
                BackupRecord::Vertex(vertex) => vertex_batch.push(vertex_input(vertex)),
                BackupRecord::Edge(edge) => edge_batch.push(edge_input(edge)),
            }
        }
        self.flush_restore(&mut vertex_batch, &mut edge_batch, progress)
            .await?;
        let (bytes, digest) = reader.finish();
        if bytes != manifest.bytes
            || vertices != manifest.vertices
            || edges != manifest.edges
            || digest != manifest.sha256
        {
            return Err(LanternError::BackupIntegrity(
                "archive changed during restore",
            ));
        }
        Ok(())
    }

    fn put_record_size(&self, record: &BackupRecord) -> Result<usize, LanternError> {
        let mut length = match record {
            BackupRecord::Vertex(vertex) => vertex.encoded_len(),
            BackupRecord::Edge(edge) => edge.encoded_len(),
        };
        let mut value = length;
        loop {
            length += 1;
            value >>= 7;
            if value == 0 {
                break;
            }
        }
        length += 1; // repeated message field tag in a plural Put request
        if length > self.encode_limit() {
            return Err(LanternError::MessageTooLarge {
                actual: length,
                limit: self.encode_limit(),
            });
        }
        Ok(length)
    }

    async fn flush_restore(
        &self,
        vertex_batch: &mut Vec<VertexInput>,
        edge_batch: &mut Vec<EdgeInput>,
        progress: &mut RestoreReport,
    ) -> Result<(), LanternError> {
        let vertices = vertex_batch.len();
        if vertices != 0 {
            let batch = std::mem::take(vertex_batch);
            match self.put_vertices(batch).await {
                Ok(outcomes) => {
                    if let Some(outcome) = outcomes
                        .into_iter()
                        .find(|outcome| *outcome != PutOutcome::AppliedAndLive)
                    {
                        return Err(LanternError::RestoreOutcome(outcome));
                    }
                    progress.completed_vertices += vertices as u64;
                }
                Err(error) => {
                    if let LanternError::Batch(batch) = &error {
                        progress.completed_vertices += batch.completed_items as u64;
                    }
                    return Err(error);
                }
            }
        }
        let edges = edge_batch.len();
        if edges != 0 {
            let batch = std::mem::take(edge_batch);
            match self.put_edges(batch).await {
                Ok(outcomes) => {
                    if let Some(outcome) = outcomes
                        .into_iter()
                        .find(|outcome| *outcome != PutOutcome::AppliedAndLive)
                    {
                        return Err(LanternError::RestoreOutcome(outcome));
                    }
                    progress.completed_edges += edges as u64;
                }
                Err(error) => {
                    if let LanternError::Batch(batch) = &error {
                        progress.completed_edges += batch.completed_items as u64;
                    }
                    return Err(error);
                }
            }
        }
        Ok(())
    }
}

fn vertex_input(vertex: Vertex) -> VertexInput {
    VertexInput::new(vertex.key, vertex.value)
        .with_expiration(vertex.expiration.map_or(Expiration::Never, Expiration::At))
}

fn edge_input(edge: Edge) -> EdgeInput {
    EdgeInput::new(edge.tail, edge.head, edge.weight)
        .with_expiration(edge.expiration.map_or(Expiration::Never, Expiration::At))
}

#[cfg(test)]
mod tests;
