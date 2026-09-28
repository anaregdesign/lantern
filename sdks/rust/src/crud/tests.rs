use std::{error::Error, future::Future, pin::Pin, sync::Arc, time::Duration};

use super::*;
use crate::{
    ContribId, Expiration, ProtoDuration, RpcErrorKind, Timestamp, TokenError, TokenProvider,
    VertexKind, VertexValue, test_server::GoServer,
};

type TestResult = Result<(), Box<dyn Error>>;

struct DelayedToken(Duration);

impl TokenProvider for DelayedToken {
    fn token(&self) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
        Box::pin(async move {
            tokio::time::sleep(self.0).await;
            Ok("development".into())
        })
    }
}

fn vertex(key: &str) -> Vertex {
    Vertex {
        key: key.into(),
        value: Some(VertexValue::Nil(true)),
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

#[test]
fn found_missing_are_unordered_identity_multisets() {
    let keys = vec!["a".into(), "a".into(), "b".into()];
    let valid = GetVerticesResponse {
        vertices: vec![vertex("a")],
        missing: vec!["b".into(), "a".into()],
    };
    validate_get_vertices(&keys, &valid).unwrap();
    let repeated = GetVerticesResponse {
        vertices: vec![vertex("a"), vertex("a"), vertex("a")],
        missing: Vec::new(),
    };
    assert!(matches!(
        validate_get_vertices(&keys, &repeated),
        Err(LanternError::Protocol(_))
    ));
    let omitted = GetVerticesResponse {
        vertices: vec![vertex("a")],
        missing: vec!["b".into()],
    };
    assert!(matches!(
        validate_get_vertices(&keys, &omitted),
        Err(LanternError::Protocol(_))
    ));
    let unexpected = GetVerticesResponse {
        vertices: vec![vertex("a"), vertex("other")],
        missing: vec!["b".into()],
    };
    assert!(matches!(
        validate_get_vertices(&keys, &unexpected),
        Err(LanternError::Protocol(_))
    ));
    let malformed = GetVerticesResponse {
        vertices: vec![Vertex {
            value: Some(VertexValue::Nil(false)),
            ..vertex("a")
        }],
        missing: vec!["a".into(), "b".into()],
    };
    assert!(matches!(
        validate_get_vertices(&keys, &malformed),
        Err(LanternError::Protocol(_))
    ));

    let pairs = vec![
        EdgeRef::new("t", "h"),
        EdgeRef::new("t", "h"),
        EdgeRef::new("t", "other"),
    ];
    let valid = GetEdgesResponse {
        edges: vec![edge("t", "h"), edge("t", "h")],
        missing: vec![EdgeKey {
            tail: "t".into(),
            head: "other".into(),
        }],
    };
    validate_get_edges(&pairs, &valid).unwrap();
    let wrong_pair = GetEdgesResponse {
        edges: vec![edge("t", "h"), edge("different", "h")],
        missing: valid.missing,
    };
    assert!(matches!(
        validate_get_edges(&pairs, &wrong_pair),
        Err(LanternError::Protocol(_))
    ));
}

#[test]
fn outcome_vectors_and_counts_fail_closed_without_erasing_server_results() {
    let past = Vertex {
        expiration: Some(Timestamp {
            seconds: 0,
            nanos: 500_000_000,
        }),
        ..vertex("v")
    };
    assert_eq!(
        validate_put_outcomes(
            std::slice::from_ref(&past),
            vec![PutOutcome::AppliedAndLive as i32]
        )
        .unwrap(),
        [PutOutcome::Expired],
    );
    for outcome in [PutOutcome::ConditionNotMet, PutOutcome::Superseded] {
        assert_eq!(
            validate_put_outcomes(std::slice::from_ref(&past), vec![outcome as i32]).unwrap(),
            [outcome],
        );
    }
    for bad in [0, 999_999] {
        assert!(matches!(
            validate_put_outcomes(std::slice::from_ref(&past), vec![bad]),
            Err(LanternError::Protocol(_))
        ));
    }
    assert!(matches!(
        validate_put_outcomes(std::slice::from_ref(&past), vec![]),
        Err(LanternError::Protocol(_))
    ));
    assert!(matches!(
        validate_edge_put_outcomes(
            &[edge("t", "h")],
            vec![PutOutcome::Expired as i32, PutOutcome::Expired as i32]
        ),
        Err(LanternError::Protocol(_))
    ));

    for (deleted, existed) in [(1, vec![]), (1, vec![false]), (-1, vec![true])] {
        assert!(matches!(
            validate_delete(1, deleted, existed),
            Err(LanternError::Protocol(_))
        ));
    }
    assert_eq!(
        validate_delete(3, 2, vec![true, false, true])
            .unwrap()
            .deleted,
        2,
    );
    for (written, weights) in [(2, vec![1.0]), (-1, vec![1.0]), (3, vec![1.0])] {
        assert!(matches!(
            validate_add(
                1,
                AddEdgesResponse {
                    written,
                    effective_weights: weights,
                },
            ),
            Err(LanternError::Protocol(_))
        ));
    }
    let result = validate_add(
        2,
        AddEdgesResponse {
            written: 1,
            effective_weights: vec![f32::NAN, f32::INFINITY],
        },
    )
    .unwrap();
    assert!(result.effective_weights[0].is_nan());
    assert!(result.effective_weights[1].is_infinite());

    let error = failed_chunk(2, LanternError::Protocol("missing outcome"));
    match error {
        LanternError::Batch(batch) => {
            assert_eq!(batch.completed_items, 2);
            assert!(matches!(*batch.source, LanternError::Protocol(_)));
        }
        other => panic!("expected partial batch error: {other}"),
    }
}

#[test]
fn contribution_delete_validates_ids_keys_and_indexed_results() {
    let id = ContribId::new([0x2a; 24]).unwrap();
    assert!(matches!(
        ContribId::new([0; 24]),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        ContribId::try_from(&[0x2a; 49][..]),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(
        EdgeContributionRef::new("tail", "head", id)
            .validate()
            .is_ok()
    );
    assert!(matches!(
        EdgeContributionRef::new("", "head", id).validate(),
        Err(LanternError::InvalidInput(_))
    ));
    assert_eq!(
        validate_delete_contributions(
            3,
            DeleteEdgeContributionsResponse {
                deleted: 1,
                existed: vec![true, false, false],
            }
        )
        .unwrap(),
        DeleteBatch {
            deleted: 1,
            existed: vec![true, false, false],
        }
    );
    for (deleted, existed) in [
        (0, vec![true]),
        (1, vec![true, false]),
        (2, vec![true, false, false]),
        (-1, vec![true, false, false]),
    ] {
        assert!(matches!(
            validate_delete_contributions(3, DeleteEdgeContributionsResponse { deleted, existed }),
            Err(LanternError::Protocol(_))
        ));
    }
}

#[test]
fn logical_bound_rejects_late_items_without_unbounded_collection() {
    assert!(matches!(
        collect_bounded(0..65_537),
        Err(LanternError::InvalidInput(_))
    ));
    assert_eq!(collect_bounded(0..65_536).unwrap().len(), 65_536);
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_delete_selected_contributions_preserves_put_base() -> TestResult {
    let mut server = GoServer::start(&[])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(&endpoint)
        .batch_chunk_size(2)?
        .retry(crate::RetryPolicy::Unavailable { max_attempts: 3 })?
        .connect()
        .await?;
    let tail = "rust:contrib:tail";
    let head = "rust:contrib:head";
    client
        .put_vertices([VertexInput::nil(tail), VertexInput::nil(head)])
        .await?;
    client.put_edge(EdgeInput::new(tail, head, 2.0)).await?;
    let first = ContribId::new([0x23; 24])?;
    let second = ContribId::new([0x42; 24])?;
    let prepared = client.prepare_add([
        AddInput::new(EdgeInput::new(tail, head, 1.0)).with_contrib_id(first),
        AddInput::new(EdgeInput::new(tail, head, 2.0)).with_contrib_id(second),
    ])?;
    assert_eq!(
        client
            .add_prepared_edges(&prepared)
            .await?
            .effective_weights,
        [3.0, 5.0]
    );

    let ref_first = EdgeContributionRef::new(tail, head, first);
    let result = client
        .delete_edge_contributions([
            ref_first.clone(),
            ref_first.clone(),
            EdgeContributionRef::new("missing", head, second),
            EdgeContributionRef::new(tail, head, second),
        ])
        .await?;
    assert_eq!(result.deleted, 2);
    assert_eq!(result.existed, [true, false, false, true]);
    assert_eq!(client.get_edge(tail, head).await?.weight, 2.0);
    assert!(!client.delete_edge_contribution(ref_first.clone()).await?);
    assert!(matches!(
        client
            .delete_edge_contributions([EdgeContributionRef::new("", head, first)])
            .await,
        Err(LanternError::InvalidInput(_))
    ));
    let repeated = client
        .add_edge(AddInput::new(EdgeInput::new(tail, head, 1.0)).with_contrib_id(first))
        .await?;
    assert_eq!(repeated, 2.0);
    assert!(!client.delete_edge_contribution(ref_first).await?);

    let fresh = ContribId::new([0x56; 24])?;
    let fresh_ref = EdgeContributionRef::new(tail, head, fresh);
    client
        .add_edge(AddInput::new(EdgeInput::new(tail, head, 1.0)).with_contrib_id(fresh))
        .await?;
    assert!(client.delete_edge_contribution(fresh_ref).await?);
    let expiring = ContribId::new([0x78; 24])?;
    let expiring_ref = EdgeContributionRef::new(tail, head, expiring);
    client
        .add_edge(
            AddInput::new(
                EdgeInput::new(tail, head, 1.0)
                    .with_expiration(Expiration::After(Duration::from_millis(80))),
            )
            .with_contrib_id(expiring),
        )
        .await?;
    tokio::time::sleep(Duration::from_millis(130)).await;
    assert!(!client.delete_edge_contribution(expiring_ref).await?);
    assert_eq!(client.get_edge(tail, head).await?.weight, 2.0);
    assert!(client.delete_edge(tail, head).await?);
    assert!(matches!(
        client.get_edge(tail, head).await,
        Err(LanternError::NotFound)
    ));
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_all_exact_values_and_expiration_boundaries() -> TestResult {
    let mut server = GoServer::start(&[("LANTERN_DEFAULT_TTL_SECONDS", "1")])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(&endpoint).connect().await?;
    let samples = vec![
        VertexInput::int32("rust:v:i32", i32::MIN),
        VertexInput::int64("rust:v:i64", i64::MIN),
        VertexInput::uint32("rust:v:u32", u32::MAX),
        VertexInput::uint64("rust:v:u64", u64::MAX),
        VertexInput::float32("rust:v:f32", 0.25),
        VertexInput::float64("rust:v:f64", -0.125),
        VertexInput::boolean("rust:v:bool", false),
        VertexInput::string("rust:v:string", "a🕯b"),
        VertexInput::bytes("rust:v:bytes", [0, 255, 42]),
        VertexInput::timestamp(
            "rust:v:time",
            Timestamp {
                seconds: -62_135_596_800,
                nanos: 0,
            },
        )?,
        VertexInput::timestamp(
            "rust:v:time-nanos",
            Timestamp {
                seconds: -1,
                nanos: 123_456_789,
            },
        )?,
        VertexInput::duration(
            "rust:v:duration",
            ProtoDuration {
                seconds: -1,
                nanos: -123_456_789,
            },
        )?,
        VertexInput::nil("rust:v:nil"),
        VertexInput::unset("rust:v:unset"),
    ];
    let keys: Vec<_> = samples.iter().map(|value| value.key.clone()).collect();
    let outcomes = client.put_vertices(samples.clone()).await?;
    assert_eq!(outcomes, vec![PutOutcome::AppliedAndLive; samples.len()]);
    let result = client.get_vertices(keys.clone()).await?;
    assert!(result.missing.is_empty());
    assert_eq!(result.found.len(), samples.len());
    let found: HashMap<_, _> = result
        .found
        .into_iter()
        .map(|v| (v.key.clone(), v))
        .collect();
    for sample in samples {
        let vertex = found.get(&sample.key).ok_or("value missing from Get")?;
        assert_eq!(vertex.value, sample.value, "key={}", sample.key);
        assert_eq!(vertex.expiration, None, "omitted TTL must stay permanent");
    }
    assert_eq!(found["rust:v:i32"].int32_value(), Some(i32::MIN));
    assert_eq!(found["rust:v:i64"].int64_value(), Some(i64::MIN));
    assert_eq!(found["rust:v:u32"].uint32_value(), Some(u32::MAX));
    assert_eq!(found["rust:v:u64"].uint64_value(), Some(u64::MAX));
    assert_eq!(found["rust:v:f32"].float32_value(), Some(0.25));
    assert_eq!(found["rust:v:f64"].float64_value(), Some(-0.125));
    assert_eq!(found["rust:v:bool"].bool_value(), Some(false));
    assert_eq!(found["rust:v:string"].string_value(), Some("a🕯b"));
    assert_eq!(found["rust:v:bytes"].bytes_value(), Some(&[0, 255, 42][..]));
    assert_eq!(
        found["rust:v:time"].timestamp_value().unwrap().seconds,
        -62_135_596_800
    );
    assert_eq!(
        found["rust:v:time-nanos"].timestamp_value().unwrap(),
        &Timestamp {
            seconds: -1,
            nanos: 123_456_789
        }
    );
    assert_eq!(
        found["rust:v:duration"].duration_value().unwrap().nanos,
        -123_456_789
    );
    assert!(found["rust:v:nil"].is_nil());
    assert_eq!(found["rust:v:nil"].kind(), VertexKind::Nil);
    assert!(found["rust:v:unset"].is_unset());
    assert_eq!(found["rust:v:unset"].kind(), VertexKind::Unset);

    tokio::time::sleep(Duration::from_millis(1_100)).await;
    assert!(client.get_vertex("rust:v:nil").await?.is_nil());

    for (seconds, nanos) in [(-1, 0), (0, 0), (0, 500_000_000)] {
        client.put_vertex(VertexInput::nil("rust:v:past")).await?;
        let expired = client
            .put_vertex(
                VertexInput::string("rust:v:past", "overwritten")
                    .with_expiration(Expiration::At(Timestamp { seconds, nanos })),
            )
            .await?;
        assert_eq!(expired, PutOutcome::Expired);
        assert!(matches!(
            client.get_vertex("rust:v:past").await,
            Err(LanternError::NotFound)
        ));
    }
    client.put_vertex(VertexInput::nil("rust:v:past")).await?;
    assert_eq!(
        client
            .put_vertex(
                VertexInput::nil("rust:v:past").with_expiration(Expiration::At(Timestamp {
                    seconds: -62_135_596_800,
                    nanos: 1,
                }))
            )
            .await?,
        PutOutcome::Expired
    );
    assert!(matches!(
        client.get_vertex("rust:v:past").await,
        Err(LanternError::NotFound)
    ));
    client.put_vertex(VertexInput::nil("rust:v:past")).await?;
    let sentinel = client
        .put_vertex(
            VertexInput::string("rust:v:past", "rejected").with_expiration(Expiration::At(
                Timestamp {
                    seconds: -62_135_596_800,
                    nanos: 0,
                },
            )),
        )
        .await;
    assert!(matches!(sentinel, Err(LanternError::InvalidInput(_))));
    assert!(client.get_vertex("rust:v:past").await?.is_nil());

    let short = VertexInput::nil("rust:v:short")
        .with_expiration(Expiration::After(Duration::from_millis(80)));
    assert_eq!(client.put_vertex(short).await?, PutOutcome::AppliedAndLive);
    assert!(
        client
            .get_vertex("rust:v:short")
            .await?
            .expiration
            .is_some()
    );
    tokio::time::sleep(Duration::from_millis(130)).await;
    assert!(matches!(
        client.get_vertex("rust:v:short").await,
        Err(LanternError::NotFound)
    ));

    let chunked = LanternClient::builder(&endpoint)
        .batch_chunk_size(1)?
        .connect()
        .await?;
    let same_ttl = (0..3).map(|i| {
        VertexInput::nil(format!("rust:v:chunk:{i}"))
            .with_expiration(Expiration::After(Duration::from_secs(60)))
    });
    assert_eq!(
        chunked.put_vertices(same_ttl).await?,
        [PutOutcome::AppliedAndLive; 3]
    );
    let read = chunked
        .get_vertices((0..3).map(|i| format!("rust:v:chunk:{i}")))
        .await?;
    assert_eq!(read.found.len(), 3);
    assert!(
        read.found
            .iter()
            .all(|v| v.expiration == read.found[0].expiration)
    );
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_plural_batches_duplicates_and_partial_failure() -> TestResult {
    let mut server = GoServer::start(&[
        ("LANTERN_MAX_BATCH_SIZE", "2"),
        ("LANTERN_MAX_KEY_LEN", "32"),
    ])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(&endpoint)
        .batch_chunk_size(5)?
        .server_max_batch_size(2)?
        .connect()
        .await?;
    assert!(
        client
            .get_vertices(Vec::<String>::new())
            .await?
            .found
            .is_empty()
    );
    assert!(
        client
            .get_edges(Vec::<EdgeRef>::new())
            .await?
            .missing
            .is_empty()
    );
    assert!(
        client
            .put_vertices(Vec::<VertexInput>::new())
            .await?
            .is_empty()
    );
    assert!(client.put_edges(Vec::<EdgeInput>::new()).await?.is_empty());
    assert!(
        client
            .delete_vertices(Vec::<String>::new())
            .await?
            .existed
            .is_empty()
    );
    assert!(
        client
            .delete_edges(Vec::<EdgeRef>::new())
            .await?
            .existed
            .is_empty()
    );
    assert!(
        client
            .add_edges(Vec::<EdgeInput>::new())
            .await?
            .effective_weights
            .is_empty()
    );
    assert!(matches!(
        client.get_vertex("rust:missing").await,
        Err(LanternError::NotFound)
    ));
    assert_eq!(
        client
            .put_vertices_if_absent([
                VertexInput::string("rust:dup", "first"),
                VertexInput::string("rust:dup", "second")
            ])
            .await?,
        [PutOutcome::AppliedAndLive, PutOutcome::ConditionNotMet],
    );
    assert_eq!(
        client.get_vertex("rust:dup").await?.string_value(),
        Some("first")
    );
    let read = client
        .get_vertices(["rust:dup", "rust:dup", "rust:absent", "rust:absent"])
        .await?;
    assert_eq!(read.found.len(), 2);
    assert_eq!(read.missing, ["rust:absent", "rust:absent"]);
    assert!(read.found.iter().all(|v| v.key == "rust:dup"));
    assert_eq!(
        client.delete_vertices(["rust:dup", "rust:dup"]).await?,
        DeleteBatch {
            deleted: 1,
            existed: vec![true, false]
        }
    );
    assert!(!client.delete_vertex("rust:dup").await?);

    assert_eq!(
        client
            .put_vertices([
                VertexInput::nil("rust:bulk:a"),
                VertexInput::nil("rust:bulk:b"),
                VertexInput::nil("rust:bulk:c"),
            ])
            .await?,
        [PutOutcome::AppliedAndLive; 3]
    );

    let failure = client
        .put_vertices([
            VertexInput::nil("rust:partial:a"),
            VertexInput::nil("rust:partial:b"),
            VertexInput::nil("rust:partial:key-that-exceeds-the-server-limit"),
        ])
        .await
        .expect_err("second chunk must fail server key validation");
    match failure {
        LanternError::Batch(batch) => {
            assert_eq!(batch.completed_items, 2);
            assert!(matches!(*batch.source, LanternError::Rpc(ref failure)
                if failure.kind() == RpcErrorKind::InvalidArgument));
        }
        other => panic!("expected a partial mutation error: {other}"),
    }
    assert!(client.get_vertex("rust:partial:a").await?.is_nil());
    assert!(client.get_vertex("rust:partial:b").await?.is_nil());
    let read_failure = client
        .get_vertices([
            "rust:partial:a",
            "rust:partial:b",
            "rust:partial:key-that-exceeds-the-server-limit",
        ])
        .await;
    assert!(matches!(
        read_failure,
        Err(LanternError::Rpc(ref failure))
            if failure.kind() == RpcErrorKind::InvalidArgument
    ));
    let delete_failure = client
        .delete_vertices([
            "rust:partial:a",
            "rust:partial:b",
            "rust:partial:key-that-exceeds-the-server-limit",
        ])
        .await;
    assert!(matches!(
        delete_failure,
        Err(LanternError::Batch(ref batch))
            if batch.completed_items == 2 && matches!(*batch.source, LanternError::Rpc(_))
    ));
    assert!(matches!(
        client.get_vertex("rust:partial:a").await,
        Err(LanternError::NotFound)
    ));

    let bounded = LanternClient::builder(&endpoint)
        .message_limits(50, 1024 * 1024)?
        .connect()
        .await?;
    assert!(matches!(
        bounded
            .put_vertices([
                VertexInput::nil("rust:preflight"),
                VertexInput::string("rust:oversize", "x".repeat(80))
            ])
            .await,
        Err(LanternError::MessageTooLarge { .. })
    ));
    assert!(matches!(
        client.get_vertex("rust:preflight").await,
        Err(LanternError::NotFound)
    ));
    assert!(matches!(
        client
            .put_vertices([
                VertexInput::nil("rust:invalid:first"),
                VertexInput::new("rust:invalid:second", Some(VertexValue::Nil(false)))
            ])
            .await,
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        client.get_vertex("rust:invalid:first").await,
        Err(LanternError::NotFound)
    ));
    client
        .put_vertices([
            VertexInput::nil("rust:tail"),
            VertexInput::nil("rust:head"),
            VertexInput::nil("rust:head2"),
        ])
        .await?;
    assert_eq!(
        client
            .put_edges([
                EdgeInput::new("rust:tail", "rust:head", 1.0),
                EdgeInput::new("rust:tail", "rust:head", 2.5)
                    .with_expiration(Expiration::After(Duration::from_secs(60)))
            ])
            .await?,
        [PutOutcome::AppliedAndLive; 2]
    );
    let saved_edge = client.get_edge("rust:tail", "rust:head").await?;
    assert_eq!(saved_edge.weight, 2.5);
    assert!(saved_edge.expiration.is_some());
    let edges = client
        .get_edges([
            EdgeRef::new("rust:tail", "rust:head"),
            EdgeRef::new("rust:tail", "rust:head"),
            EdgeRef::new("rust:tail", "rust:head2"),
        ])
        .await?;
    assert_eq!(edges.found.len(), 2);
    assert_eq!(edges.missing, [EdgeRef::new("rust:tail", "rust:head2")]);
    assert_eq!(
        client
            .add_edge(EdgeInput::new("rust:tail", "rust:head", 1.0))
            .await?,
        3.5
    );
    let expiring_add = client.prepare_add([EdgeInput::new("rust:tail", "rust:head2", 1.0)
        .with_expiration(Expiration::After(Duration::from_secs(60)))])?;
    assert!(expiring_add.edges()[0].expiration.is_some());
    assert_eq!(
        client
            .add_prepared_edges(&expiring_add)
            .await?
            .effective_weights,
        [1.0]
    );
    assert_eq!(
        client.get_edge("rust:tail", "rust:head2").await?.expiration,
        expiring_add.edges()[0].expiration,
    );
    client
        .put_edge(EdgeInput::new("rust:tail", "rust:head2", 4.0))
        .await?;
    let permanent_edge = client.get_edge("rust:tail", "rust:head2").await?;
    assert_eq!(permanent_edge.weight, 4.0);
    assert_eq!(permanent_edge.expiration, None);
    assert_eq!(
        client
            .delete_edges([
                EdgeRef::new("rust:tail", "rust:head"),
                EdgeRef::new("rust:tail", "rust:head")
            ])
            .await?,
        DeleteBatch {
            deleted: 1,
            existed: vec![true, false]
        }
    );
    assert!(matches!(
        client.get_edge("rust:tail", "rust:head").await,
        Err(LanternError::NotFound)
    ));
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_prepared_add_replays_only_inside_live_dedup_horizon() -> TestResult {
    let mut server = GoServer::start(&[])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(&endpoint)
        .batch_chunk_size(1)?
        .auto_contribution_ids(true)
        .connect()
        .await?;
    client
        .put_vertices([
            VertexInput::nil("rust:add:t"),
            VertexInput::nil("rust:add:h"),
            VertexInput::nil("rust:add:h2"),
        ])
        .await?;
    let prepared = client.prepare_add([
        EdgeInput::new("rust:add:t", "rust:add:h", 1.0),
        EdgeInput::new("rust:add:t", "rust:add:h2", 1.0),
    ])?;
    assert_eq!(prepared.len(), 2);
    let ids = prepared.contrib_ids();
    assert!(ids.iter().all(Option::is_some));
    assert_eq!(&ids[0].unwrap().as_bytes()[16..], &[0, 0, 0, 0, 0, 1, 0, 0]);
    assert_eq!(&ids[1].unwrap().as_bytes()[16..], &[0, 0, 0, 0, 0, 1, 0, 1]);
    let next = client
        .clone()
        .prepare_add([EdgeInput::new("rust:add:t", "rust:add:h2", 1.0)])?;
    assert_eq!(
        &next.contrib_ids()[0].unwrap().as_bytes()[16..],
        &[0, 0, 0, 0, 0, 2, 0, 0]
    );
    assert_eq!(
        client
            .add_prepared_edges(&prepared)
            .await?
            .effective_weights,
        [1.0, 1.0]
    );
    assert_eq!(
        client
            .add_prepared_edges(&prepared)
            .await?
            .effective_weights,
        [1.0, 1.0],
        "a retained live ID must not count twice",
    );
    assert!(client.delete_edge("rust:add:t", "rust:add:h").await?);
    assert_eq!(
        client
            .add_prepared_edges(&prepared)
            .await?
            .effective_weights,
        [1.0, 1.0],
        "after Delete the same receipt-less ID is a new contribution",
    );
    assert_eq!(
        client.get_edge("rust:add:t", "rust:add:h").await?.weight,
        1.0
    );

    let manual_id = ContribId::new([4; 24])?;
    let manual =
        client.prepare_add([
            AddInput::new(EdgeInput::new("rust:add:t", "rust:add:h2", 2.0))
                .with_contrib_id(manual_id),
        ])?;
    assert_eq!(manual.contrib_ids(), &[Some(manual_id)]);
    assert_eq!(
        client.add_prepared_edges(&manual).await?.effective_weights,
        [3.0]
    );
    assert_eq!(
        client.add_prepared_edges(&manual).await?.effective_weights,
        [3.0]
    );
    assert!(matches!(
        client.prepare_add([
            AddInput::new(EdgeInput::new("rust:add:t", "rust:add:h", 1.0))
                .with_contrib_id(manual_id),
            AddInput::new(EdgeInput::new("rust:add:t", "rust:add:h2", 1.0))
                .with_contrib_id(manual_id),
        ]),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        client.prepare_add([EdgeInput::new("rust:add:t", "rust:add:h", f32::NAN)]),
        Err(LanternError::InvalidInput(_))
    ));
    let overflow = client
        .add_edges([
            EdgeInput::new("rust:add:t", "rust:add:h", f32::MAX),
            EdgeInput::new("rust:add:t", "rust:add:h", f32::MAX),
        ])
        .await?;
    assert_eq!(overflow.effective_weights[0], f32::MAX);
    assert!(overflow.effective_weights[1].is_infinite());

    let expiring_id = ContribId::new([6; 24])?;
    let expiring = AddInput::new(
        EdgeInput::new("rust:add:t", "rust:add:h2", 5.0)
            .with_expiration(Expiration::After(Duration::from_millis(60))),
    )
    .with_contrib_id(expiring_id);
    assert_eq!(client.add_edge(expiring).await?, 8.0);
    tokio::time::sleep(Duration::from_millis(120)).await;
    let fresh = AddInput::new(EdgeInput::new("rust:add:t", "rust:add:h2", 1.0))
        .with_contrib_id(expiring_id);
    assert_eq!(
        client.add_edge(fresh).await?,
        4.0,
        "an expired contribution no longer deduplicates its old ID"
    );
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_one_deadline_covers_all_authenticated_chunks() -> TestResult {
    let mut server = GoServer::start(&[("LANTERN_AUTH_TOKENS", "development")])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let delayed = LanternClient::builder(&endpoint)
        .token_provider(Arc::new(DelayedToken(Duration::from_millis(200))))
        .allow_credentialed_h2c_for_single_instance_development(true)
        .batch_chunk_size(1)?
        .connect()
        .await?;
    let observer = LanternClient::builder(&endpoint)
        .token_provider(Arc::new(DelayedToken(Duration::ZERO)))
        .allow_credentialed_h2c_for_single_instance_development(true)
        .connect()
        .await?;
    let result = delayed
        .put_vertices_with_options(
            [
                VertexInput::nil("rust:deadline:first"),
                VertexInput::nil("rust:deadline:second"),
            ],
            CallOptions::After(Duration::from_millis(350)),
        )
        .await;
    assert!(matches!(
        result,
        Err(LanternError::Batch(ref batch))
            if batch.completed_items == 1
                && matches!(*batch.source, LanternError::DeadlineExceeded)
    ));
    assert!(observer.get_vertex("rust:deadline:first").await?.is_nil());
    assert!(matches!(
        observer.get_vertex("rust:deadline:second").await,
        Err(LanternError::NotFound)
    ));
    Ok(())
}
