use std::{
    io::{Read, Write},
    net::{TcpListener, TcpStream},
    str::FromStr,
    time::Duration,
};

use prost::Message;

use super::*;
use crate::{
    VertexInput,
    generated::graph::v1::{
        Mutation, MutationOp, SubscribeResponse, mutation_op::Op, subscribe_response::Event,
    },
    test_server::GoServer,
};

fn origin() -> OriginId {
    OriginId::new([0x42; 16]).unwrap()
}

#[test]
fn origin_and_portable_cursor_are_canonical_and_next_expected() {
    let id = origin();
    assert_eq!(id.to_hex(), "42424242424242424242424242424242");
    assert_eq!(id.to_hex().parse::<OriginId>().unwrap(), id);
    for text in [
        "",
        "4242424242424242424242424242424",
        "4242424242424242424242424242424F",
        "00000000000000000000000000000000",
    ] {
        assert!(OriginId::from_str(text).is_err(), "{text}");
    }
    let mut cursor = CdcCursor::from_hex_map([(id.to_hex(), 4)]).unwrap();
    assert_eq!(cursor.next_expected(id), 4);
    assert_eq!(cursor.complete(id, 4).unwrap().next_expected(id), 5);
    assert_eq!(cursor.next_expected(id), 5);
    assert!(matches!(
        cursor.complete(id, 4),
        Err(LanternError::CdcGap(_))
    ));
    assert!(CdcCursor::from_hex_map([(id.to_hex(), 0)]).is_err());
    assert!(CdcCursor::from_hex_map([(id.to_hex(), 1), (id.to_hex(), 2)]).is_err());
    assert_eq!(
        CdcCursor::from_hex_map(cursor.to_hex_map()).unwrap(),
        cursor
    );
}

#[test]
fn unknown_or_malformed_frames_fail_closed_before_cursor_advances() {
    let known = SubscribeResponse {
        event: Some(Event::Mutation(Mutation {
            seq: 1,
            origin: origin().as_bytes().to_vec(),
            hlc: None,
            op: Some(MutationOp {
                op: Some(Op::DeleteVertex(Default::default())),
            }),
            tombstone_expiration: None,
        })),
    };
    let mut payload = known.encode_to_vec();
    assert!(decode_frame(&payload).is_ok());
    // A future oneof arm on the parent, not silently discarded by Prost.
    payload.extend_from_slice(&[0xa2, 0x06, 0x00]);
    assert!(matches!(
        decode_frame(&payload),
        Err(LanternError::CdcGap(_))
    ));
    // Future MutationOp arm nested inside a known full-Mutation frame.
    let mut mutation = if let Some(Event::Mutation(mutation)) = known.event {
        mutation
    } else {
        unreachable!()
    };
    mutation.op = None;
    let mut raw_mutation = mutation.encode_to_vec();
    let unknown_op = [0xa2, 0x06, 0x00];
    prost::encoding::encode_key(
        4,
        prost::encoding::WireType::LengthDelimited,
        &mut raw_mutation,
    );
    prost::encoding::encode_varint(unknown_op.len() as u64, &mut raw_mutation);
    raw_mutation.extend_from_slice(&unknown_op);
    let mut nested = Vec::new();
    prost::encoding::encode_key(1, prost::encoding::WireType::LengthDelimited, &mut nested);
    prost::encoding::encode_varint(raw_mutation.len() as u64, &mut nested);
    nested.extend_from_slice(&raw_mutation);
    assert!(matches!(
        decode_frame(&nested),
        Err(LanternError::CdcGap(_))
    ));
    assert!(matches!(
        decode_frame(&[0x0a, 0x05, 0xff]),
        Err(LanternError::CdcGap(_))
    ));
    assert!(matches!(
        decode_frame(&[]),
        Ok(SubscribeResponse { event: None })
    ));
}

#[test]
fn explicit_gap_keeps_original_failed_precondition_status() {
    for cause in [
        "gapped: retained history evicted",
        "publication generation changed after Snapshot",
    ] {
        let status = tonic::Status::failed_precondition(cause);
        let LanternError::CdcGap(gap) = cdc_rpc_error(LanternError::from(status)) else {
            panic!("expected a typed gap");
        };
        let failure = gap.failure().expect("original server status");
        assert_eq!(failure.status().code(), tonic::Code::FailedPrecondition);
        assert_eq!(failure.status().message(), cause);
    }
    let unauthorized = tonic::Status::unauthenticated("token invalid");
    assert!(matches!(
        cdc_rpc_error(LanternError::from(unauthorized)),
        LanternError::Rpc(_)
    ));
}

#[test]
fn clean_eof_without_a_cdc_terminal_boundary_is_a_gap() {
    for reason in [
        "identity stream ended without a terminal boundary",
        "full-mutation stream ended unexpectedly",
    ] {
        let LanternError::CdcGap(gap) = require_frame(None, reason).unwrap_err() else {
            panic!("an unexpected clean EOF must not advance or complete a cursor");
        };
        assert_eq!(gap.reason(), reason);
        assert!(gap.failure().is_none());
    }
}

#[tokio::test]
#[ignore = "requires a built production Go server via LANTERN_RUST_TEST_SERVER"]
async fn real_wire_identity_full_bootstrap_resume_and_bounded_gap()
-> Result<(), Box<dyn std::error::Error>> {
    let mut server = GoServer::start(&[
        ("LANTERN_NODE_ID", "42424242424242424242424242424242"),
        ("LANTERN_MUTATION_LOG_CAPACITY", "4"),
    ])?;
    server.wait_for_listener()?;
    let client = LanternClient::builder(format!("http://127.0.0.1:{}", server.port()))
        .unary_timeout(Duration::from_millis(300))?
        .connect()
        .await?;

    let mut identity = client
        .bootstrap_identities(StreamOptions::default().with_idle(Duration::from_secs(3)))
        .await?;
    let checkpoint = match identity.next_event().await? {
        IdentityEvent::Checkpoint(checkpoint) => checkpoint,
        other => panic!("bootstrap must begin with a checkpoint, got {other:?}"),
    };
    assert_eq!(
        checkpoint
            .cursor_after_revalidation()?
            .next_expected(origin()),
        1
    );
    let prefix = "rust:cdc-real:";
    client
        .put_vertex(VertexInput::int64(format!("{prefix}a"), 52))
        .await?;
    let first = match identity.next_event().await? {
        IdentityEvent::Chunk(chunk) => chunk,
        other => panic!("expected a mutation fragment, got {other:?}"),
    };
    assert_eq!(first.origin, origin());
    assert_eq!(first.category, IdentityCategory::PutVertex);
    assert_eq!(first.vertex_keys, [format!("{prefix}a")]);
    assert!(first.edge_keys.is_empty());
    let next = first.next_cursor.unwrap();
    drop(identity);

    client
        .put_vertex(VertexInput::nil(format!("{prefix}b")))
        .await?;
    let mut full = tokio::time::timeout(
        Duration::from_secs(5),
        client.subscribe_full_mutations(
            next.clone(),
            StreamOptions::default().with_idle(Duration::from_secs(3)),
        ),
    )
    .await??;
    let received = full.next_mutation().await?;
    assert_eq!(received.origin, origin());
    assert!(matches!(
        received.op,
        FullMutationOp::ReplicatedPutVertices(ref vertices)
            if matches!(vertices.as_slice(), [VertexWrite::Live(v)] if v.key == format!("{prefix}b"))
    ));
    drop(full);

    client
        .put_vertices([
            VertexInput::nil(format!("{prefix}tail")),
            VertexInput::nil(format!("{prefix}head")),
        ])
        .await?;
    let mut resume = tokio::time::timeout(
        Duration::from_secs(5),
        client.resume_identities(
            received.next_cursor,
            StreamOptions::default().with_idle(Duration::from_secs(3)),
        ),
    )
    .await??;
    let chunk = match resume.next_event().await? {
        IdentityEvent::Chunk(chunk) => chunk,
        other => panic!("resume must deliver a chunk, got {other:?}"),
    };
    assert_eq!(chunk.vertex_keys.len(), 2);
    assert!(chunk.next_cursor.is_some());
    drop(resume);

    for index in 0..8 {
        client
            .put_vertex(VertexInput::int32(format!("{prefix}fill-{index}"), index))
            .await?;
    }
    let gap = match client
        .resume_identities(
            next,
            StreamOptions::default().with_idle(Duration::from_secs(3)),
        )
        .await
    {
        Ok(mut stream) => stream
            .next_event()
            .await
            .expect_err("evicted retained log must be a gap"),
        Err(error) => error,
    };
    assert!(matches!(gap, LanternError::CdcGap(_)));
    Ok(())
}

fn active_subscribers(addr: &str) -> Result<u64, Box<dyn std::error::Error>> {
    let mut socket = TcpStream::connect(addr)?;
    socket.set_read_timeout(Some(Duration::from_secs(2)))?;
    socket.write_all(b"GET /metrics HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")?;
    let mut response = String::new();
    socket.read_to_string(&mut response)?;
    let row = response
        .lines()
        .find(|line| line.starts_with("lantern_subscribe_active_streams "))
        .ok_or("missing active Subscribe gauge")?;
    Ok(row
        .split_whitespace()
        .nth(1)
        .ok_or("empty active Subscribe gauge")?
        .parse()?)
}

#[tokio::test]
#[ignore = "requires a built production Go server via LANTERN_RUST_TEST_SERVER"]
async fn real_wire_dropped_subscription_releases_server_subscriber()
-> Result<(), Box<dyn std::error::Error>> {
    let listener = TcpListener::bind("127.0.0.1:0")?;
    let metrics = listener.local_addr()?.to_string();
    drop(listener);
    let mut server = GoServer::start(&[("LANTERN_METRICS_ADDR", &metrics)])?;
    server.wait_for_listener()?;
    let client = LanternClient::builder(format!("http://127.0.0.1:{}", server.port()))
        .connect()
        .await?;
    let mut stream = client
        .bootstrap_identities(StreamOptions::default().with_idle(Duration::from_secs(5)))
        .await?;
    assert_eq!(
        active_subscribers(&metrics)?,
        0,
        "unpolled handles do not subscribe"
    );
    assert!(matches!(
        stream.next_event().await?,
        IdentityEvent::Checkpoint(_)
    ));
    let mut active = false;
    for _ in 0..40 {
        if active_subscribers(&metrics)? == 1 {
            active = true;
            break;
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    assert!(
        active,
        "Subscribe did not register an active server subscriber"
    );
    drop(stream);
    let mut released = false;
    for _ in 0..40 {
        if active_subscribers(&metrics)? == 0 {
            released = true;
            break;
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    assert!(
        released,
        "dropping the Rust stream did not cancel server Subscribe"
    );
    Ok(())
}
