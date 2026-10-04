use std::str::FromStr;

use prost::Message;

use super::*;
use crate::generated::graph::v1::{
    Mutation, MutationOp, SubscribeResponse, mutation_op::Op, subscribe_response::Event,
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
            namespace_format: String::new(),
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
