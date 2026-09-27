use std::error::Error;

use super::*;
use crate::{EdgeInput, VertexInput, test_server::GoServer};

#[test]
fn degree_requires_prefix_and_rejects_malformed_ranking() {
    let options = DegreeRankingOptions {
        k: 2,
        ..DegreeRankingOptions::new("rust:")
    };
    let entry = DegreeEntry {
        key: "rust:a".into(),
        degree: 1,
        weighted_degree: 1.5,
    };
    let good = TopVerticesByDegreeResponse {
        entries: vec![entry.clone()],
    };
    assert_eq!(
        validate_ranking(&options, good).unwrap(),
        std::slice::from_ref(&entry)
    );
    for bad in [
        vec![entry.clone(), entry.clone()],
        vec![DegreeEntry {
            key: "other:a".into(),
            ..entry.clone()
        }],
        vec![entry.clone(), entry.clone(), entry],
    ] {
        assert!(matches!(
            validate_ranking(&options, TopVerticesByDegreeResponse { entries: bad }),
            Err(LanternError::Protocol(_))
        ));
    }
    assert_eq!(DegreeRankingOptions::new("rust:").k, 0);
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_degree_directions_and_explicit_status_snapshots() -> Result<(), Box<dyn Error>> {
    let mut server = GoServer::start(&[])?;
    server.wait_for_listener()?;
    let client = LanternClient::builder(format!("http://127.0.0.1:{}", server.port()))
        .connect()
        .await?;
    for key in ["rust:a", "rust:b", "other:c"] {
        client.put_vertex(VertexInput::nil(key)).await?;
    }
    for (tail, head, weight) in [
        ("rust:a", "rust:b", 2.0),
        ("rust:a", "other:c", 3.0),
        ("other:c", "rust:b", 4.0),
    ] {
        client.put_edge(EdgeInput::new(tail, head, weight)).await?;
    }

    let mut options = DegreeRankingOptions::new("rust:");
    options.k = 2;
    let ranked = client.top_vertices_by_degree(options.clone()).await?;
    assert_eq!((ranked[0].key.as_str(), ranked[0].degree), ("rust:a", 2));
    options.direction = DegreeDirection::In;
    options.weighted = true;
    let ranked = client.top_vertices_by_degree(options.clone()).await?;
    assert_eq!(
        (
            ranked[0].key.as_str(),
            ranked[0].degree,
            ranked[0].weighted_degree
        ),
        ("rust:b", 2, 6.0)
    );
    options.direction = DegreeDirection::Both;
    options.weighted = false;
    let ranked = client.top_vertices_by_degree(options).await?;
    assert_eq!((ranked[0].key.as_str(), ranked[0].degree), ("rust:a", 2));
    assert_eq!((ranked[1].key.as_str(), ranked[1].degree), ("rust:b", 2));
    assert!(matches!(
        client
            .top_vertices_by_degree(DegreeRankingOptions::new(""))
            .await,
        Err(LanternError::InvalidInput(_))
    ));

    let status = client.server_status().await?;
    assert!(status.vertex_count >= 3);
    assert!(status.edge_count >= 3);
    assert!(status.max_batch_size > 0);
    assert!(status.search.is_some());
    let replication = client.replication_status().await?;
    assert!(!replication.enabled);
    assert!(replication.peers.is_empty());
    assert_eq!(replication.node_id.len(), 32);
    assert!(replication.local_now.is_some());
    Ok(())
}
