//! Verified-HTTPS CDC and graph-only backup example. The archive and
//! separately retained manifest are created only at user-selected paths.
//! Restore is opt-in and writes to a separately named destination.

use std::{
    env,
    error::Error,
    fs::{self, File, OpenOptions},
    future::Future,
    io::{self, Read, Write},
    pin::Pin,
    sync::Arc,
    time::Duration,
};

use lantern_client::{
    BackupFormat, BackupManifest, IdentityEvent, LanternClient, LanternError, RestoreOptions,
    StreamOptions, TokenError, TokenProvider,
};

struct EnvironmentToken;

impl TokenProvider for EnvironmentToken {
    fn token(&self) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
        Box::pin(async {
            env::var("LANTERN_BEARER_TOKEN").map_err(|error| Box::new(error) as TokenError)
        })
    }
}

async fn connect(endpoint: &str) -> Result<LanternClient, Box<dyn Error>> {
    if !endpoint.starts_with("https://") {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "this example requires a certificate-verified HTTPS endpoint",
        )
        .into());
    }
    let mut builder = LanternClient::builder(endpoint);
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
                "client certificate and key must be supplied together",
            )
            .into());
        }
    }
    if env::var_os("LANTERN_BEARER_TOKEN").is_some() {
        builder = builder.token_provider(Arc::new(EnvironmentToken));
    }
    let client = builder.connect().await?;
    client.ping().await?;
    Ok(client)
}

fn read_manifest(path: &str) -> Result<BackupManifest, Box<dyn Error>> {
    let mut bytes = Vec::new();
    File::open(path)?.take(16_385).read_to_end(&mut bytes)?;
    if bytes.len() > 16_384 {
        return Err(io::Error::new(io::ErrorKind::InvalidData, "manifest is too large").into());
    }
    Ok(BackupManifest::from_json(std::str::from_utf8(&bytes)?)?)
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let endpoint = env::var("LANTERN_ENDPOINT")?;
    let prefix = env::var("LANTERN_PREFIX")?;
    let archive_path = env::var("LANTERN_BACKUP_PATH")?;
    let manifest_path = env::var("LANTERN_MANIFEST_PATH")?;
    if prefix.is_empty() || archive_path == manifest_path {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "choose a nonempty private prefix and distinct archive/manifest paths",
        )
        .into());
    }
    let client = connect(&endpoint).await?;
    let mut identities = client
        .bootstrap_identities(
            StreamOptions::default()
                .with_idle(Duration::from_secs(3))
                .with_lifetime(Duration::from_secs(10)),
        )
        .await?;
    let IdentityEvent::Checkpoint(checkpoint) = identities.next_event().await? else {
        return Err(io::Error::other("bootstrap did not begin with a checkpoint").into());
    };

    // Replace this demonstration with your resident-cache revalidation and
    // atomic invalidation+cursor transaction before using CDC in production.
    let resident = env::var("LANTERN_RESIDENT_KEYS").unwrap_or_default();
    let mut checked = 0;
    for key in resident.split(',').filter(|key| !key.is_empty()) {
        if !key.starts_with(&prefix) {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "resident keys must be inside the selected private prefix",
            )
            .into());
        }
        match client.get_vertex(key).await {
            Ok(_) | Err(LanternError::NotFound) => checked += 1,
            Err(error) => return Err(error.into()),
        }
    }
    let mut cursor = checkpoint.cursor_after_revalidation()?;
    println!("revalidated {checked} resident identities at the responder's cut");
    drop(identities);

    let mut resumed = client
        .resume_identities(
            cursor.clone(),
            StreamOptions::default().with_idle(Duration::from_secs(2)),
        )
        .await?;
    let mut incomplete = false;
    loop {
        match resumed.next_event().await {
            Ok(IdentityEvent::Chunk(chunk)) => {
                incomplete = chunk.next_cursor.is_none();
                if let Some(next) = chunk.next_cursor {
                    cursor = next;
                    println!(
                        "observed a complete identity invalidation at origin {} seq {}",
                        chunk.origin, chunk.seq
                    );
                    break;
                }
            }
            Ok(IdentityEvent::Checkpoint(_)) => {
                return Err(io::Error::other("resumed CDC unexpectedly sent a checkpoint").into());
            }
            Err(LanternError::StreamIdleTimeout) if !incomplete => break,
            Err(LanternError::StreamIdleTimeout) => {
                return Err(io::Error::other(
                    "identity chunks stopped before the final marker: rebootstrap and revalidate",
                )
                .into());
            }
            Err(error) => return Err(error.into()),
        }
    }
    drop(resumed);
    if env::var("LANTERN_SHOW_FULL_CDC").as_deref() == Ok("1") {
        let mut full = client
            .subscribe_full_mutations(
                cursor,
                StreamOptions::default().with_idle(Duration::from_secs(2)),
            )
            .await?;
        match full.next_mutation().await {
            Ok(mutation) => println!(
                "observed explicit full-mode mutation at origin {} seq {}; payload not printed",
                mutation.origin, mutation.seq
            ),
            Err(LanternError::StreamIdleTimeout) => {}
            Err(error) => return Err(error.into()),
        }
    }

    let format = match env::var("LANTERN_BACKUP_FORMAT").as_deref() {
        Ok("rust-ndjson-v1") => BackupFormat::RustNdjsonV1,
        Ok("protobuf") | Err(_) => BackupFormat::LengthDelimitedProtobuf,
        _ => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "backup format must be protobuf or rust-ndjson-v1",
            )
            .into());
        }
    };
    let mut archive = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&archive_path)?;
    let manifest = client
        .write_backup(
            &mut archive,
            &prefix,
            format,
            StreamOptions::default().with_lifetime(Duration::from_secs(60)),
        )
        .await?;
    archive.sync_all()?;
    let mut manifest_file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&manifest_path)?;
    manifest_file.write_all(manifest.to_json().as_bytes())?;
    manifest_file.sync_all()?;
    println!(
        "captured {} vertices and {} folded edges; keep the manifest separately",
        manifest.vertex_count(),
        manifest.edge_count()
    );

    if let Ok(destination) = env::var("LANTERN_RESTORE_ENDPOINT") {
        if env::var("LANTERN_CONFIRM_EMPTY_RESTORE").as_deref() != Ok("yes")
            || destination == endpoint
        {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "restore requires a different, empty destination and LANTERN_CONFIRM_EMPTY_RESTORE=yes",
            )
            .into());
        }
        let target = connect(&destination).await?;
        let persisted_manifest = read_manifest(&manifest_path)?;
        let mut source = File::open(&archive_path)?;
        match target
            .restore_backup(&mut source, &persisted_manifest, RestoreOptions::default())
            .await
        {
            Ok(report) => println!(
                "observed {} vertex and {} edge Put results",
                report.completed_vertices, report.completed_edges
            ),
            Err(failure) => {
                eprintln!(
                    "partial restore: {} validated vertices, {} validated edges; failed batch may have applied",
                    failure.progress.completed_vertices, failure.progress.completed_edges
                );
                return Err(failure.into());
            }
        }
    }
    Ok(())
}
