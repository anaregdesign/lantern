use std::{error::Error, time::Duration};

use super::*;
use crate::{
    EdgeInput, Expiration, PutOutcome, RpcErrorKind, Timestamp, VertexInput, VertexValue,
    generated::graph::v1::Graph as WireGraph, test_server::GoServer,
};

fn vertex(key: &str) -> Vertex {
    Vertex {
        key: key.into(),
        value: Some(VertexValue::Nil(true)),
        expiration: None,
    }
}

fn wire_graph(edges: Vec<Edge>) -> IlluminateResponse {
    IlluminateResponse {
        graph: Some(WireGraph {
            vertices: vec![vertex("root"), vertex("neighbor")],
            edges,
        }),
    }
}

fn edge() -> Edge {
    Edge {
        tail: "root".into(),
        head: "neighbor".into(),
        weight: 2.0,
        expiration: Some(Timestamp {
            seconds: 1_900_000_000,
            nanos: 123,
        }),
    }
}

#[test]
fn family_is_required_and_zero_sentinels_are_preserved() {
    assert!(matches!(
        traversal_request("root", &TraversalOptions::bfs(0, 1)),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        traversal_request("root", &TraversalOptions::bfs(1, 0)),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        traversal_request("", &TraversalOptions::bfs(1, 1)),
        Err(LanternError::InvalidInput(_))
    ));

    let bfs = traversal_request("root", &TraversalOptions::bfs(2, 3)).unwrap();
    assert!(matches!(
        bfs.params,
        Some(Params::Bfs(BfsParams {
            step: 2,
            fan_out: 3,
            ..
        }))
    ));

    let ppr = traversal_request("root", &TraversalOptions::ppr(0)).unwrap();
    assert!(matches!(
        ppr.params,
        Some(Params::Ppr(PprParams {
            top_n: 0,
            restart_prob: 0.0,
            epsilon: 0.0,
        }))
    ));
    let community = traversal_request("root", &TraversalOptions::community(0)).unwrap();
    assert!(matches!(
        community.params,
        Some(Params::Community(LocalCommunityParams {
            max_size: 0,
            restart_prob: 0.0,
            epsilon: 0.0,
            ..
        }))
    ));

    let configured = TraversalOptions {
        family: TraversalFamily::LocalCommunity(CommunityOptions {
            max_size: 5,
            restart_prob: 0.2,
            epsilon: 0.001,
            reduction: Reduction::ShortestPathTree,
            objective: Objective::Minimize,
        }),
        weighting: Weighting::Bm25,
        vertex_prefix: "rust:".into(),
    };
    let wire = traversal_request("root", &configured).unwrap();
    assert_eq!(wire.weighting, Weighting::Bm25 as i32);
    assert_eq!(wire.vertex_prefix, "rust:");
    assert!(matches!(
        wire.params,
        Some(Params::Community(LocalCommunityParams {
            max_size: 5,
            reduction,
            objective,
            ..
        })) if reduction == Reduction::ShortestPathTree as i32
            && objective == Objective::Minimize as i32
    ));
}

#[test]
fn invalid_push_knobs_fail_without_erasing_server_defaults() {
    for prob in [f32::NAN, f32::INFINITY, -0.1, 1.0, 1.5] {
        assert!(matches!(
            traversal_request(
                "root",
                &TraversalOptions {
                    family: TraversalFamily::Ppr(PprOptions {
                        restart_prob: prob,
                        ..PprOptions::default()
                    }),
                    ..TraversalOptions::ppr(0)
                }
            ),
            Err(LanternError::InvalidInput(_))
        ));
    }
    for epsilon in [f32::NAN, f32::NEG_INFINITY, -1.0] {
        assert!(matches!(
            traversal_request(
                "root",
                &TraversalOptions {
                    family: TraversalFamily::LocalCommunity(CommunityOptions {
                        epsilon,
                        ..CommunityOptions::default()
                    }),
                    ..TraversalOptions::community(0)
                }
            ),
            Err(LanternError::InvalidInput(_))
        ));
    }
    assert!(traversal_request("root", &TraversalOptions::ppr(0)).is_ok());
}

#[test]
fn real_edges_retain_expiration_but_ppr_links_are_never_stored_edges() {
    let stored = edge();
    for family in [
        TraversalFamily::Bfs(BfsOptions::new(1, 1)),
        TraversalFamily::LocalCommunity(CommunityOptions::default()),
    ] {
        let graph = decode_graph(wire_graph(vec![stored.clone()]), "root", &family).unwrap();
        assert_eq!(
            graph.edges["root"]["neighbor"],
            GraphEdge::Traversed(stored.clone())
        );
    }

    let ppr = TraversalFamily::Ppr(PprOptions::default());
    let mut synthetic = stored.clone();
    synthetic.expiration = None;
    synthetic.weight = 0.42;
    let graph = decode_graph(wire_graph(vec![synthetic.clone()]), "root", &ppr).unwrap();
    assert_eq!(
        graph.edges["root"]["neighbor"],
        GraphEdge::PprRelevance { mass: 0.42 }
    );
    for broken in [
        stored,
        Edge {
            tail: "neighbor".into(),
            head: "root".into(),
            ..synthetic
        },
    ] {
        assert!(matches!(
            decode_graph(wire_graph(vec![broken]), "root", &ppr),
            Err(LanternError::Protocol(_))
        ));
    }
}

#[test]
fn malformed_traversal_graph_fails_closed() {
    let bfs = TraversalFamily::Bfs(BfsOptions::new(1, 1));
    assert!(matches!(
        decode_graph(IlluminateResponse { graph: None }, "root", &bfs),
        Err(LanternError::Protocol(_))
    ));
    let mut graph = wire_graph(vec![edge()]);
    graph.graph.as_mut().unwrap().vertices.push(vertex("root"));
    assert!(matches!(
        decode_graph(graph, "root", &bfs),
        Err(LanternError::Protocol(_))
    ));
    let mut graph = wire_graph(vec![edge()]);
    graph.graph.as_mut().unwrap().edges.push(edge());
    assert!(matches!(
        decode_graph(graph, "root", &bfs),
        Err(LanternError::Protocol(_))
    ));
    let mut graph = wire_graph(vec![edge()]);
    graph.graph.as_mut().unwrap().edges[0].head = "missing".into();
    assert!(matches!(
        decode_graph(graph, "root", &bfs),
        Err(LanternError::Protocol(_))
    ));
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_bfs_community_and_synthetic_ppr() -> Result<(), Box<dyn Error>> {
    let mut server = GoServer::start(&[])?;
    server.wait_for_listener()?;
    let client = LanternClient::builder(format!("http://127.0.0.1:{}", server.port()))
        .connect()
        .await?;
    for key in ["rust:root", "rust:a", "rust:b"] {
        assert_eq!(
            client.put_vertex(VertexInput::nil(key)).await?,
            PutOutcome::AppliedAndLive
        );
    }
    for (tail, head) in [
        ("rust:root", "rust:a"),
        ("rust:a", "rust:root"),
        ("rust:root", "rust:b"),
        ("rust:b", "rust:root"),
        ("rust:a", "rust:b"),
        ("rust:b", "rust:a"),
    ] {
        client
            .put_edge(
                EdgeInput::new(tail, head, 2.0)
                    .with_expiration(Expiration::After(Duration::from_secs(120))),
            )
            .await?;
    }

    let bfs = client
        .illuminate("rust:root", TraversalOptions::bfs(1, 2))
        .await?;
    match &bfs.edges["rust:root"]["rust:a"] {
        GraphEdge::Traversed(edge) => {
            assert_eq!(edge.weight, 2.0);
            assert!(edge.expiration.is_some());
        }
        GraphEdge::PprRelevance { .. } => panic!("BFS fabricated a synthetic relevance link"),
    }
    let ppr = client
        .illuminate("rust:root", TraversalOptions::ppr(0))
        .await?;
    assert!(!ppr.edges.is_empty());
    for (tail, heads) in &ppr.edges {
        assert_eq!(tail, "rust:root");
        for link in heads.values() {
            assert!(matches!(link, GraphEdge::PprRelevance { mass } if *mass > 0.0));
        }
    }
    let community = client
        .illuminate("rust:root", TraversalOptions::community(0))
        .await?;
    assert!(community.vertices.contains_key("rust:root"));
    assert!(!community.edges.is_empty());
    for heads in community.edges.values() {
        for link in heads.values() {
            assert!(matches!(
                link,
                GraphEdge::Traversed(Edge {
                    expiration: Some(_),
                    ..
                })
            ));
        }
    }
    assert!(matches!(
        client
            .illuminate("rust:root", TraversalOptions::bfs(0, 1))
            .await,
        Err(LanternError::InvalidInput(_))
    ));
    for options in [
        TraversalOptions::ppr(u32::MAX),
        TraversalOptions::community(u32::MAX),
    ] {
        assert!(matches!(
            client.illuminate("rust:root", options).await,
            Err(LanternError::Rpc(failure))
                if failure.kind() == RpcErrorKind::InvalidArgument
        ));
    }
    Ok(())
}
