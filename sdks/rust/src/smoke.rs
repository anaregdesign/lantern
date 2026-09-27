use std::{
    error::Error,
    time::{Duration, Instant},
};

use prost::Message;
use tonic::{Code, Request, transport::Endpoint};
use tonic_health::pb::{
    HealthCheckRequest, health_check_response::ServingStatus, health_client::HealthClient,
};

use crate::{
    Timestamp, Vertex, VertexValue,
    generated::graph::v1::{
        GetServerStatusRequest, GetVertexRequest, lantern_service_client::LanternServiceClient,
    },
    test_server::GoServer,
};

const MAX_MESSAGE_BYTES: usize = 16 * 1024 * 1024;

#[test]
fn domain_types_round_trip_without_public_rpc_envelopes() -> Result<(), Box<dyn Error>> {
    let vertex = Vertex {
        key: "rust:smoke".into(),
        expiration: Some(Timestamp {
            seconds: -1,
            nanos: 1,
        }),
        value: Some(VertexValue::Nil(true)),
    };
    let encoded = vertex.encode_to_vec();
    assert_eq!(Vertex::decode(encoded.as_slice())?, vertex);
    Ok(())
}

#[test]
fn native_tls_roots_are_available_by_default() -> Result<(), Box<dyn Error>> {
    let _endpoint = Endpoint::from_static("https://localhost")
        .tls_config(tonic::transport::ClientTlsConfig::new().with_native_roots())?;
    Ok(())
}

#[cfg(feature = "bundled-roots")]
#[test]
fn bundled_roots_require_an_explicit_trust_choice() -> Result<(), Box<dyn Error>> {
    let _endpoint = Endpoint::from_static("https://localhost")
        .tls_config(tonic::transport::ClientTlsConfig::new().with_webpki_roots())?;
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_health_and_unary() -> Result<(), Box<dyn Error>> {
    let mut server = GoServer::start(&[])?;
    let port = server.port();
    let endpoint = Endpoint::from_shared(format!("http://127.0.0.1:{port}"))?
        .connect_timeout(Duration::from_secs(1));
    let deadline = Instant::now() + Duration::from_secs(15);
    let channel = loop {
        if let Some(status) = server.try_wait()? {
            return Err(
                format!("production Lantern server exited before readiness: {status}").into(),
            );
        }

        let observation = match endpoint.connect().await {
            Ok(channel) => {
                let mut health = HealthClient::new(channel.clone())
                    .max_decoding_message_size(MAX_MESSAGE_BYTES)
                    .max_encoding_message_size(MAX_MESSAGE_BYTES);
                let mut request = Request::new(HealthCheckRequest {
                    service: "graph.v1.LanternService".into(),
                });
                request.set_timeout(Duration::from_secs(1));
                match health.check(request).await {
                    Ok(response) if response.get_ref().status == ServingStatus::Serving as i32 => {
                        break channel;
                    }
                    Ok(response)
                        if response.get_ref().status == ServingStatus::NotServing as i32
                            || response.get_ref().status == ServingStatus::Unknown as i32 =>
                    {
                        format!("Health status {}", response.get_ref().status)
                    }
                    Ok(response) => {
                        return Err(format!(
                            "unexpected gRPC Health status: {}",
                            response.get_ref().status
                        )
                        .into());
                    }
                    Err(status) if status.code() == Code::Unavailable => status.to_string(),
                    Err(status) => return Err(status.into()),
                }
            }
            Err(error) => error.to_string(),
        };
        if Instant::now() >= deadline {
            return Err(format!(
                "production Lantern gRPC Health did not become SERVING in 15s: {observation}"
            )
            .into());
        }
        tokio::time::sleep(Duration::from_millis(100)).await;
    };

    let mut client = LanternServiceClient::new(channel)
        .max_decoding_message_size(MAX_MESSAGE_BYTES)
        .max_encoding_message_size(MAX_MESSAGE_BYTES);
    let status = client
        .get_server_status(GetServerStatusRequest {})
        .await?
        .into_inner();
    assert!(
        !status.go_version.is_empty(),
        "missing production Go version"
    );

    let missing = client
        .get_vertex(GetVertexRequest {
            key: format!("rust:smoke:missing:{port}"),
        })
        .await
        .expect_err("a fresh server must report a missing vertex");
    assert_eq!(missing.code(), Code::NotFound);
    Ok(())
}
