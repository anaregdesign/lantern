use std::{
    io::{Cursor, Write},
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use super::*;
use crate::{AddInput, Timestamp, VertexValue, test_server::GoServer};

fn vertex(key: &str, value: Option<VertexValue>) -> BackupRecord {
    BackupRecord::Vertex(Vertex {
        key: key.into(),
        value,
        expiration: None,
    })
}

fn archive(
    records: &[BackupRecord],
    prefix: &str,
    format: BackupFormat,
) -> (Vec<u8>, BackupManifest) {
    let mut bytes = Vec::new();
    let mut vertices = 0_u64;
    let mut edges = 0_u64;
    for record in records {
        bytes.extend(format::encode(record, format).unwrap());
        match record {
            BackupRecord::Vertex(_) => vertices += 1,
            BackupRecord::Edge(_) => edges += 1,
        }
    }
    let sha256 = Sha256::digest(&bytes).into();
    let manifest = BackupManifest {
        format,
        vertex_prefix: prefix.into(),
        vertices,
        edges,
        bytes: bytes.len() as u64,
        sha256,
    };
    (bytes, manifest)
}

fn records() -> Vec<BackupRecord> {
    let seconds = 2_000_000_000_i64;
    let nanos = 123_456_789;
    [
        None,
        Some(VertexValue::Nil(true)),
        Some(VertexValue::Int32(i32::MIN)),
        Some(VertexValue::Int64(i64::MIN)),
        Some(VertexValue::Uint32(u32::MAX)),
        Some(VertexValue::Uint64(u64::MAX)),
        Some(VertexValue::Float32(f32::from_bits(0x7fc0_0011))),
        Some(VertexValue::Float64(f64::from_bits(0xfff8_0000_0000_0022))),
        Some(VertexValue::Bool(false)),
        Some(VertexValue::String("newline\n\u{1f642}".into())),
        Some(VertexValue::Bytes(vec![0, 255, 10, 13])),
        Some(VertexValue::Timestamp(Timestamp { seconds, nanos })),
        Some(VertexValue::Duration(crate::ProtoDuration {
            seconds: -42,
            nanos: -234,
        })),
    ]
    .into_iter()
    .enumerate()
    .map(|(index, value)| vertex(&format!("rust:backup:{index:02}"), value))
    .chain([BackupRecord::Edge(Edge {
        tail: "rust:backup:00".into(),
        head: "rust:backup:01".into(),
        weight: 0.0,
        expiration: Some(Timestamp { seconds, nanos }),
    })])
    .collect()
}

#[test]
fn rust_ndjson_retains_negative_zero_edge_bit_pattern() {
    let edge = BackupRecord::Edge(Edge {
        tail: "rust:a".into(),
        head: "rust:b".into(),
        weight: -0.0,
        expiration: None,
    });
    let raw = format::encode(&edge, BackupFormat::RustNdjsonV1).unwrap();
    let mut reader =
        format::ArchiveReader::new(raw.as_slice(), BackupFormat::RustNdjsonV1, MAX_RECORD_BYTES);
    let Some(BackupRecord::Edge(decoded)) = reader.next_record("").unwrap() else {
        panic!("expected edge");
    };
    assert_eq!(decoded.weight.to_bits(), (-0.0_f32).to_bits());
}

#[test]
fn both_archive_formats_roundtrip_all_exact_values_and_manifest() {
    for format in [
        BackupFormat::LengthDelimitedProtobuf,
        BackupFormat::RustNdjsonV1,
    ] {
        let expected = records();
        let (bytes, manifest) = archive(&expected, "rust:backup:", format);
        assert_eq!(manifest.vertex_count(), 13);
        assert_eq!(manifest.edge_count(), 1);
        assert_eq!(manifest.byte_length(), bytes.len() as u64);
        let encoded = manifest.to_json();
        assert_eq!(BackupManifest::from_json(&encoded).unwrap(), manifest);
        let mut reader = format::ArchiveReader::new(bytes.as_slice(), format, MAX_RECORD_BYTES);
        let mut decoded = Vec::new();
        while let Some(record) = reader.next_record("rust:backup:").unwrap() {
            decoded.push(record);
        }
        let (length, hash) = reader.finish();
        assert_eq!(length, manifest.bytes);
        assert_eq!(hash, manifest.sha256);
        assert_eq!(decoded.len(), expected.len());
        for (original, restored) in expected.iter().zip(&decoded) {
            match (original, restored) {
                (BackupRecord::Vertex(a), BackupRecord::Vertex(b)) => {
                    assert_eq!(a.key, b.key);
                    assert_eq!(a.expiration, b.expiration);
                    match (&a.value, &b.value) {
                        (Some(VertexValue::Float32(a)), Some(VertexValue::Float32(b))) => {
                            assert_eq!(a.to_bits(), b.to_bits());
                        }
                        (Some(VertexValue::Float64(a)), Some(VertexValue::Float64(b))) => {
                            assert_eq!(a.to_bits(), b.to_bits());
                        }
                        (a, b) => assert_eq!(a, b),
                    }
                }
                (BackupRecord::Edge(a), BackupRecord::Edge(b)) => {
                    assert_eq!(a.weight.to_bits(), b.weight.to_bits());
                    assert_eq!(a.expiration, b.expiration);
                }
                _ => panic!("record kind changed"),
            }
        }
    }
}

#[test]
fn corrupt_truncated_unknown_and_oversize_archives_fail_explicitly() {
    for format in [
        BackupFormat::LengthDelimitedProtobuf,
        BackupFormat::RustNdjsonV1,
    ] {
        let (bytes, manifest) = archive(&[vertex("rust:backup:a", None)], "rust:backup:", format);
        for data in [bytes[..bytes.len() - 1].to_vec(), {
            let mut value = bytes.clone();
            value[0] ^= 0x40;
            value
        }] {
            let mut reader = format::ArchiveReader::new(data.as_slice(), format, MAX_RECORD_BYTES);
            let result = reader.next_record("rust:backup:");
            match result {
                Ok(Some(_)) => assert_ne!(reader.finish().1, manifest.sha256),
                Err(LanternError::BackupFormat(_) | LanternError::Json(_)) => (),
                other => panic!("unexpected corrupt archive result: {other:?}"),
            }
        }
        let mut reader = format::ArchiveReader::new(bytes.as_slice(), format, 2);
        assert!(matches!(
            reader.next_record("rust:backup:"),
            Err(LanternError::BackupFormat(_))
        ));
        let mut reader = format::ArchiveReader::new(bytes.as_slice(), format, MAX_RECORD_BYTES);
        assert!(reader.next_record("different:").is_err());
    }
    let mut reader =
        format::ArchiveReader::new(&[0x80][..], BackupFormat::LengthDelimitedProtobuf, 32);
    assert!(matches!(
        reader.next_record(""),
        Err(LanternError::BackupFormat(_))
    ));
    let mut reader =
        format::ArchiveReader::new(&[0x80, 0x00][..], BackupFormat::LengthDelimitedProtobuf, 32);
    assert!(matches!(
        reader.next_record(""),
        Err(LanternError::BackupFormat(_))
    ));
    let mut reader = format::ArchiveReader::new(
        b"{\"version\":1,\"vertex\":{},\"unknown\":false}\n".as_slice(),
        BackupFormat::RustNdjsonV1,
        MAX_RECORD_BYTES,
    );
    assert!(matches!(
        reader.next_record(""),
        Err(LanternError::BackupFormat(_))
    ));
    let canonical =
        format::encode(&vertex("rust:backup:dup", None), BackupFormat::RustNdjsonV1).unwrap();
    let duplicate = String::from_utf8(canonical).unwrap().replacen(
        "\"version\":1",
        "\"version\":1,\"version\":1",
        1,
    );
    let mut reader = format::ArchiveReader::new(
        duplicate.as_bytes(),
        BackupFormat::RustNdjsonV1,
        MAX_RECORD_BYTES,
    );
    assert!(matches!(
        reader.next_record("rust:backup:"),
        Err(LanternError::BackupFormat(_))
    ));
    let mut reader = format::ArchiveReader::new(
        &[0x4a, 0x00][..],
        BackupFormat::LengthDelimitedProtobuf,
        MAX_RECORD_BYTES,
    );
    assert!(matches!(
        reader.next_record(""),
        Err(LanternError::BackupFormat(_))
    ));
}

#[test]
fn nonfinite_folded_edge_and_elapsed_deadline_are_unsupported_before_put() {
    let nonfinite = BackupRecord::Edge(Edge {
        tail: "rust:a".into(),
        head: "rust:b".into(),
        weight: f32::INFINITY,
        expiration: None,
    });
    assert!(matches!(
        format::check_restorable(&nonfinite),
        Err(LanternError::UnsupportedBackup(_))
    ));
    let expired = BackupRecord::Vertex(Vertex {
        key: "rust:expired".into(),
        value: None,
        expiration: Some(Timestamp {
            seconds: 1_500_000_000,
            nanos: 0,
        }),
    });
    assert!(matches!(
        format::check_restorable(&expired),
        Err(LanternError::UnsupportedBackup(_))
    ));
    let future = BackupRecord::Vertex(Vertex {
        key: "rust:future".into(),
        value: None,
        expiration: Some(Timestamp {
            seconds: 2_000_000_000,
            nanos: 0,
        }),
    });
    format::check_restorable(&future).unwrap();
}

#[test]
fn manifest_rejects_unknown_format_version_and_noncanonical_digest() {
    let (_, manifest) = archive(&[], "", BackupFormat::RustNdjsonV1);
    for corrupted in [
        manifest.to_json().replace("\"version\":1", "\"version\":2"),
        manifest.to_json().replace("rust-ndjson-v1", "go-ndjson"),
        manifest
            .to_json()
            .replacen("\"version\":1", "\"version\":1,\"version\":1", 1),
        manifest.to_json().replace(
            &manifest.sha256_hex(),
            &manifest.sha256_hex().to_ascii_uppercase(),
        ),
        format!("{{\"unknown\":false,{}", &manifest.to_json()[1..]),
    ] {
        assert!(BackupManifest::from_json(&corrupted).is_err());
    }
}

struct FailingWriter {
    bytes: usize,
}

impl Write for FailingWriter {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        if self.bytes == 0 {
            return Err(std::io::Error::other("injected writer failure"));
        }
        let size = buf.len().min(self.bytes);
        self.bytes -= size;
        Ok(size)
    }

    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

#[test]
fn restore_options_have_explicit_limits() {
    let options = RestoreOptions::default()
        .with_batch_size(1)
        .unwrap()
        .with_max_record_bytes(256)
        .unwrap()
        .with_max_archive_bytes(16)
        .unwrap();
    assert_eq!(options.batch_size, 1);
    assert!(RestoreOptions::default().with_batch_size(0).is_err());
    assert!(
        RestoreOptions::default()
            .with_max_record_bytes(MAX_RECORD_BYTES + 1)
            .is_err()
    );
    assert!(RestoreOptions::default().with_max_archive_bytes(0).is_err());
    let _writer = FailingWriter { bytes: 1 };
}

#[tokio::test]
#[ignore = "requires a built production Go server via LANTERN_RUST_TEST_SERVER"]
async fn real_wire_backup_prefix_restore_corruption_and_partial_progress()
-> Result<(), Box<dyn std::error::Error>> {
    let mut source_server = GoServer::start(&[])?;
    let mut destination_server = GoServer::start(&[])?;
    source_server.wait_for_listener()?;
    destination_server.wait_for_listener()?;
    let source = LanternClient::builder(format!("http://127.0.0.1:{}", source_server.port()))
        .connect()
        .await?;
    let destination =
        LanternClient::builder(format!("http://127.0.0.1:{}", destination_server.port()))
            .connect()
            .await?;
    let mut empty_archive = Vec::new();
    let empty = tokio::time::timeout(
        Duration::from_secs(3),
        source.write_backup(
            &mut empty_archive,
            "rust:empty-backup:",
            BackupFormat::LengthDelimitedProtobuf,
            StreamOptions::default(),
        ),
    )
    .await??;
    assert_eq!((empty.vertex_count(), empty.edge_count()), (0, 0));
    assert!(empty_archive.is_empty());
    let prefix = "rust:backup-real:";
    let expiration = SystemTime::now() + Duration::from_secs(600);
    let from_epoch = expiration.duration_since(UNIX_EPOCH)?;
    let expiration = Timestamp {
        seconds: from_epoch.as_secs() as i64,
        nanos: from_epoch.subsec_nanos() as i32,
    };
    source
        .put_vertices([
            VertexInput::int64(format!("{prefix}tail"), i64::MAX)
                .with_expiration(Expiration::At(expiration)),
            VertexInput::bytes(format!("{prefix}head"), [0, 255, 1]),
            VertexInput::nil("rust:outside"),
        ])
        .await?;
    source
        .put_edge(
            EdgeInput::new(format!("{prefix}tail"), format!("{prefix}head"), 1.0)
                .with_expiration(Expiration::At(expiration)),
        )
        .await?;
    source
        .add_edge(AddInput::new(EdgeInput::new(
            format!("{prefix}tail"),
            format!("{prefix}head"),
            2.0,
        )))
        .await?;
    source
        .put_edge(EdgeInput::new(format!("{prefix}tail"), "rust:outside", 4.0))
        .await?;
    for format in [
        BackupFormat::LengthDelimitedProtobuf,
        BackupFormat::RustNdjsonV1,
    ] {
        assert!(matches!(
            source
                .write_backup(
                    &mut FailingWriter { bytes: 1 },
                    prefix,
                    format,
                    StreamOptions::default(),
                )
                .await,
            Err(LanternError::Io(_))
        ));
        let mut data = Vec::new();
        let manifest = source
            .write_backup(&mut data, prefix, format, StreamOptions::default())
            .await?;
        assert_eq!((manifest.vertex_count(), manifest.edge_count()), (2, 1));
        let persisted = BackupManifest::from_json(&manifest.to_json())?;
        assert_eq!(manifest, persisted);
        let mut corrupt = data.clone();
        let middle = corrupt.len() / 2;
        corrupt[middle] ^= 1;
        let err = destination
            .restore_backup(
                &mut Cursor::new(corrupt),
                &manifest,
                RestoreOptions::default(),
            )
            .await
            .expect_err("tampering must fail before any Put");
        assert_eq!(err.progress, RestoreReport::default());
        if format == BackupFormat::LengthDelimitedProtobuf {
            assert!(
                destination
                    .get_vertex(format!("{prefix}tail"))
                    .await
                    .is_err()
            );
        }

        let restored = destination
            .restore_backup(&mut Cursor::new(data), &manifest, RestoreOptions::default())
            .await?;
        assert_eq!(restored.completed_vertices, 2);
        assert_eq!(restored.completed_edges, 1);
        let tail = destination.get_vertex(format!("{prefix}tail")).await?;
        assert_eq!(tail.int64_value(), Some(i64::MAX));
        assert_eq!(tail.expiration, Some(expiration));
        assert_eq!(
            destination
                .get_vertex(format!("{prefix}head"))
                .await?
                .bytes_value(),
            Some([0, 255, 1].as_slice())
        );
        let edge = destination
            .get_edge(format!("{prefix}tail"), format!("{prefix}head"))
            .await?;
        assert_eq!(edge.weight, 3.0);
        assert!(destination.get_vertex("rust:outside").await.is_err());
        assert!(
            destination
                .get_edge(format!("{prefix}tail"), "rust:outside")
                .await
                .is_err()
        );
    }

    // A valid, application-owned archive can still contain a folded weight
    // impossible to restore via public Puts. Reject before writing a Vertex.
    let impossible = [
        vertex("rust:nonfinite:tail", Some(VertexValue::Nil(true))),
        BackupRecord::Edge(Edge {
            tail: "rust:nonfinite:tail".into(),
            head: "rust:nonfinite:tail".into(),
            weight: f32::INFINITY,
            expiration: None,
        }),
    ];
    let (data, manifest) = archive(
        &impossible,
        "rust:nonfinite:",
        BackupFormat::LengthDelimitedProtobuf,
    );
    let err = destination
        .restore_backup(&mut Cursor::new(data), &manifest, RestoreOptions::default())
        .await
        .expect_err("nonfinite folded edge must be rejected before writes");
    assert!(matches!(err.source, LanternError::UnsupportedBackup(_)));
    assert_eq!(err.progress, RestoreReport::default());
    assert!(destination.get_vertex("rust:nonfinite:tail").await.is_err());

    let small_limit =
        LanternClient::builder(format!("http://127.0.0.1:{}", destination_server.port()))
            .message_limits(128, 1024)?
            .connect()
            .await?;
    let large_put = [
        vertex("rust:preflight:valid", None),
        vertex(&format!("rust:preflight:{}", "x".repeat(256)), None),
    ];
    let (data, manifest) = archive(
        &large_put,
        "rust:preflight:",
        BackupFormat::LengthDelimitedProtobuf,
    );
    let err = small_limit
        .restore_backup(&mut Cursor::new(data), &manifest, RestoreOptions::default())
        .await
        .expect_err("one oversized Put must fail before any earlier Vertex is written");
    assert_eq!(err.progress, RestoreReport::default());
    assert!(matches!(err.source, LanternError::MessageTooLarge { .. }));
    assert!(
        destination
            .get_vertex("rust:preflight:valid")
            .await
            .is_err()
    );

    let partial = [
        vertex("rust:partial:valid", Some(VertexValue::Int32(1))),
        vertex(
            &format!("rust:partial:{}", "x".repeat(50_000)),
            Some(VertexValue::Int32(2)),
        ),
    ];
    let (data, manifest) = archive(&partial, "rust:partial:", BackupFormat::RustNdjsonV1);
    let options = RestoreOptions::default().with_batch_size(1)?;
    let err = destination
        .restore_backup(&mut Cursor::new(data), &manifest, options)
        .await
        .expect_err("server rejects a later oversized key");
    assert_eq!(err.progress.completed_vertices, 1);
    assert!(destination.get_vertex("rust:partial:valid").await.is_ok());
    Ok(())
}
