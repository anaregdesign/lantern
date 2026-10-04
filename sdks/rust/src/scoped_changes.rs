//! Public, scope-bound CDC. Replication origin vectors are never inferred.
use crate::{
    Edge, EdgeRef, LanternClient, LanternError, StreamOptions, Vertex,
    error::CdcGap,
    generated::graph::v1::{
        self as wire,
        change_invalidation::{CurrentImage, Identity},
    },
    transport::DataStream,
};
use prost::Message;

/// Opaque Server checkpoint. Persist only after applying its whole frame.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ChangeCursor(Vec<u8>);
impl ChangeCursor {
    pub fn new(bytes: impl Into<Vec<u8>>) -> Result<Self, LanternError> {
        let bytes = bytes.into();
        if bytes.is_empty() || bytes.len() > 8192 {
            return Err(LanternError::InvalidInput("invalid opaque change cursor"));
        }
        Ok(Self(bytes))
    }
    pub fn as_bytes(&self) -> &[u8] {
        &self.0
    }
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub enum ChangeProjection {
    #[default]
    Identity,
    Value,
}

/// Exactly one of bootstrap or a cursor is required. Prefix is literal and
/// applies to both Edge endpoints. Stream deadlines are separate from unary.
#[derive(Clone, Debug, Default)]
pub struct WatchChangesOptions {
    pub prefix: String,
    pub projection: ChangeProjection,
    pub bootstrap: bool,
    pub cursor: Option<ChangeCursor>,
}

/// Value images, when authorized, describe current local state, not the
/// original mutation payload. No image means invalidate and optionally refetch.
#[derive(Clone, Debug, PartialEq)]
pub enum ChangeInvalidation {
    Vertex {
        key: String,
        current: Option<Vertex>,
    },
    Edge {
        identity: EdgeRef,
        current: Option<Edge>,
    },
}
#[derive(Clone, Debug, PartialEq)]
pub struct ChangeFrame {
    pub invalidations: Vec<ChangeInvalidation>,
    /// Intermediate mutation frames have no cursor.
    pub cursor: Option<ChangeCursor>,
    pub bootstrap: bool,
}

/// Demand-driven public stream. Dropping it cancels its one RPC attempt.
pub struct ChangeStream {
    wire: Option<DataStream>,
    projection: ChangeProjection,
    bootstrap: bool,
    bootstrapped: bool,
}
impl LanternClient {
    /// Keep a bootstrap tail open while rebuilding resident keys through
    /// ordinary authorized reads. No automatic reconnect/retry or cursor reset.
    pub async fn watch_changes(
        &self,
        options: WatchChangesOptions,
        stream: StreamOptions,
    ) -> Result<ChangeStream, LanternError> {
        if options.bootstrap == options.cursor.is_some() {
            return Err(LanternError::InvalidInput(
                "choose bootstrap or an opaque resume cursor",
            ));
        }
        let request = wire::WatchChangesRequest {
            prefix: options.prefix,
            projection: match options.projection {
                ChangeProjection::Identity => wire::ChangeProjection::Identity,
                ChangeProjection::Value => wire::ChangeProjection::Value,
            } as i32,
            bootstrap: options.bootstrap,
            cursor: options.cursor.map(|cursor| cursor.0).unwrap_or_default(),
        };
        let wire = self
            .raw_data_stream(
                request,
                "/graph.v1.LanternChangeService/WatchChanges",
                stream,
            )
            .await
            .map_err(crate::cdc::cdc_rpc_error)?;
        Ok(ChangeStream {
            wire: Some(wire),
            projection: options.projection,
            bootstrap: options.bootstrap,
            bootstrapped: false,
        })
    }
}
impl ChangeStream {
    /// Wait for one complete checked frame. Unexpected EOF is a typed gap;
    /// resume with the last applied checkpoint or rebuild if the Server rejects it.
    pub async fn next_frame(&mut self) -> Result<ChangeFrame, LanternError> {
        let result = self.read_frame().await;
        if result.is_err() {
            self.wire = None;
        }
        result
    }
    async fn read_frame(&mut self) -> Result<ChangeFrame, LanternError> {
        let raw = self
            .wire
            .as_mut()
            .ok_or_else(|| gap("public change stream has ended"))?
            .next()
            .await
            .map_err(crate::cdc::cdc_rpc_error)?
            .ok_or_else(|| gap("public change stream ended unexpectedly"))?;
        let frame = decode_frame(&raw, self.projection)?;
        if frame.bootstrap && (!self.bootstrap || self.bootstrapped)
            || !frame.bootstrap && self.bootstrap && !self.bootstrapped
        {
            return Err(gap("unexpected public change bootstrap order"));
        }
        self.bootstrapped |= frame.bootstrap;
        Ok(frame)
    }
}
fn gap(reason: &'static str) -> LanternError {
    LanternError::CdcGap(CdcGap::protocol(reason))
}
fn decode_frame(raw: &[u8], projection: ChangeProjection) -> Result<ChangeFrame, LanternError> {
    if raw.len() > 1 << 20 {
        return Err(gap("public change frame exceeds 1 MiB"));
    }
    let frame = wire::WatchChangesResponse::decode(raw)
        .map_err(|_| gap("malformed public change frame"))?;
    if frame.encoded_len() != raw.len()
        || frame.invalidations.len() > 1024
        || frame.cursor.len() > 8192
        || frame.invalidations.is_empty() && frame.cursor.is_empty()
        || frame.bootstrap && (!frame.invalidations.is_empty() || frame.cursor.is_empty())
    {
        return Err(gap("unknown or malformed public change frame"));
    }
    let mut invalidations = Vec::with_capacity(frame.invalidations.len());
    for item in frame.invalidations {
        if projection == ChangeProjection::Identity && item.current_image.is_some() {
            return Err(gap("identity CDC cannot contain a value"));
        }
        let invalidation = match (item.identity, item.current_image) {
            (Some(Identity::VertexKey(key)), None) if !key.is_empty() => {
                ChangeInvalidation::Vertex { key, current: None }
            }
            (Some(Identity::VertexKey(key)), Some(CurrentImage::Vertex(image)))
                if !key.is_empty() && key == image.key =>
            {
                ChangeInvalidation::Vertex {
                    key,
                    current: Some(image),
                }
            }
            (Some(Identity::EdgeKey(key)), None)
                if !key.tail.is_empty() && !key.head.is_empty() =>
            {
                ChangeInvalidation::Edge {
                    identity: EdgeRef {
                        tail: key.tail,
                        head: key.head,
                    },
                    current: None,
                }
            }
            (Some(Identity::EdgeKey(key)), Some(CurrentImage::Edge(image)))
                if !key.tail.is_empty()
                    && !key.head.is_empty()
                    && key.tail == image.tail
                    && key.head == image.head =>
            {
                ChangeInvalidation::Edge {
                    identity: EdgeRef {
                        tail: key.tail,
                        head: key.head,
                    },
                    current: Some(image),
                }
            }
            _ => return Err(gap("invalid public change identity or image")),
        };
        invalidations.push(invalidation);
    }
    let cursor = if frame.cursor.is_empty() {
        None
    } else {
        Some(ChangeCursor::new(frame.cursor).map_err(|_| gap("invalid public change cursor"))?)
    };
    Ok(ChangeFrame {
        invalidations,
        cursor,
        bootstrap: frame.bootstrap,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn checked_opaque_progress_and_value_projection() {
        let frame = wire::WatchChangesResponse {
            cursor: vec![1, 2, 3],
            bootstrap: false,
            invalidations: vec![wire::ChangeInvalidation {
                identity: Some(Identity::VertexKey("orders:1".into())),
                current_image: None,
            }],
        };
        let decoded = decode_frame(&frame.encode_to_vec(), ChangeProjection::Identity).unwrap();
        assert_eq!(decoded.cursor.unwrap().as_bytes(), &[1, 2, 3]);
        assert_eq!(
            decoded.invalidations,
            vec![ChangeInvalidation::Vertex {
                key: "orders:1".into(),
                current: None
            }]
        );
        let mut image = frame;
        image.invalidations[0].current_image = Some(CurrentImage::Vertex(Vertex {
            key: "orders:1".into(),
            ..Default::default()
        }));
        assert!(decode_frame(&image.encode_to_vec(), ChangeProjection::Identity).is_err());
        assert!(decode_frame(&image.encode_to_vec(), ChangeProjection::Value).is_ok());
        image.invalidations[0].current_image = Some(CurrentImage::Vertex(Vertex {
            key: "orders:2".into(),
            ..Default::default()
        }));
        assert!(decode_frame(&image.encode_to_vec(), ChangeProjection::Value).is_err());
    }
    #[test]
    fn malformed_or_future_frames_cannot_advance_progress() {
        let frame = wire::WatchChangesResponse {
            cursor: vec![1],
            bootstrap: true,
            invalidations: vec![],
        };
        let mut unknown = frame.encode_to_vec();
        unknown.extend_from_slice(&[0x98, 0x06, 1]);
        assert!(decode_frame(&unknown, ChangeProjection::Identity).is_err());
        assert!(decode_frame(&[], ChangeProjection::Identity).is_err());
        assert!(ChangeCursor::new(vec![]).is_err());
        assert!(ChangeCursor::new(vec![1; 8193]).is_err());
    }
    #[tokio::test]
    #[ignore = "run through the certified OIDC root integration fixture"]
    async fn real_public_scoped_wire() -> Result<(), Box<dyn std::error::Error>> {
        use crate::{RpcErrorKind, TokenError, TokenProvider, VertexInput};
        use std::{future::Future, pin::Pin, sync::Arc, time::Duration};
        struct Credential(String);
        impl TokenProvider for Credential {
            fn token(
                &self,
            ) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
                Box::pin(async { Ok(self.0.clone()) })
            }
        }
        let endpoint = std::env::var("LANTERN_SCOPED_WIRE_URL")?;
        let ca = std::fs::read(std::env::var("LANTERN_SCOPED_WIRE_CA")?)?;
        let sdk = LanternClient::builder(endpoint)
            .tls_private_ca_pem(ca)?
            .token_provider(Arc::new(Credential(std::env::var(
                "LANTERN_SCOPED_WIRE_CREDENTIAL",
            )?)))
            .connect()
            .await?;
        let options = WatchChangesOptions {
            prefix: "orders:".into(),
            bootstrap: true,
            ..Default::default()
        };
        let mut stream = sdk
            .watch_changes(
                options,
                StreamOptions::default().with_lifetime(Duration::from_secs(5)),
            )
            .await?;
        let first = stream.next_frame().await?;
        assert!(first.bootstrap && first.cursor.is_some() && first.invalidations.is_empty());
        sdk.put_vertices([
            VertexInput::string("orders:private:1", "hidden"),
            VertexInput::string("orders:1", "visible"),
        ])
        .await?;
        loop {
            let frame = stream.next_frame().await?;
            if frame.invalidations.is_empty() {
                continue;
            }
            assert_eq!(
                frame.invalidations,
                vec![ChangeInvalidation::Vertex {
                    key: "orders:1".into(),
                    current: None
                }]
            );
            break;
        }
        drop(stream);
        sdk.put_vertices([
            VertexInput::nil("orders:create:source:a"),
            VertexInput::nil("orders:create:target:b"),
        ])
        .await?;
        let input = crate::EdgeInput::new("orders:create:source:a", "orders:create:target:b", 2.0);
        assert_eq!(
            sdk.create_edges([
                input.clone(),
                crate::EdgeInput::new("orders:create:source:a", "orders:create:target:b", 9.0),
                crate::EdgeInput::new(
                    "orders:create:source:a",
                    "orders:create:target:missing",
                    1.0
                ),
                input.with_expiration(crate::Expiration::At(crate::Timestamp {
                    seconds: 0,
                    nanos: 1
                })),
            ])
            .await?,
            [
                crate::CreateEdgeOutcome::CreatedAndLive,
                crate::CreateEdgeOutcome::EdgeExists,
                crate::CreateEdgeOutcome::EndpointNotLive,
                crate::CreateEdgeOutcome::Expired
            ]
        );
        assert_eq!(
            sdk.create_edge(crate::EdgeInput::new(
                "orders:create:target:b",
                "orders:create:source:a",
                1.0,
            ))
            .await?,
            crate::CreateEdgeOutcome::CreatedAndLive
        );
        let denied_create = sdk
            .create_edge(crate::EdgeInput::new(
                "orders:create:source:a",
                "outside:denied",
                1.0,
            ))
            .await;
        assert!(
            matches!(denied_create, Err(LanternError::Batch(ref batch)) if matches!(*batch.source, LanternError::Rpc(ref failure) if failure.kind()==RpcErrorKind::PermissionDenied))
        );
        let denied = sdk
            .watch_changes(
                WatchChangesOptions {
                    prefix: "orders:".into(),
                    projection: ChangeProjection::Value,
                    bootstrap: true,
                    ..Default::default()
                },
                StreamOptions::default(),
            )
            .await;
        match denied {
            Err(LanternError::Rpc(error)) => {
                assert_eq!(error.kind(), RpcErrorKind::PermissionDenied)
            }
            Ok(mut stream) => {
                let error = stream
                    .next_frame()
                    .await
                    .expect_err("value projection inherited grant");
                assert!(
                    matches!(error,LanternError::Rpc(ref failure) if failure.kind()==RpcErrorKind::PermissionDenied)
                );
            }
            Err(other) => return Err(other.into()),
        }
        Ok(())
    }
    #[tokio::test]
    #[ignore = "requires a built production Go Server binary"]
    async fn real_wire_opaque_bootstrap_resume_and_bounded_gap()
    -> Result<(), Box<dyn std::error::Error>> {
        use crate::{VertexInput, test_server::GoServer};
        use std::time::Duration;
        let mut server = GoServer::start(&[("LANTERN_MUTATION_LOG_CAPACITY", "4")])?;
        server.wait_for_listener()?;
        let client = LanternClient::builder(format!("http://127.0.0.1:{}", server.port()))
            .unary_timeout(Duration::from_millis(300))?
            .connect()
            .await?;
        let options = |bootstrap, cursor| WatchChangesOptions {
            prefix: "rust:cdc-real:".into(),
            bootstrap,
            cursor,
            ..Default::default()
        };
        let mut stream = client
            .watch_changes(
                options(true, None),
                StreamOptions::default().with_idle(Duration::from_secs(5)),
            )
            .await?;
        assert!(stream.next_frame().await?.bootstrap);
        client
            .put_vertex(VertexInput::int64("rust:cdc-real:a", 52))
            .await?;
        let first = stream.next_frame().await?;
        assert_eq!(
            first.invalidations,
            [ChangeInvalidation::Vertex {
                key: "rust:cdc-real:a".into(),
                current: None
            }]
        );
        let cursor = first.cursor.ok_or("final opaque cursor missing")?;
        drop(stream);
        client
            .put_vertex(VertexInput::nil("rust:cdc-real:b"))
            .await?;
        let mut resumed = client
            .watch_changes(
                options(false, Some(cursor.clone())),
                StreamOptions::default().with_idle(Duration::from_secs(5)),
            )
            .await?;
        let second = resumed.next_frame().await?;
        assert_eq!(
            second.invalidations,
            [ChangeInvalidation::Vertex {
                key: "rust:cdc-real:b".into(),
                current: None
            }]
        );
        assert!(second.cursor.is_some());
        drop(resumed);
        for index in 0..8 {
            client
                .put_vertex(VertexInput::int32(
                    format!("rust:cdc-real:fill-{index}"),
                    index,
                ))
                .await?;
        }
        let rejected = match client
            .watch_changes(options(false, Some(cursor)), StreamOptions::default())
            .await
        {
            Ok(mut stream) => stream
                .next_frame()
                .await
                .expect_err("retained gap must reject"),
            Err(error) => error,
        };
        assert!(matches!(rejected, LanternError::CdcGap(_)), "{rejected:?}");
        Ok(())
    }
    #[tokio::test]
    #[ignore = "requires a built production Go Server binary"]
    async fn real_wire_dropped_public_stream_releases_registered_subscriber()
    -> Result<(), Box<dyn std::error::Error>> {
        use crate::test_server::GoServer;
        use std::{
            io::{Read, Write},
            net::{TcpListener, TcpStream},
            time::Duration,
        };
        fn active(addr: &str) -> Result<u64, Box<dyn std::error::Error>> {
            let mut socket = TcpStream::connect(addr)?;
            socket.set_read_timeout(Some(Duration::from_secs(2)))?;
            socket.write_all(
                b"GET /metrics HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
            )?;
            let mut response = String::new();
            socket.read_to_string(&mut response)?;
            let row = response
                .lines()
                .find(|line| line.starts_with("lantern_changes_active_streams "))
                .ok_or("public changes gauge missing")?;
            Ok(row
                .split_whitespace()
                .nth(1)
                .ok_or("empty gauge")?
                .parse()?)
        }
        let listener = TcpListener::bind("127.0.0.1:0")?;
        let metrics = listener.local_addr()?.to_string();
        drop(listener);
        let mut server = GoServer::start(&[("LANTERN_METRICS_ADDR", &metrics)])?;
        server.wait_for_listener()?;
        let client = LanternClient::builder(format!("http://127.0.0.1:{}", server.port()))
            .connect()
            .await?;
        assert_eq!(active(&metrics)?, 0);
        let mut stream = client
            .watch_changes(
                WatchChangesOptions {
                    bootstrap: true,
                    ..Default::default()
                },
                StreamOptions::default(),
            )
            .await?;
        assert!(stream.next_frame().await?.bootstrap);
        assert_eq!(active(&metrics)?, 1);
        drop(stream);
        let mut released = false;
        for _ in 0..40 {
            if active(&metrics)? == 0 {
                released = true;
                break;
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
        assert!(
            released,
            "public CDC drop did not cancel registered subscriber"
        );
        Ok(())
    }
}
