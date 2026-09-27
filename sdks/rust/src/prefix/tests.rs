use std::error::Error;

use super::*;
use crate::{EdgeInput, VertexInput, scans::EdgeScanOptions, test_server::GoServer};

type TestResult = Result<(), Box<dyn Error>>;

#[test]
fn controlled_prefix_guards_and_bounded_response_counts() {
    assert!(matches!(
        require_edge_prefix("", ""),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(require_edge_prefix("tail/", "").is_ok());
    assert!(require_edge_prefix("", "head/").is_ok());
    assert_eq!(checked_deleted(2, 2).unwrap(), 2);
    assert_eq!(checked_deleted(10, 0).unwrap(), 10);
    assert!(matches!(
        checked_deleted(3, 2),
        Err(LanternError::Protocol(_))
    ));
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_prefix_count_dry_run_and_bounded_vertex_delete() -> TestResult {
    let mut server = GoServer::start(&[])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(&endpoint).connect().await?;
    client
        .put_vertices([
            VertexInput::nil("rust:prefix:v:a"),
            VertexInput::nil("rust:prefix:v:b"),
            VertexInput::nil("rust:prefix:v:c"),
            VertexInput::nil("rust:prefix:outside"),
        ])
        .await?;
    let prefix = "rust:prefix:v:";
    assert_eq!(client.count_vertices_by_prefix(prefix).await?, 3);
    assert_eq!(
        client
            .count_vertices_by_prefix_with_options("", CallOptions::Default)
            .await?,
        4,
        "an empty count prefix is valid"
    );
    assert_eq!(client.delete_vertices_by_prefix(prefix, 1, true).await?, 1);
    assert_eq!(client.count_vertices_by_prefix(prefix).await?, 3);
    assert_eq!(
        client
            .delete_vertices_by_prefix_with_options(prefix, 1, false, CallOptions::Default)
            .await?,
        1
    );
    assert_eq!(
        client.count_vertices_by_prefix(prefix).await?,
        2,
        "one call must not silently drain the rest of the prefix"
    );
    assert_eq!(client.delete_vertices_by_prefix(prefix, 2, true).await?, 2);
    assert_eq!(client.count_vertices_by_prefix(prefix).await?, 2);
    assert_eq!(client.delete_vertices_by_prefix(prefix, 2, false).await?, 2);
    assert_eq!(client.count_vertices_by_prefix(prefix).await?, 0);
    assert_eq!(client.count_vertices_by_prefix("rust:prefix:").await?, 1);
    assert_eq!(
        client.delete_vertices_by_prefix("", 1, true).await?,
        1,
        "the Go server permits an explicitly empty vertex-delete prefix"
    );
    assert_eq!(client.count_vertices_by_prefix("").await?, 1);

    let bounded = LanternClient::builder(&endpoint)
        .message_limits(32, 1024 * 1024)?
        .connect()
        .await?;
    assert!(matches!(
        bounded
            .delete_vertices_by_prefix("rust:prefix:".repeat(12), 1, false)
            .await,
        Err(LanternError::MessageTooLarge { .. })
    ));
    assert_eq!(client.count_vertices_by_prefix("").await?, 1);
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_edge_delete_requires_scope_and_does_not_drain() -> TestResult {
    let mut server = GoServer::start(&[])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(&endpoint).connect().await?;
    client
        .put_edges([
            EdgeInput::new("rust:prefix:tail:a", "rust:prefix:head:p1", 1.0),
            EdgeInput::new("rust:prefix:tail:a", "rust:prefix:head:p2", 2.0),
            EdgeInput::new("rust:prefix:tail:b", "rust:prefix:head:p1", 3.0),
            EdgeInput::new("rust:prefix:other", "rust:prefix:head:q", 4.0),
        ])
        .await?;
    let scope = EdgeScanOptions {
        tail_prefix: "rust:prefix:tail:".into(),
        head_prefix: "rust:prefix:head:p".into(),
        limit: 10,
    };
    assert_eq!(
        client
            .scan_edges_page(scope.clone(), None)
            .await?
            .items
            .len(),
        3
    );
    assert!(matches!(
        client.delete_edges_by_prefix("", "", 1, true).await,
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        client.delete_edges_by_prefix("", "", 1, false).await,
        Err(LanternError::InvalidInput(_))
    ));
    assert_eq!(
        client
            .delete_edges_by_prefix("rust:prefix:tail:", "rust:prefix:head:p", 2, true)
            .await?,
        2
    );
    assert_eq!(
        client
            .scan_edges_page(scope.clone(), None)
            .await?
            .items
            .len(),
        3
    );
    assert_eq!(
        client
            .delete_edges_by_prefix_with_options(
                "rust:prefix:tail:",
                "rust:prefix:head:p",
                1,
                false,
                CallOptions::Default,
            )
            .await?,
        1
    );
    assert_eq!(
        client
            .scan_edges_page(scope.clone(), None)
            .await?
            .items
            .len(),
        2,
        "one bounded call must not drain every matching edge"
    );
    assert_eq!(
        client
            .delete_edges_by_prefix("", "rust:prefix:head:p", 1, true)
            .await?,
        1,
        "head-only scopes are valid"
    );
    assert_eq!(
        client
            .delete_edges_by_prefix("rust:prefix:tail:", "rust:prefix:head:p", 2, false)
            .await?,
        2
    );
    assert!(client.scan_edges_page(scope, None).await?.items.is_empty());
    let unaffected = client
        .scan_edges_page(
            EdgeScanOptions {
                tail_prefix: "rust:prefix:other".into(),
                head_prefix: "rust:prefix:head:q".into(),
                limit: 1,
            },
            None,
        )
        .await?;
    assert_eq!(unaffected.items.len(), 1);
    assert_eq!(unaffected.items[0].weight, 4.0);
    Ok(())
}
