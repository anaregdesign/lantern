use std::{
    error::Error,
    future::{Future, pending},
    pin::Pin,
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    time::Duration,
};

use futures_util::StreamExt;

use super::*;
use crate::{
    EdgeInput, Expiration, RpcErrorKind, Timestamp, TokenError, TokenProvider, VertexInput,
    VertexValue, test_server::GoServer,
};

type TestResult = Result<(), Box<dyn Error>>;

fn vertex(key: &str) -> Vertex {
    Vertex {
        key: key.into(),
        value: None,
        expiration: None,
    }
}

fn edge(tail: &str, head: &str) -> Edge {
    Edge {
        tail: tail.into(),
        head: head.into(),
        weight: 1.0,
        expiration: None,
    }
}

fn assert_protocol<T>(result: Result<T, LanternError>) {
    assert!(matches!(result, Err(LanternError::Protocol(_))));
}

fn assert_server_invalid_cursor(error: LanternError) {
    assert!(
        matches!(&error, LanternError::Rpc(failure)
            if failure.kind() == RpcErrorKind::InvalidArgument),
        "expected the Go server's INVALID_ARGUMENT: {error}"
    );
}

fn assert_local_cursor_mismatch(error: LanternError) {
    assert!(
        matches!(&error, LanternError::InvalidInput(_)),
        "expected a local cursor-binding error: {error}"
    );
}

#[test]
fn sdk_cursor_round_trip_and_local_binding_guards() {
    let endpoint = "http://127.0.0.1:6380";
    let scan = ScanOptions {
        prefix: "scope/".into(),
        limit: 2,
        order: ScanOrder::Asc,
    };
    let vertex_binding = CursorBinding::vertices(endpoint, &scan);
    let raw = b"do-not-log-this-cursor".to_vec();
    let vertex = VertexCursor::issued(vertex_binding, raw.clone());
    let bytes = vertex.as_bytes().to_vec();
    assert!(bytes.starts_with(CURSOR_MAGIC));
    assert_ne!(bytes, raw);
    assert_eq!(VertexCursor::from_bytes(bytes.clone()).unwrap(), vertex);
    assert_eq!(vertex.clone().into_bytes(), bytes);
    assert_eq!(vertex.server_cursor(vertex_binding).unwrap(), raw);
    assert!(!format!("{vertex:?}").contains("do-not-log"));
    let unspecified = ScanOptions {
        order: ScanOrder::Unspecified,
        ..scan.clone()
    };
    let vertex_default = VertexCursor::issued(
        CursorBinding::vertices(endpoint, &unspecified),
        b"default-order".to_vec(),
    );
    assert_eq!(
        decode_bound_cursor(vertex_default.as_bytes(), CursorFamily::Vertices)
            .unwrap()
            .order,
        ScanOrder::Asc as i32
    );
    assert_eq!(
        vertex
            .server_cursor(CursorBinding::vertices(endpoint, &unspecified))
            .unwrap(),
        raw
    );
    assert_eq!(
        vertex_default.server_cursor(vertex_binding).unwrap(),
        b"default-order".to_vec()
    );

    for candidate in [raw, Vec::new(), vec![0xff]] {
        assert!(matches!(
            VertexCursor::from_bytes(candidate),
            Err(LanternError::InvalidInput(_))
        ));
    }
    for changed in [
        ScanOptions {
            prefix: "other/".into(),
            ..scan.clone()
        },
        ScanOptions {
            limit: 3,
            ..scan.clone()
        },
        ScanOptions {
            order: ScanOrder::Desc,
            ..scan.clone()
        },
    ] {
        assert!(matches!(
            vertex.server_cursor(CursorBinding::vertices(endpoint, &changed)),
            Err(LanternError::InvalidInput(_))
        ));
    }
    assert!(matches!(
        vertex.server_cursor(CursorBinding::vertices("http://127.0.0.1:6381", &scan)),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        KeyCursor::from_bytes(bytes.clone()),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        EdgeCursor::from_bytes(bytes.clone()),
        Err(LanternError::InvalidInput(_))
    ));
    let mut future = decode_bound_cursor(&bytes, CursorFamily::Vertices).unwrap();
    future.version += 1;
    assert!(matches!(
        VertexCursor::from_bytes(encode_bound_cursor(&future)),
        Err(LanternError::InvalidInput(_))
    ));
    let mut truncated = bytes.clone();
    truncated.pop();
    let mut trailing_unknown = bytes.clone();
    trailing_unknown.extend_from_slice(&[0x98, 0x06, 0x01]);
    let mut malformed_trailing = bytes.clone();
    malformed_trailing.push(0xff);
    for malformed in [truncated, trailing_unknown, malformed_trailing] {
        assert!(matches!(
            VertexCursor::from_bytes(malformed),
            Err(LanternError::InvalidInput(_))
        ));
    }
    let mut invalid_order = decode_bound_cursor(&bytes, CursorFamily::Vertices).unwrap();
    invalid_order.order = i32::MAX;
    assert!(matches!(
        VertexCursor::from_bytes(encode_bound_cursor(&invalid_order)),
        Err(LanternError::InvalidInput(_))
    ));
    invalid_order.order = ScanOrder::Unspecified as i32;
    assert!(matches!(
        VertexCursor::from_bytes(encode_bound_cursor(&invalid_order)),
        Err(LanternError::InvalidInput(_))
    ));
    let mut no_server_cursor = decode_bound_cursor(&bytes, CursorFamily::Vertices).unwrap();
    no_server_cursor.server_cursor.clear();
    assert!(matches!(
        VertexCursor::from_bytes(encode_bound_cursor(&no_server_cursor)),
        Err(LanternError::InvalidInput(_))
    ));
    let mut oversized = bytes.clone();
    oversized.resize(MAX_CURSOR_BYTES + 1, 0);
    assert!(matches!(
        VertexCursor::from_bytes(oversized),
        Err(LanternError::InvalidInput(
            "expected a versioned SDK-issued scan cursor"
        ))
    ));

    let key = KeyCursor::issued(CursorBinding::keys(endpoint, &scan), b"keys".to_vec());
    assert_eq!(
        KeyCursor::from_bytes(key.as_bytes().to_vec())
            .unwrap()
            .server_cursor(CursorBinding::keys(endpoint, &scan))
            .unwrap(),
        b"keys".to_vec()
    );
    let key_default = KeyCursor::issued(
        CursorBinding::keys(endpoint, &unspecified),
        b"default-keys".to_vec(),
    );
    assert_eq!(
        key.server_cursor(CursorBinding::keys(endpoint, &unspecified))
            .unwrap(),
        b"keys".to_vec()
    );
    assert_eq!(
        key_default
            .server_cursor(CursorBinding::keys(endpoint, &scan))
            .unwrap(),
        b"default-keys".to_vec()
    );
    let descending = ScanOptions {
        order: ScanOrder::Desc,
        ..scan.clone()
    };
    assert!(matches!(
        key.server_cursor(CursorBinding::keys(endpoint, &descending)),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        key_default.server_cursor(CursorBinding::keys(endpoint, &descending)),
        Err(LanternError::InvalidInput(_))
    ));
    let edge_scan = EdgeScanOptions {
        tail_prefix: "tails/".into(),
        head_prefix: "heads/".into(),
        limit: 2,
    };
    let edge = EdgeCursor::issued(
        CursorBinding::edges(endpoint, &edge_scan),
        b"edges".to_vec(),
    );
    assert_eq!(
        EdgeCursor::from_bytes(edge.as_bytes().to_vec())
            .unwrap()
            .server_cursor(CursorBinding::edges(endpoint, &edge_scan))
            .unwrap(),
        b"edges".to_vec()
    );
    for changed in [
        EdgeScanOptions {
            tail_prefix: "other/".into(),
            ..edge_scan.clone()
        },
        EdgeScanOptions {
            head_prefix: "other/".into(),
            ..edge_scan.clone()
        },
        EdgeScanOptions {
            limit: 3,
            ..edge_scan.clone()
        },
    ] {
        assert!(matches!(
            edge.server_cursor(CursorBinding::edges(endpoint, &changed)),
            Err(LanternError::InvalidInput(_))
        ));
    }
    assert!(matches!(
        edge.server_cursor(CursorBinding::edges("http://127.0.0.1:6381", &edge_scan)),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(VertexCursor::default().as_bytes().is_empty());
    assert!(KeyCursor::default().as_bytes().is_empty());
    assert!(EdgeCursor::default().as_bytes().is_empty());
    assert_eq!(
        VertexCursor::default()
            .server_cursor(vertex_binding)
            .unwrap(),
        Vec::<u8>::new()
    );
}

#[test]
fn controlled_vertex_and_key_pages_validate_order_scope_and_continuation() {
    let asc = ScanOptions {
        prefix: "a/".into(),
        limit: 2,
        order: ScanOrder::Asc,
    };
    let endpoint = "http://fixture.test";
    let asc_binding = CursorBinding::vertices(endpoint, &asc);
    let page = validate_vertices_page(
        &asc,
        b"",
        ScanVerticesResponse {
            vertices: vec![vertex("a/1"), vertex("a/2")],
            next_cursor: vec![1, 2, 3],
        },
        asc_binding,
    )
    .unwrap();
    assert_eq!(page.items.len(), 2);
    let cursor = page.next_cursor.unwrap();
    assert_eq!(cursor.server_cursor(asc_binding).unwrap(), [1, 2, 3]);
    assert!(VertexCursor::from_bytes(cursor.as_bytes().to_vec()).is_ok());

    let desc = ScanOptions {
        order: ScanOrder::Desc,
        ..asc.clone()
    };
    assert!(
        validate_vertices_page(
            &desc,
            b"",
            ScanVerticesResponse {
                vertices: vec![vertex("a/2"), vertex("a/1")],
                next_cursor: Vec::new(),
            },
            CursorBinding::vertices(endpoint, &desc),
        )
        .is_ok()
    );
    for bad in [
        vec![vertex("a/2"), vertex("a/1")],
        vec![vertex("a/1"), vertex("a/1")],
        vec![vertex("a/1"), vertex("elsewhere")],
        vec![vertex("a/1"), vertex("a/2"), vertex("a/3")],
        vec![Vertex {
            value: Some(VertexValue::Nil(false)),
            ..vertex("a/1")
        }],
    ] {
        assert_protocol(validate_vertices_page(
            &asc,
            b"",
            ScanVerticesResponse {
                vertices: bad,
                next_cursor: Vec::new(),
            },
            asc_binding,
        ));
    }
    assert_protocol(validate_vertices_page(
        &asc,
        b"",
        ScanVerticesResponse {
            vertices: vec![],
            next_cursor: vec![1],
        },
        asc_binding,
    ));
    assert_protocol(validate_vertices_page(
        &asc,
        &[1],
        ScanVerticesResponse {
            vertices: vec![vertex("a/1")],
            next_cursor: vec![1],
        },
        asc_binding,
    ));

    assert!(matches!(
        require_key_prefix(&ScanOptions::default()),
        Err(LanternError::InvalidInput(_))
    ));
    let keys = validate_keys_page(
        &desc,
        b"",
        ScanVertexKeysResponse {
            keys: vec!["a/2".into(), "a/1".into()],
            next_cursor: vec![7],
        },
        CursorBinding::keys(endpoint, &desc),
    )
    .unwrap();
    assert_eq!(
        keys.next_cursor
            .unwrap()
            .server_cursor(CursorBinding::keys(endpoint, &desc))
            .unwrap(),
        [7]
    );
    for bad in [
        vec!["".into()],
        vec!["different".into()],
        vec!["a/1".into(), "a/2".into()],
        vec!["a/2".into(), "a/2".into()],
        vec!["a/3".into(), "a/2".into(), "a/1".into()],
    ] {
        assert_protocol(validate_keys_page(
            &desc,
            b"",
            ScanVertexKeysResponse {
                keys: bad,
                next_cursor: Vec::new(),
            },
            CursorBinding::keys(endpoint, &desc),
        ));
    }
}

#[test]
fn controlled_edge_pages_validate_pair_order_scope_and_expiration() {
    let scan = EdgeScanOptions {
        tail_prefix: "tail/".into(),
        head_prefix: "head/".into(),
        limit: 2,
    };
    let binding = CursorBinding::edges("http://fixture.test", &scan);
    let page = validate_edges_page(
        &scan,
        b"",
        ScanEdgesResponse {
            edges: vec![edge("tail/a", "head/a"), edge("tail/a", "head/b")],
            next_cursor: vec![9],
        },
        binding,
    )
    .unwrap();
    assert_eq!(page.items.len(), 2);
    assert_eq!(
        page.next_cursor.unwrap().server_cursor(binding).unwrap(),
        [9]
    );
    for bad in [
        vec![edge("tail/b", "head/a"), edge("tail/a", "head/z")],
        vec![edge("tail/a", "head/a"), edge("tail/a", "head/a")],
        vec![edge("other", "head/a")],
        vec![edge("tail/a", "other")],
        vec![
            edge("tail/a", "head/a"),
            edge("tail/a", "head/b"),
            edge("tail/b", "head/a"),
        ],
        vec![Edge {
            expiration: Some(Timestamp {
                seconds: 253_402_300_800,
                nanos: 0,
            }),
            ..edge("tail/a", "head/a")
        }],
    ] {
        assert_protocol(validate_edges_page(
            &scan,
            b"",
            ScanEdgesResponse {
                edges: bad,
                next_cursor: Vec::new(),
            },
            binding,
        ));
    }
    assert_protocol(validate_edges_page(
        &scan,
        b"",
        ScanEdgesResponse {
            edges: vec![],
            next_cursor: vec![1],
        },
        binding,
    ));
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_scans_page_stream_and_cursor_mismatch() -> TestResult {
    let mut server = GoServer::start(&[])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(&endpoint).connect().await?;
    client
        .put_vertices([
            VertexInput::string("rust:scan:v:a", "first"),
            VertexInput::string("rust:scan:v:b", "second"),
            VertexInput::string("rust:scan:v:c", "third"),
        ])
        .await?;

    let asc = ScanOptions {
        prefix: "rust:scan:v:".into(),
        limit: 1,
        order: ScanOrder::Asc,
    };
    let first = client.scan_vertices_page(asc.clone(), None).await?;
    assert_eq!(first.items[0].key, "rust:scan:v:a");
    assert_eq!(first.items[0].string_value(), Some("first"));
    let vertex_cursor = first.next_cursor.ok_or("missing first vertex cursor")?;
    let resumed = client
        .scan_vertices_page(
            asc.clone(),
            Some(VertexCursor::from_bytes(vertex_cursor.as_bytes().to_vec())?),
        )
        .await?;
    assert_eq!(resumed.items[0].key, "rust:scan:v:b");
    let unspecified = ScanOptions {
        order: ScanOrder::Unspecified,
        ..asc.clone()
    };
    assert_eq!(
        client
            .scan_vertices_page(unspecified.clone(), Some(vertex_cursor.clone()))
            .await?
            .items[0]
            .key,
        "rust:scan:v:b"
    );
    let default_vertex_cursor = client
        .scan_vertices_page(unspecified.clone(), None)
        .await?
        .next_cursor
        .ok_or("missing unspecified-order vertex cursor")?;
    assert_eq!(
        client
            .scan_vertices_page(asc.clone(), Some(default_vertex_cursor))
            .await?
            .items[0]
            .key,
        "rust:scan:v:b"
    );
    let remaining = client
        .scan_vertices_stream_from(asc.clone(), vertex_cursor.clone())
        .collect::<Vec<_>>()
        .await
        .into_iter()
        .collect::<Result<Vec<_>, _>>()?;
    assert_eq!(
        remaining.iter().map(|v| v.key.as_str()).collect::<Vec<_>>(),
        ["rust:scan:v:b", "rust:scan:v:c"]
    );

    let desc = ScanOptions {
        order: ScanOrder::Desc,
        ..asc.clone()
    };
    let high = client
        .scan_vertices_page_with_options(
            desc.clone(),
            None,
            CallOptions::After(Duration::from_secs(5)),
        )
        .await?;
    assert_eq!(high.items[0].key, "rust:scan:v:c");
    let desc_cursor = high.next_cursor.ok_or("missing descending cursor")?;
    let descending = client
        .scan_vertices_stream_from(desc.clone(), desc_cursor)
        .collect::<Vec<_>>()
        .await
        .into_iter()
        .collect::<Result<Vec<_>, _>>()?;
    assert_eq!(
        descending
            .iter()
            .map(|v| v.key.as_str())
            .collect::<Vec<_>>(),
        ["rust:scan:v:b", "rust:scan:v:a"]
    );
    assert_local_cursor_mismatch(
        client
            .scan_vertices_page(desc, Some(vertex_cursor.clone()))
            .await
            .expect_err("an ascending cursor must locally reject a descending scan"),
    );
    for changed in [
        ScanOptions {
            prefix: "rust:scan:other:".into(),
            ..asc.clone()
        },
        ScanOptions {
            limit: 2,
            ..asc.clone()
        },
    ] {
        assert_local_cursor_mismatch(
            client
                .scan_vertices_page(changed, Some(vertex_cursor.clone()))
                .await
                .expect_err("changed scan options must be rejected locally"),
        );
    }
    let mut mismatched_stream = client.scan_vertices_stream_from(
        ScanOptions {
            prefix: "rust:scan:other:".into(),
            ..asc.clone()
        },
        vertex_cursor.clone(),
    );
    assert_local_cursor_mismatch(
        mismatched_stream
            .next()
            .await
            .expect("stream must yield a binding error")
            .expect_err("changed stream prefix must fail"),
    );
    assert!(mismatched_stream.next().await.is_none());
    let mut other_server = GoServer::start(&[])?;
    other_server.wait_for_listener()?;
    let other_endpoint = format!("http://127.0.0.1:{}", other_server.port());
    let other_client = LanternClient::builder(&other_endpoint).connect().await?;
    assert_local_cursor_mismatch(
        other_client
            .scan_vertices_page(asc.clone(), Some(vertex_cursor.clone()))
            .await
            .expect_err("another endpoint must reject a bound cursor locally"),
    );

    let keys = client.scan_vertex_keys_page(asc.clone(), None).await?;
    assert_eq!(keys.items, ["rust:scan:v:a"]);
    let key_cursor = keys.next_cursor.ok_or("missing keys-only cursor")?;
    assert_eq!(
        client
            .scan_vertex_keys_page(unspecified.clone(), Some(key_cursor.clone()))
            .await?
            .items,
        ["rust:scan:v:b"]
    );
    let default_key_cursor = client
        .scan_vertex_keys_page(unspecified.clone(), None)
        .await?
        .next_cursor
        .ok_or("missing unspecified-order keys-only cursor")?;
    assert_eq!(
        client
            .scan_vertex_keys_page(asc.clone(), Some(default_key_cursor))
            .await?
            .items,
        ["rust:scan:v:b"]
    );
    assert_eq!(
        client
            .scan_vertex_keys_page(asc.clone(), Some(key_cursor.clone()))
            .await?
            .items,
        ["rust:scan:v:b"]
    );
    assert_eq!(
        client
            .scan_vertex_keys_page(
                asc.clone(),
                Some(KeyCursor::from_bytes(key_cursor.as_bytes().to_vec())?),
            )
            .await?
            .items,
        ["rust:scan:v:b"]
    );
    assert_eq!(
        client
            .scan_vertex_keys_stream_from(asc.clone(), key_cursor.clone())
            .collect::<Vec<_>>()
            .await
            .into_iter()
            .collect::<Result<Vec<_>, _>>()?,
        ["rust:scan:v:b", "rust:scan:v:c"]
    );
    assert!(matches!(
        KeyCursor::from_bytes(vertex_cursor.as_bytes().to_vec()),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        VertexCursor::from_bytes(key_cursor.as_bytes().to_vec()),
        Err(LanternError::InvalidInput(_))
    ));
    assert_local_cursor_mismatch(
        client
            .scan_vertex_keys_page(
                ScanOptions {
                    prefix: "rust:scan:other:".into(),
                    ..asc.clone()
                },
                Some(key_cursor.clone()),
            )
            .await
            .expect_err("keys-only cursor is prefix-bound"),
    );
    assert_local_cursor_mismatch(
        client
            .scan_vertex_keys_page(
                ScanOptions {
                    limit: 2,
                    ..asc.clone()
                },
                Some(key_cursor.clone()),
            )
            .await
            .expect_err("keys-only cursor is page-size-bound"),
    );
    assert_local_cursor_mismatch(
        client
            .scan_vertex_keys_page(
                ScanOptions {
                    order: ScanOrder::Desc,
                    ..asc.clone()
                },
                Some(key_cursor.clone()),
            )
            .await
            .expect_err("keys-only cursor is order-bound"),
    );
    assert_local_cursor_mismatch(
        other_client
            .scan_vertex_keys_page(asc.clone(), Some(key_cursor.clone()))
            .await
            .expect_err("keys-only cursor is endpoint-bound"),
    );
    assert!(matches!(
        VertexCursor::from_bytes(vec![0xff]),
        Err(LanternError::InvalidInput(_))
    ));
    let invalid_server_cursor = VertexCursor::issued(
        CursorBinding::vertices(client.endpoint_identity(), &asc),
        vec![0xff],
    );
    assert_server_invalid_cursor(
        client
            .scan_vertices_page(asc.clone(), Some(invalid_server_cursor))
            .await
            .expect_err("the Go server must reject malformed underlying cursor bytes"),
    );
    let wrong_rpc_server_cursor = VertexCursor::issued(
        CursorBinding::vertices(client.endpoint_identity(), &asc),
        key_cursor.server_cursor(CursorBinding::keys(client.endpoint_identity(), &asc))?,
    );
    assert_server_invalid_cursor(
        client
            .scan_vertices_page(asc.clone(), Some(wrong_rpc_server_cursor))
            .await
            .expect_err("the Go server must reject another RPC's underlying cursor"),
    );
    let key_order = client
        .scan_vertex_keys_stream(ScanOptions {
            order: ScanOrder::Desc,
            ..asc.clone()
        })
        .collect::<Vec<_>>()
        .await
        .into_iter()
        .collect::<Result<Vec<_>, _>>()?;
    assert_eq!(
        key_order,
        ["rust:scan:v:c", "rust:scan:v:b", "rust:scan:v:a"]
    );
    assert!(matches!(
        client
            .scan_vertex_keys_page(ScanOptions::default(), None)
            .await,
        Err(LanternError::InvalidInput(_))
    ));
    assert_eq!(
        client
            .scan_vertices_page(
                ScanOptions {
                    limit: 1,
                    ..ScanOptions::default()
                },
                None
            )
            .await?
            .items
            .len(),
        1,
        "full vertex scans allow an empty prefix"
    );

    client
        .put_edges([
            EdgeInput::new("rust:scan:tail:a", "rust:scan:head:a", 1.5)
                .with_expiration(Expiration::After(Duration::from_secs(60))),
            EdgeInput::new("rust:scan:tail:a", "rust:scan:head:b", 2.5),
            EdgeInput::new("rust:scan:tail:b", "rust:scan:head:a", 3.5),
            EdgeInput::new("rust:scan:other", "rust:scan:head:a", 4.5),
        ])
        .await?;
    let edge_scan = EdgeScanOptions {
        tail_prefix: "rust:scan:tail:".into(),
        head_prefix: "rust:scan:head:".into(),
        limit: 1,
    };
    let edge_first = client.scan_edges_page(edge_scan.clone(), None).await?;
    assert_eq!(edge_first.items[0].tail, "rust:scan:tail:a");
    assert_eq!(edge_first.items[0].head, "rust:scan:head:a");
    assert_eq!(edge_first.items[0].weight, 1.5);
    assert!(edge_first.items[0].expiration.is_some());
    let edge_cursor = edge_first.next_cursor.ok_or("missing edge cursor")?;
    let restored_edge = EdgeCursor::from_bytes(edge_cursor.as_bytes().to_vec())?;
    assert_eq!(
        client
            .scan_edges_page(edge_scan.clone(), Some(restored_edge))
            .await?
            .items[0]
            .head,
        "rust:scan:head:b"
    );
    let more_edges = client
        .scan_edges_stream_from(edge_scan.clone(), edge_cursor.clone())
        .collect::<Vec<_>>()
        .await
        .into_iter()
        .collect::<Result<Vec<_>, _>>()?;
    assert_eq!(
        more_edges
            .iter()
            .map(|edge| (edge.tail.as_str(), edge.head.as_str()))
            .collect::<Vec<_>>(),
        [
            ("rust:scan:tail:a", "rust:scan:head:b"),
            ("rust:scan:tail:b", "rust:scan:head:a")
        ]
    );
    for changed in [
        EdgeScanOptions {
            tail_prefix: "rust:scan:other".into(),
            ..edge_scan.clone()
        },
        EdgeScanOptions {
            head_prefix: "rust:scan:head:a".into(),
            ..edge_scan.clone()
        },
        EdgeScanOptions {
            limit: 2,
            ..edge_scan.clone()
        },
    ] {
        assert_local_cursor_mismatch(
            client
                .scan_edges_page(changed, Some(edge_cursor.clone()))
                .await
                .expect_err("changed edge scan options must be rejected locally"),
        );
    }
    assert_local_cursor_mismatch(
        other_client
            .scan_edges_page(edge_scan.clone(), Some(edge_cursor.clone()))
            .await
            .expect_err("edge cursor must be rejected by another endpoint"),
    );
    assert!(matches!(
        EdgeCursor::from_bytes(vertex_cursor.as_bytes().to_vec()),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        EdgeCursor::from_bytes(key_cursor.as_bytes().to_vec()),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        KeyCursor::from_bytes(edge_cursor.as_bytes().to_vec()),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        VertexCursor::from_bytes(edge_cursor.as_bytes().to_vec()),
        Err(LanternError::InvalidInput(_))
    ));
    assert_eq!(
        client
            .scan_edges_page(
                EdgeScanOptions {
                    limit: 1,
                    ..EdgeScanOptions::default()
                },
                None
            )
            .await?
            .items
            .len(),
        1,
        "edge scans allow both prefixes to be empty"
    );

    let bounded = LanternClient::builder(&endpoint)
        .message_limits(32, 1024 * 1024)?
        .connect()
        .await?;
    assert!(matches!(
        bounded
            .scan_vertices_page(
                ScanOptions {
                    prefix: "rust:scan:vertex:".repeat(12),
                    limit: 1,
                    order: ScanOrder::Asc
                },
                None
            )
            .await,
        Err(LanternError::MessageTooLarge { .. })
    ));
    Ok(())
}

#[derive(Default)]
struct CountingToken {
    calls: AtomicUsize,
}

impl TokenProvider for CountingToken {
    fn token(&self) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        Box::pin(async { Ok(crate::test_server::TEST_TOKEN.into()) })
    }
}

struct PendingToken {
    entered: tokio::sync::Notify,
    canceled: Arc<AtomicBool>,
}

impl TokenProvider for PendingToken {
    fn token(&self) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
        struct OnDrop(Arc<AtomicBool>);
        impl Drop for OnDrop {
            fn drop(&mut self) {
                self.0.store(true, Ordering::SeqCst);
            }
        }
        Box::pin(async {
            let _guard = OnDrop(self.canceled.clone());
            self.entered.notify_one();
            pending::<Result<String, TokenError>>().await
        })
    }
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_stream_is_lazy_page_bounded_and_cancels_on_drop() -> TestResult {
    let mut server = GoServer::start_authenticated(&[], false, false)?;
    server.wait_for_listener()?;
    let endpoint = server.endpoint(0)?;
    let token = Arc::new(CountingToken::default());
    let client = LanternClient::builder(endpoint)
        .token_provider(token.clone())
        .tls_private_ca_pem(server.ca_pem()?)?
        .connect()
        .await?;
    client
        .put_vertices([
            VertexInput::nil("rust:lazy:a"),
            VertexInput::nil("rust:lazy:b"),
            VertexInput::nil("rust:lazy:c"),
        ])
        .await?;
    let before = token.calls.load(Ordering::SeqCst);
    let scan = ScanOptions {
        prefix: "rust:lazy:".into(),
        limit: 2,
        order: ScanOrder::Asc,
    };
    let mut stream = client.scan_vertices_stream_with_options(
        scan.clone(),
        CallOptions::After(Duration::from_secs(5)),
    );
    assert_eq!(token.calls.load(Ordering::SeqCst), before);
    assert_eq!(
        stream.next().await.ok_or("missing first item")??.key,
        "rust:lazy:a"
    );
    assert_eq!(token.calls.load(Ordering::SeqCst), before + 1);
    assert_eq!(
        stream.next().await.ok_or("missing second item")??.key,
        "rust:lazy:b"
    );
    assert_eq!(token.calls.load(Ordering::SeqCst), before + 1);
    assert_eq!(
        stream.next().await.ok_or("missing third item")??.key,
        "rust:lazy:c"
    );
    assert_eq!(token.calls.load(Ordering::SeqCst), before + 2);
    assert!(stream.next().await.is_none());
    drop(stream);
    assert_eq!(token.calls.load(Ordering::SeqCst), before + 2);

    let mut partial = client.scan_vertices_stream(ScanOptions {
        limit: 1,
        ..scan.clone()
    });
    assert_eq!(
        partial.next().await.ok_or("missing partial item")??.key,
        "rust:lazy:a"
    );
    let after_first = token.calls.load(Ordering::SeqCst);
    drop(partial);
    tokio::task::yield_now().await;
    assert_eq!(token.calls.load(Ordering::SeqCst), after_first);

    let issued = client
        .scan_vertices_page(scan.clone(), None)
        .await?
        .next_cursor
        .ok_or("missing cursor for local-rejection test")?;
    let before_rejection = token.calls.load(Ordering::SeqCst);
    assert_local_cursor_mismatch(
        client
            .scan_vertices_page(
                ScanOptions {
                    limit: 1,
                    ..scan.clone()
                },
                Some(issued.clone()),
            )
            .await
            .expect_err("changed page size must not restart the scan"),
    );
    let mut wrong_stream = client.scan_vertices_stream_from(
        ScanOptions {
            prefix: "rust:unrelated:".into(),
            ..scan.clone()
        },
        issued,
    );
    assert_local_cursor_mismatch(
        wrong_stream
            .next()
            .await
            .expect("mismatched stream must yield an error")
            .expect_err("changed prefix must not restart the stream"),
    );
    assert!(wrong_stream.next().await.is_none());
    assert_eq!(
        token.calls.load(Ordering::SeqCst),
        before_rejection,
        "mismatched cursors must fail before fetching credentials or sending RPCs"
    );

    let pending = Arc::new(PendingToken {
        entered: tokio::sync::Notify::new(),
        canceled: Arc::new(AtomicBool::new(false)),
    });
    let stalled = LanternClient::builder(endpoint)
        .token_provider(pending.clone())
        .tls_private_ca_pem(server.ca_pem()?)?
        .connect()
        .await?;
    let task = tokio::spawn(async move {
        let mut stream = stalled.scan_vertex_keys_stream(ScanOptions {
            prefix: "rust:lazy:".into(),
            limit: 1,
            order: ScanOrder::Asc,
        });
        stream.next().await
    });
    tokio::time::timeout(Duration::from_secs(5), pending.entered.notified()).await?;
    task.abort();
    let _ = task.await;
    assert!(pending.canceled.load(Ordering::SeqCst));
    Ok(())
}
