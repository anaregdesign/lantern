//! End-to-end HTTPS example. Choose a private, nonempty `LANTERN_PREFIX`;
//! this example creates and deletes keys in that namespace. See the crate
//! README for optional TLS/credential environment variables.

use std::{env, error::Error, fs, future::Future, io, pin::Pin, sync::Arc, time::Duration};

use lantern_client::{
    AddInput, DegreeRankingOptions, EdgeInput, Expiration, GraphEdge, LanternClient, LanternError,
    PutOutcome, RetryPolicy, ScanOptions, ScanOrder, SearchRequest, TokenError, TokenProvider,
    TraversalOptions, VertexInput,
};

struct EnvironmentToken;

impl TokenProvider for EnvironmentToken {
    fn token(&self) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
        Box::pin(async {
            env::var("LANTERN_BEARER_TOKEN").map_err(|error| Box::new(error) as TokenError)
        })
    }
}

fn report_batch<T>(result: Result<T, LanternError>) -> Result<T, LanternError> {
    if let Err(LanternError::Batch(batch)) = &result {
        eprintln!(
            "batch failed after {} fully observed items; the failed chunk may have applied",
            batch.completed_items
        );
    }
    result
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let endpoint = env::var("LANTERN_ENDPOINT")?;
    if !endpoint.starts_with("https://") {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "this example requires certificate-verified HTTPS",
        )
        .into());
    }
    let prefix = env::var("LANTERN_PREFIX")?;
    if prefix.is_empty() {
        return Err(io::Error::new(io::ErrorKind::InvalidInput, "prefix must be nonempty").into());
    }

    let mut builder = LanternClient::builder(&endpoint)
        .batch_chunk_size(2)?
        .retry(RetryPolicy::Unavailable { max_attempts: 2 })?
        .auto_contribution_ids(true);
    if let Some(path) = env::var_os("LANTERN_CA_PEM") {
        builder = builder.tls_private_ca_pem(fs::read(path)?)?;
    }
    match (
        env::var_os("LANTERN_CLIENT_CERT_PEM"),
        env::var_os("LANTERN_CLIENT_KEY_PEM"),
    ) {
        (Some(cert), Some(key)) => {
            builder = builder.tls_client_identity(fs::read(cert)?, fs::read(key)?)?;
        }
        (None, None) => {}
        _ => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "client certificate and key must be configured together",
            )
            .into());
        }
    }
    if env::var_os("LANTERN_BEARER_TOKEN").is_some() {
        builder = builder.token_provider(Arc::new(EnvironmentToken));
    }
    let client = builder.connect().await?;
    client.ping().await?;

    let seed = format!("{prefix}root");
    let counter = format!("{prefix}counter");
    let nil = format!("{prefix}nil");
    let ttl = Expiration::After(Duration::from_secs(300));
    let outcomes = report_batch(
        client
            .put_vertices([
                VertexInput::string(&seed, "lantern rust sdk").with_expiration(ttl.clone()),
                VertexInput::int64(&counter, 42).with_expiration(ttl.clone()),
                VertexInput::nil(&nil).with_expiration(ttl.clone()),
            ])
            .await,
    )?;
    if outcomes
        .iter()
        .any(|outcome| *outcome != PutOutcome::AppliedAndLive)
    {
        return Err(io::Error::other("a sample vertex Put did not remain live").into());
    }
    let fetched = client.get_vertices([&seed, &counter, &nil]).await?;
    println!(
        "found {} exact vertices, missing {}",
        fetched.found.len(),
        fetched.missing.len()
    );
    let stored = client.get_vertex(&counter).await?;
    if stored.int64_value() != Some(42) || stored.expiration.is_none() {
        return Err(io::Error::other("exact value/TTL round-trip changed").into());
    }
    println!(
        "counter={:?}, expires={:?}",
        stored.int64_value(),
        stored.expiration
    );

    let edge = EdgeInput::new(&seed, &counter, 2.0).with_expiration(ttl.clone());
    println!(
        "PutEdge replaced weight: {:?}",
        client.put_edge(edge).await?
    );
    let prepared = client.prepare_add([AddInput::new(
        EdgeInput::new(&seed, &counter, 1.0).with_expiration(ttl),
    )])?;
    let added = report_batch(client.add_prepared_edges(&prepared).await)?;
    if added.effective_weights != [3.0] {
        return Err(io::Error::other("Add did not accumulate after Put").into());
    }
    println!("AddEdge effective weights: {:?}", added.effective_weights);

    let keys = client
        .scan_vertex_keys_page(
            ScanOptions {
                prefix: prefix.clone(),
                limit: 8,
                order: ScanOrder::Asc,
            },
            None,
        )
        .await?;
    for key in keys.items {
        println!("key: {key}");
    }

    let status = client.server_status().await?;
    println!(
        "{}: {} vertices, {} edges",
        status.version, status.vertex_count, status.edge_count
    );
    let replication = client.replication_status().await?;
    println!("replication enabled: {}", replication.enabled);

    if status.search.as_ref().is_some_and(|search| search.enabled) {
        let mut request = SearchRequest::new("lantern rust sdk");
        request.prefix = prefix.clone();
        request.limit = 8;
        let results = client.search_vertices(request).await?;
        for hit in results.hits {
            println!("search: {} score={}", hit.key, hit.score);
        }
    } else {
        println!("full-text search is disabled on this server");
    }

    for entry in client
        .top_vertices_by_degree(DegreeRankingOptions::new(&prefix))
        .await?
    {
        println!("{} degree={}", entry.key, entry.degree);
    }
    let graph = client
        .illuminate(&seed, TraversalOptions::bfs(1, 8))
        .await?;
    for (tail, heads) in graph.edges {
        for (head, link) in heads {
            if let GraphEdge::Traversed(edge) = link {
                println!(
                    "real {tail} -> {head}: {} (expires {:?})",
                    edge.weight, edge.expiration
                );
            }
        }
    }
    let star = client.illuminate(&seed, TraversalOptions::ppr(8)).await?;
    for heads in star.edges.values() {
        for (head, link) in heads {
            if let GraphEdge::PprRelevance { mass } = link {
                println!("synthetic relevance for {head}: {mass}");
            }
        }
    }
    let community = client
        .illuminate(&seed, TraversalOptions::community(8))
        .await?;
    println!("local community members: {}", community.vertices.len());

    println!(
        "deleted edge: {}",
        client.delete_edge(&seed, &counter).await?
    );
    let deleted = report_batch(client.delete_vertices([seed, counter, nil]).await)?;
    println!("deleted {} sample vertices", deleted.deleted);
    Ok(())
}
