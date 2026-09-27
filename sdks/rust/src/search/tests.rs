use std::{collections::HashMap, error::Error, time::Duration};

use futures_util::StreamExt;
use prost::Message;
use tonic::{Code, Status, codegen::Bytes};
use tonic_types::pb::Status as RichStatus;

use super::*;
use crate::{
    Expiration, SearchDetails, SearchErrorReason, Timestamp, VertexInput, VertexValue,
    generated::graph::v1::SearchErrorDetail, test_server::GoServer,
};

type TestResult = Result<(), Box<dyn Error>>;

fn hit(key: &str, status: SearchHitProjectionStatus, vertex: Option<Vertex>) -> WireSearchHit {
    WireSearchHit {
        key: key.into(),
        score: 1.25,
        vertex,
        projection_status: status as i32,
    }
}

fn response(hits: Vec<WireSearchHit>) -> SearchVerticesResponse {
    SearchVerticesResponse {
        effective_limit: hits.len().max(1) as u32,
        hits,
        next_cursor: Vec::new(),
        truncated: false,
        continuation_limited: false,
    }
}

fn snapshot() -> Vertex {
    Vertex {
        key: "search:one".into(),
        value: Some(VertexValue::String("unchanged value".into())),
        expiration: Some(Timestamp {
            seconds: 2_000_000_000,
            nanos: 123_456_789,
        }),
    }
}

#[test]
fn options_are_validated_locally_and_defaults_keep_search_lightweight() {
    let request = SearchRequest::new("");
    assert_eq!(request.projection, SearchProjection::KeyScore);
    assert_eq!(request.match_mode, MatchMode::Unspecified);
    assert_eq!(request.limit, 0);
    request.validate().unwrap();
    let wire = request.to_wire(SearchCursor::default());
    assert!(wire.options.is_none());
    assert_eq!(wire.projection, SearchProjection::KeyScore as i32);

    let mut phrase = SearchRequest::new("two words");
    phrase.phrase = true;
    phrase.validate().unwrap();
    assert!(
        phrase
            .to_wire(SearchCursor::default())
            .options
            .unwrap()
            .phrase
    );
    for conflict in 0..4 {
        let mut invalid = phrase.clone();
        match conflict {
            0 => invalid.match_mode = MatchMode::Any,
            1 => invalid.min_should_match = 2,
            2 => invalid.fuzziness = 1,
            _ => invalid.prefix_terms = true,
        }
        assert!(matches!(
            invalid.validate(),
            Err(LanternError::InvalidInput(_))
        ));
    }
    let mut invalid = request.clone();
    invalid.fuzziness = 3;
    assert!(matches!(
        invalid.validate(),
        Err(LanternError::InvalidInput(_))
    ));
    invalid.fuzziness = 2;
    invalid.min_should_match = 1;
    assert!(matches!(
        invalid.validate(),
        Err(LanternError::InvalidInput(_))
    ));
    invalid.match_mode = MatchMode::MinShould;
    invalid.validate().unwrap();
    let wire = invalid.to_wire(SearchCursor::from_bytes([0, 255, 4]));
    assert_eq!(wire.cursor, [0, 255, 4]);
    assert_eq!(wire.options.unwrap().min_should_match, 1);
}

#[test]
fn cursor_bytes_are_opaque_and_round_trip_without_loss() {
    let cursor = SearchCursor::from_bytes(b"do-not-log-this".to_vec());
    assert_eq!(cursor.clone(), cursor);
    assert_eq!(cursor.as_bytes(), b"do-not-log-this");
    assert_eq!(cursor.as_ref(), b"do-not-log-this");
    assert!(!cursor.is_empty());
    assert!(!format!("{cursor:?}").contains("do-not-log-this"));
    assert_eq!(cursor.into_bytes(), b"do-not-log-this");
    assert!(SearchCursor::default().is_empty());
}

#[test]
fn projection_fixtures_preserve_exact_snapshot_or_explicit_absence() {
    let full = SearchPage::from_wire(
        response(vec![hit(
            "search:one",
            SearchHitProjectionStatus::Snapshot,
            Some(snapshot()),
        )]),
        SearchProjection::FullVertex,
    )
    .unwrap();
    assert_eq!(full.hits[0].vertex, Some(snapshot()));
    assert_eq!(
        full.hits[0].vertex.as_ref().unwrap().expiration,
        snapshot().expiration
    );
    for status in [
        SearchHitProjectionStatus::Missing,
        SearchHitProjectionStatus::Replaced,
    ] {
        let page = SearchPage::from_wire(
            response(vec![hit("search:one", status, None)]),
            SearchProjection::FullVertex,
        )
        .unwrap();
        assert_eq!(page.hits[0].projection_status, status);
        assert_eq!(page.hits[0].vertex, None);
    }
    for projection in [SearchProjection::Unspecified, SearchProjection::KeyScore] {
        let page = SearchPage::from_wire(
            response(vec![hit(
                "search:one",
                SearchHitProjectionStatus::KeyScore,
                None,
            )]),
            projection,
        )
        .unwrap();
        assert_eq!(page.hits[0].vertex, None);
    }
}

#[test]
fn malformed_projection_fixtures_fail_closed_without_fabricated_values() {
    let valid = snapshot();
    let cases = [
        (
            SearchProjection::FullVertex,
            SearchHitProjectionStatus::Snapshot,
            None,
        ),
        (
            SearchProjection::FullVertex,
            SearchHitProjectionStatus::Snapshot,
            Some(Vertex {
                key: "unrelated".into(),
                ..valid.clone()
            }),
        ),
        (
            SearchProjection::FullVertex,
            SearchHitProjectionStatus::Snapshot,
            Some(Vertex {
                value: Some(VertexValue::Nil(false)),
                ..valid.clone()
            }),
        ),
        (
            SearchProjection::FullVertex,
            SearchHitProjectionStatus::Missing,
            Some(valid.clone()),
        ),
        (
            SearchProjection::FullVertex,
            SearchHitProjectionStatus::Replaced,
            Some(valid.clone()),
        ),
        (
            SearchProjection::KeyScore,
            SearchHitProjectionStatus::KeyScore,
            Some(valid.clone()),
        ),
        (
            SearchProjection::KeyScore,
            SearchHitProjectionStatus::Snapshot,
            Some(valid.clone()),
        ),
        (
            SearchProjection::FullVertex,
            SearchHitProjectionStatus::KeyScore,
            None,
        ),
        (
            SearchProjection::FullVertex,
            SearchHitProjectionStatus::Unspecified,
            None,
        ),
    ];
    for (projection, status, vertex) in cases {
        assert!(matches!(
            SearchPage::from_wire(
                response(vec![hit("search:one", status, vertex)]),
                projection
            ),
            Err(LanternError::Protocol(_))
        ));
    }
    for unknown in [0, 999] {
        assert!(matches!(
            SearchPage::from_wire(
                response(vec![WireSearchHit {
                    projection_status: unknown,
                    ..hit("search:one", SearchHitProjectionStatus::KeyScore, None)
                }]),
                SearchProjection::KeyScore
            ),
            Err(LanternError::Protocol(_))
        ));
    }
    for malformed in [
        WireSearchHit {
            key: String::new(),
            ..hit("search:one", SearchHitProjectionStatus::KeyScore, None)
        },
        WireSearchHit {
            score: f64::NAN,
            ..hit("search:one", SearchHitProjectionStatus::KeyScore, None)
        },
    ] {
        assert!(matches!(
            SearchPage::from_wire(response(vec![malformed]), SearchProjection::KeyScore),
            Err(LanternError::Protocol(_))
        ));
    }
    let mixed = response(vec![
        hit("search:one", SearchHitProjectionStatus::KeyScore, None),
        hit(
            "search:two",
            SearchHitProjectionStatus::Snapshot,
            Some(valid),
        ),
    ]);
    assert!(matches!(
        SearchPage::from_wire(mixed, SearchProjection::KeyScore),
        Err(LanternError::Protocol(_))
    ));
}

#[test]
fn page_limits_and_flags_are_preserved_and_inconsistent_envelopes_rejected() {
    let mut page = response(vec![hit(
        "search:one",
        SearchHitProjectionStatus::KeyScore,
        None,
    )]);
    page.effective_limit = 2;
    page.next_cursor = vec![0, 255];
    page.truncated = true;
    let read = SearchPage::from_wire(page.clone(), SearchProjection::KeyScore).unwrap();
    assert_eq!(read.effective_limit, 2);
    assert!(read.truncated);
    assert!(!read.continuation_limited);
    assert_eq!(read.next_cursor.unwrap().as_bytes(), &[0, 255]);

    page.effective_limit = 0;
    assert!(matches!(
        SearchPage::from_wire(page.clone(), SearchProjection::KeyScore),
        Err(LanternError::Protocol(_))
    ));
    page.effective_limit = 2;
    page.truncated = false;
    assert!(matches!(
        SearchPage::from_wire(page.clone(), SearchProjection::KeyScore),
        Err(LanternError::Protocol(_))
    ));
    page.truncated = true;
    page.hits.clear();
    assert!(matches!(
        SearchPage::from_wire(page.clone(), SearchProjection::KeyScore),
        Err(LanternError::Protocol(_))
    ));
    page.next_cursor.clear();
    page.continuation_limited = true;
    page.truncated = false;
    assert!(matches!(
        SearchPage::from_wire(page, SearchProjection::KeyScore),
        Err(LanternError::Protocol(_))
    ));
    let mut excess = response(vec![
        hit("search:one", SearchHitProjectionStatus::KeyScore, None),
        hit("search:two", SearchHitProjectionStatus::KeyScore, None),
    ]);
    excess.effective_limit = 1;
    assert!(matches!(
        SearchPage::from_wire(excess, SearchProjection::KeyScore),
        Err(LanternError::Protocol(_))
    ));
}

#[tokio::test]
async fn bounded_tail_yields_retained_hits_then_typed_error_even_without_cursor() {
    let mut stream = page_stream(SearchCursor::default(), |cursor| async move {
        let (key, next_cursor, limited) = if cursor.is_empty() {
            ("search:first", vec![1, 255], true)
        } else {
            ("search:last", Vec::new(), false)
        };
        let mut wire = response(vec![hit(key, SearchHitProjectionStatus::KeyScore, None)]);
        wire.next_cursor = next_cursor;
        wire.truncated = limited;
        wire.continuation_limited = limited;
        Ok(SearchPage::from_wire(wire, SearchProjection::KeyScore)?.into_stream_items())
    });
    assert_eq!(stream.next().await.unwrap().unwrap().key, "search:first");
    assert_eq!(stream.next().await.unwrap().unwrap().key, "search:last");
    assert!(matches!(
        stream.next().await,
        Some(Err(LanternError::SearchContinuationLimited))
    ));
    assert!(stream.next().await.is_none());

    let mut stream = page_stream(SearchCursor::default(), |_| async {
        let mut wire = response(vec![hit(
            "search:only",
            SearchHitProjectionStatus::KeyScore,
            None,
        )]);
        wire.truncated = true;
        wire.continuation_limited = true;
        Ok(SearchPage::from_wire(wire, SearchProjection::KeyScore)?.into_stream_items())
    });
    assert_eq!(stream.next().await.unwrap().unwrap().key, "search:only");
    assert!(matches!(
        stream.next().await,
        Some(Err(LanternError::SearchContinuationLimited))
    ));
    assert!(stream.next().await.is_none());

    let mut stream = page_stream(SearchCursor::default(), |_| async {
        let mut wire = response(vec![hit(
            "search:unknown-tail",
            SearchHitProjectionStatus::KeyScore,
            None,
        )]);
        wire.truncated = true;
        Ok(SearchPage::from_wire(wire, SearchProjection::KeyScore)?.into_stream_items())
    });
    assert_eq!(
        stream.next().await.unwrap().unwrap().key,
        "search:unknown-tail"
    );
    assert!(matches!(
        stream.next().await,
        Some(Err(LanternError::Protocol(_)))
    ));
    assert!(stream.next().await.is_none());

    let mut stream = page_stream(SearchCursor::default(), |cursor| async move {
        if cursor.is_empty() {
            let mut wire = response(vec![hit(
                "search:before-bad-cursor",
                SearchHitProjectionStatus::KeyScore,
                None,
            )]);
            wire.next_cursor = vec![255];
            wire.truncated = true;
            wire.continuation_limited = true;
            Ok(SearchPage::from_wire(wire, SearchProjection::KeyScore)?.into_stream_items())
        } else {
            let code = Code::InvalidArgument;
            let rich = RichStatus {
                code: code as i32,
                message: "invalid continuation".into(),
                details: vec![prost_types::Any {
                    type_url: "type.googleapis.com/graph.v1.SearchErrorDetail".into(),
                    value: SearchErrorDetail {
                        reason: SearchErrorReason::SearchCursorInvalid as i32,
                        work_kind: String::new(),
                    }
                    .encode_to_vec(),
                }],
            };
            Err(LanternError::from(Status::with_details(
                code,
                "invalid continuation",
                Bytes::from(rich.encode_to_vec()),
            )))
        }
    });
    assert_eq!(
        stream.next().await.unwrap().unwrap().key,
        "search:before-bad-cursor"
    );
    assert_eq!(
        stream.next().await.unwrap().unwrap_err().search_reason(),
        Some((SearchErrorReason::SearchCursorInvalid, ""))
    );
    assert!(stream.next().await.is_none());
}

#[test]
fn typed_rich_errors_preserve_reason_work_kind_and_original_status() {
    let reasons = [
        (
            Code::FailedPrecondition,
            SearchErrorReason::SearchDisabled,
            "",
        ),
        (
            Code::FailedPrecondition,
            SearchErrorReason::SearchPositionsDisabled,
            "",
        ),
        (
            Code::FailedPrecondition,
            SearchErrorReason::SearchIndexIncomplete,
            "",
        ),
        (
            Code::ResourceExhausted,
            SearchErrorReason::SearchIndexBudgetExhausted,
            "live_postings",
        ),
        (
            Code::ResourceExhausted,
            SearchErrorReason::SearchWorkBudgetExhausted,
            "posting_visits",
        ),
        (
            Code::ResourceExhausted,
            SearchErrorReason::SearchAdmissionSaturated,
            "",
        ),
        (
            Code::InvalidArgument,
            SearchErrorReason::SearchCursorInvalid,
            "",
        ),
        (Code::Aborted, SearchErrorReason::SearchCursorStale, ""),
    ];
    for (code, reason, work_kind) in reasons {
        let details = RichStatus {
            code: code as i32,
            message: "unstructured text".into(),
            details: vec![prost_types::Any {
                type_url: "type.googleapis.com/graph.v1.SearchErrorDetail".into(),
                value: SearchErrorDetail {
                    reason: reason as i32,
                    work_kind: work_kind.into(),
                }
                .encode_to_vec(),
            }],
        };
        let original = Status::with_details(
            code,
            "different unstructured text",
            Bytes::from(details.encode_to_vec()),
        );
        let error = LanternError::from(original.clone());
        assert_eq!(error.search_reason(), Some((reason, work_kind)));
        match error {
            LanternError::Rpc(failure) => {
                assert_eq!(failure.status().code(), code);
                assert_eq!(failure.status().message(), original.message());
                assert_eq!(failure.status().details(), original.details());
            }
            other => panic!("expected original RPC status, got {other}"),
        }
    }
    let status = Status::with_details(
        Code::InvalidArgument,
        "unknown typed reason",
        Bytes::from(
            RichStatus {
                code: Code::InvalidArgument as i32,
                message: String::new(),
                details: vec![prost_types::Any {
                    type_url: "type.googleapis.com/graph.v1.SearchErrorDetail".into(),
                    value: SearchErrorDetail {
                        reason: 9_999,
                        work_kind: "future".into(),
                    }
                    .encode_to_vec(),
                }],
            }
            .encode_to_vec(),
        ),
    );
    let error = LanternError::from(status);
    assert_eq!(error.search_reason(), None);
    assert!(matches!(
        error,
        LanternError::Rpc(failure)
            if matches!(failure.search_details(), SearchDetails::Unknown { .. })
    ));
}

async fn start_client(
    overrides: &[(&str, &str)],
) -> Result<(GoServer, LanternClient), Box<dyn Error>> {
    let mut settings = vec![
        ("LANTERN_SEARCH_ENABLED", "true"),
        ("LANTERN_SEARCH_POSITIONS", "true"),
    ];
    settings.extend_from_slice(overrides);
    let mut server = GoServer::start(&settings)?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(&endpoint).connect().await?;
    Ok((server, client))
}

async fn put_matches(
    client: &LanternClient,
    prefix: &str,
    count: usize,
    query: &str,
) -> Result<Vec<Vertex>, Box<dyn Error>> {
    let mut vertices = Vec::new();
    for index in 0..count {
        let key = format!("{prefix}{index}");
        client
            .put_vertex(
                VertexInput::string(&key, query)
                    .with_expiration(Expiration::After(Duration::from_secs(120))),
            )
            .await?;
        vertices.push(client.get_vertex(key).await?);
    }
    Ok(vertices)
}

fn assert_rpc_reason(error: LanternError, code: Code, reason: SearchErrorReason, work_kind: &str) {
    assert_eq!(error.search_reason(), Some((reason, work_kind)));
    match error {
        LanternError::Rpc(failure) => {
            assert_eq!(failure.status().code(), code);
            assert!(!failure.status().details().is_empty());
        }
        other => panic!("expected RPC error, got {other}"),
    }
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_full_vertex_snapshot_clamp_and_cursor_bindings() -> TestResult {
    let (_server, client) = start_client(&[
        ("LANTERN_SEARCH_DEFAULT_LIMIT", "2"),
        ("LANTERN_SEARCH_MAX_LIMIT", "2"),
    ])
    .await?;
    let expected: HashMap<_, _> = put_matches(&client, "rust:snapshot:", 4, "meridian")
        .await?
        .into_iter()
        .map(|vertex| (vertex.key.clone(), vertex))
        .collect();
    put_matches(&client, "rust:outside:", 1, "meridian").await?;
    let mut request = SearchRequest::new("meridian");
    request.prefix = "rust:snapshot:".into();
    request.limit = 1;
    request.projection = SearchProjection::FullVertex;
    let first = client.search_vertices(request.clone()).await?;
    assert_eq!(first.effective_limit, 1);
    assert_eq!(first.hits.len(), 1);
    assert!(first.truncated);
    assert!(!first.continuation_limited);
    let cursor = first.next_cursor.clone().ok_or("missing search cursor")?;
    for hit in &first.hits {
        assert_eq!(hit.projection_status, SearchHitProjectionStatus::Snapshot);
        assert_eq!(hit.vertex.as_ref(), expected.get(&hit.key));
        assert!(hit.vertex.as_ref().unwrap().expiration.is_some());
    }
    let changed_key = expected
        .keys()
        .find(|key| *key != &first.hits[0].key)
        .ok_or("no later hit to replace")?
        .clone();
    client
        .put_vertex(VertexInput::string(&changed_key, "replacement"))
        .await?;
    assert_ne!(
        client.get_vertex(&changed_key).await?,
        expected[&changed_key]
    );

    let second = client
        .search_vertices_page_with_options(
            request.clone(),
            cursor.clone(),
            CallOptions::After(Duration::from_secs(2)),
        )
        .await?;
    assert_eq!(second.hits.len(), 1);
    assert_eq!(
        second.hits[0].vertex.as_ref(),
        expected.get(&second.hits[0].key)
    );
    assert!(second.next_cursor.is_some());

    for change in 0..5 {
        let mut mismatched = request.clone();
        match change {
            0 => mismatched.limit = 2,
            1 => mismatched.match_mode = MatchMode::All,
            2 => mismatched.projection = SearchProjection::KeyScore,
            3 => mismatched.prefix = "rust:".into(),
            _ => mismatched.query = "different".into(),
        }
        assert_rpc_reason(
            client
                .search_vertices_page(mismatched, cursor.clone())
                .await
                .expect_err("cursor-bound options must not change"),
            Code::InvalidArgument,
            SearchErrorReason::SearchCursorInvalid,
            "",
        );
    }
    let (_other_server, other_client) = start_client(&[]).await?;
    let mut wrong_endpoint =
        other_client.search_vertices_stream_from(request.clone(), cursor.clone());
    assert_rpc_reason(
        wrong_endpoint
            .next()
            .await
            .expect("cross-endpoint stream must report an error")
            .expect_err("cross-endpoint cursor must not restart"),
        Code::InvalidArgument,
        SearchErrorReason::SearchCursorInvalid,
        "",
    );
    assert!(wrong_endpoint.next().await.is_none());

    let mut collected = first.hits;
    let mut stream = client.search_vertices_stream_from(request.clone(), cursor);
    while let Some(hit) = stream.next().await {
        collected.push(hit?);
    }
    assert_eq!(collected.len(), 4);
    for hit in &collected {
        assert_eq!(hit.projection_status, SearchHitProjectionStatus::Snapshot);
        assert_eq!(hit.vertex.as_ref(), expected.get(&hit.key));
    }
    for adjacent in collected.windows(2) {
        assert!(
            adjacent[0].score > adjacent[1].score
                || adjacent[0].score == adjacent[1].score && adjacent[0].key < adjacent[1].key
        );
    }
    let mut clamped = request;
    clamped.limit = 10;
    let page = client.search_vertices(clamped).await?;
    assert_eq!(page.effective_limit, 2);
    assert_eq!(page.hits.len(), 2);
    assert!(page.truncated);
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_invalid_and_expired_cursors_stay_typed() -> TestResult {
    let (_server, client) = start_client(&[("LANTERN_SEARCH_CURSOR_TTL_SECONDS", "1")]).await?;
    put_matches(&client, "rust:cursor:", 2, "solstice").await?;
    let mut request = SearchRequest::new("solstice");
    request.limit = 1;
    request.prefix = "rust:cursor:".into();
    let first = client.search_vertices(request.clone()).await?;
    let cursor = first.next_cursor.ok_or("missing cursor for two hits")?;
    assert_rpc_reason(
        client
            .search_vertices_page(request.clone(), SearchCursor::from_bytes([255, 0, 7]))
            .await
            .expect_err("malformed cursor must be invalid"),
        Code::InvalidArgument,
        SearchErrorReason::SearchCursorInvalid,
        "",
    );
    tokio::time::sleep(Duration::from_millis(1_200)).await;
    assert_rpc_reason(
        client
            .search_vertices_page(request.clone(), cursor.clone())
            .await
            .expect_err("expired cursor must be stale"),
        Code::Aborted,
        SearchErrorReason::SearchCursorStale,
        "",
    );
    let mut stream = client.search_vertices_stream_from(request, cursor);
    assert_rpc_reason(
        stream
            .next()
            .await
            .expect("stale stream must report an error")
            .expect_err("stale stream must not restart"),
        Code::Aborted,
        SearchErrorReason::SearchCursorStale,
        "",
    );
    assert!(stream.next().await.is_none());
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_disabled_positions_and_work_budget_reasons() -> TestResult {
    {
        let (_server, client) = start_client(&[("LANTERN_SEARCH_ENABLED", "false")]).await?;
        assert_rpc_reason(
            client
                .search_vertices(SearchRequest::new("meridian"))
                .await
                .expect_err("disabled search must not return empty success"),
            Code::FailedPrecondition,
            SearchErrorReason::SearchDisabled,
            "",
        );
        let mut invalid = SearchRequest::new("meridian");
        invalid.phrase = true;
        invalid.match_mode = MatchMode::Any;
        assert!(matches!(
            client.search_vertices(invalid).await,
            Err(LanternError::InvalidInput(_))
        ));
    }
    {
        let (_server, client) = start_client(&[("LANTERN_SEARCH_POSITIONS", "false")]).await?;
        let mut phrase = SearchRequest::new("meridian");
        phrase.phrase = true;
        assert_rpc_reason(
            client
                .search_vertices(phrase)
                .await
                .expect_err("phrase requires positional postings"),
            Code::FailedPrecondition,
            SearchErrorReason::SearchPositionsDisabled,
            "",
        );
    }
    {
        let (_server, client) = start_client(&[
            ("LANTERN_SEARCH_MAX_QUERY_BYTES", "8"),
            ("LANTERN_SEARCH_MAX_QUERY_TERMS", "8"),
        ])
        .await?;
        assert_rpc_reason(
            client
                .search_vertices(SearchRequest::new("too-many-query-bytes"))
                .await
                .expect_err("oversized query must exhaust its deterministic budget"),
            Code::ResourceExhausted,
            SearchErrorReason::SearchWorkBudgetExhausted,
            "query_bytes",
        );
    }
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_bounded_tail_emits_terminal_error_after_retained_hits() -> TestResult {
    let (_server, client) = start_client(&[
        ("LANTERN_SEARCH_DEFAULT_LIMIT", "1"),
        ("LANTERN_SEARCH_MAX_LIMIT", "1"),
        ("LANTERN_SEARCH_MAX_SESSION_HITS", "2"),
    ])
    .await?;
    put_matches(&client, "rust:bounded:", 4, "auroralight").await?;
    let mut request = SearchRequest::new("auroralight");
    request.limit = 1;
    request.prefix = "rust:bounded:".into();
    let first = client.search_vertices(request.clone()).await?;
    assert_eq!(first.hits.len(), 1);
    assert!(first.truncated && first.continuation_limited);
    let cursor = first.next_cursor.ok_or("retained second page missing")?;
    let last = client
        .search_vertices_page(request.clone(), cursor.clone())
        .await?;
    assert_eq!(last.hits.len(), 1);
    assert!(last.truncated && last.continuation_limited);
    assert!(last.next_cursor.is_none());
    let mut full_stream = client.search_vertices_stream(request.clone());
    let first_hit = full_stream.next().await.unwrap()?;
    let last_hit = full_stream.next().await.unwrap()?;
    assert_ne!(first_hit.key, last_hit.key);
    assert!(matches!(
        full_stream.next().await,
        Some(Err(LanternError::SearchContinuationLimited))
    ));
    assert!(full_stream.next().await.is_none());

    let mut stream = client.search_vertices_stream_from(request, cursor);
    assert!(stream.next().await.unwrap().is_ok());
    assert!(matches!(
        stream.next().await,
        Some(Err(LanternError::SearchContinuationLimited))
    ));
    assert!(stream.next().await.is_none());
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_unretained_tail_errors_even_without_a_cursor() -> TestResult {
    let (_server, client) = start_client(&[
        ("LANTERN_SEARCH_DEFAULT_LIMIT", "1"),
        ("LANTERN_SEARCH_MAX_LIMIT", "1"),
        ("LANTERN_SEARCH_MAX_SESSION_HITS", "2"),
        ("LANTERN_SEARCH_MAX_SESSION_BYTES", "1"),
    ])
    .await?;
    put_matches(&client, "rust:unretained:", 4, "lumenfield").await?;
    let mut request = SearchRequest::new("lumenfield");
    request.limit = 1;
    request.prefix = "rust:unretained:".into();
    let page = client.search_vertices(request.clone()).await?;
    assert_eq!(page.hits.len(), 1);
    assert!(page.truncated && page.continuation_limited);
    assert!(page.next_cursor.is_none());
    let mut stream = client.search_vertices_stream(request);
    assert!(stream.next().await.unwrap().is_ok());
    assert!(matches!(
        stream.next().await,
        Some(Err(LanternError::SearchContinuationLimited))
    ));
    assert!(stream.next().await.is_none());
    Ok(())
}
