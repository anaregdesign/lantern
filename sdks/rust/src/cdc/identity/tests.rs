use super::*;
use crate::generated::graph::v1::{EdgeKey, HlcTimestamp, SubscribeResponse};

fn origin() -> OriginId {
    OriginId::new([0x53; 16]).unwrap()
}

fn make_stream(bootstrap: bool) -> IdentityStream {
    IdentityStream {
        wire: None,
        cursor: CdcCursor::new(),
        waiting_for_checkpoint: bootstrap,
        pending: None,
    }
}

fn frame(chunk: WireChunk) -> SubscribeResponse {
    SubscribeResponse {
        event: Some(Event::IdentityChunk(chunk)),
    }
}

fn chunk() -> WireChunk {
    WireChunk {
        origin: origin().as_bytes().to_vec(),
        seq: 1,
        hlc: Some(HlcTimestamp {
            wall_ns: 9,
            logical: 2,
            node_id: origin().as_bytes().to_vec(),
        }),
        operation: IdentityOperation::PutVertex as i32,
        chunk_index: 0,
        is_last: true,
        vertex_keys: vec!["rust:a".into()],
        edge_keys: vec![],
        first_item_index: 0,
    }
}

#[test]
fn bootstrap_is_checkpoint_first_and_initializes_per_origin_cut() {
    let mut stream = make_stream(true);
    assert!(matches!(
        stream.process_frame(frame(chunk())),
        Err(LanternError::CdcGap(_))
    ));
    let mut cut = BTreeMap::new();
    cut.insert(origin().to_hex(), 4);
    let checkpoint = SubscribeResponse {
        event: Some(Event::Checkpoint(WireCheckpoint {
            last_seq_per_origin: cut.into_iter().collect(),
        })),
    };
    let IdentityEvent::Checkpoint(checkpoint) = stream.process_frame(checkpoint).unwrap() else {
        panic!("expected checkpoint");
    };
    assert_eq!(
        checkpoint
            .cursor_after_revalidation()
            .unwrap()
            .next_expected(origin()),
        5
    );
    assert_eq!(stream.cursor.next_expected(origin()), 5);
    let mut next = chunk();
    next.seq = 5;
    let IdentityEvent::Chunk(chunk) = stream.process_frame(frame(next)).unwrap() else {
        panic!("expected first live mutation");
    };
    assert_eq!(chunk.next_cursor.unwrap().next_expected(origin()), 6);
    assert!(matches!(
        stream.process_frame(SubscribeResponse {
            event: Some(Event::Checkpoint(WireCheckpoint::default())),
        }),
        Err(LanternError::CdcGap(_))
    ));
}

#[test]
fn final_only_cursor_including_empty_receipt_and_category_seven() {
    let mut stream = make_stream(false);
    let mut first = chunk();
    first.is_last = false;
    assert!(matches!(
        stream.process_frame(frame(first)).unwrap(),
        IdentityEvent::Chunk(IdentityChunk {
            next_cursor: None,
            ..
        })
    ));
    assert_eq!(stream.cursor.next_expected(origin()), 1);
    let mut last = chunk();
    last.chunk_index = 1;
    last.first_item_index = 1;
    last.vertex_keys.clear();
    let IdentityEvent::Chunk(completed) = stream.process_frame(frame(last)).unwrap() else {
        panic!("expected final empty fragment");
    };
    assert!(completed.is_last && completed.vertex_keys.is_empty());
    assert_eq!(completed.next_cursor.unwrap().next_expected(origin()), 2);

    let mut contribution = chunk();
    contribution.seq = 2;
    contribution.operation = IdentityOperation::DeleteEdgeContribution as i32;
    contribution.vertex_keys.clear();
    contribution.edge_keys = vec![EdgeKey {
        tail: "rust:tail".into(),
        head: "rust:head".into(),
    }];
    let IdentityEvent::Chunk(event) = stream.process_frame(frame(contribution)).unwrap() else {
        panic!("expected contribution invalidation");
    };
    assert_eq!(event.category, IdentityCategory::DeleteEdgeContribution);
    assert_eq!(event.edge_keys, [EdgeRef::new("rust:tail", "rust:head")]);
    assert_eq!(event.next_cursor.unwrap().next_expected(origin()), 3);

    let mut receipt = chunk();
    receipt.seq = 3;
    receipt.operation = IdentityOperation::ReceiptOnly as i32;
    receipt.vertex_keys.clear();
    let IdentityEvent::Chunk(event) = stream.process_frame(frame(receipt)).unwrap() else {
        panic!("expected zero-key receipt event");
    };
    assert_eq!(event.category, IdentityCategory::ReceiptOnly);
    assert!(event.vertex_keys.is_empty() && event.edge_keys.is_empty());
    assert_eq!(event.next_cursor.unwrap().next_expected(origin()), 4);
}

#[test]
fn mismatched_chunks_and_unknown_categories_fail_without_advancing_cursor() {
    let mut first = chunk();
    first.is_last = false;
    let mut stream = make_stream(false);
    stream.process_frame(frame(first.clone())).unwrap();
    let mut invalid = first.clone();
    invalid.chunk_index = 2;
    invalid.first_item_index = 1;
    assert!(matches!(
        stream.process_frame(frame(invalid)),
        Err(LanternError::CdcGap(_))
    ));
    assert_eq!(stream.cursor.next_expected(origin()), 1);
    for invalid in [
        {
            let mut c = chunk();
            c.operation = 0;
            c
        },
        {
            let mut c = chunk();
            c.operation = 8;
            c
        },
        {
            let mut c = chunk();
            c.seq = 2;
            c
        },
        {
            let mut c = chunk();
            c.chunk_index = 1;
            c
        },
        {
            let mut c = chunk();
            c.first_item_index = 1;
            c
        },
        {
            let mut c = chunk();
            c.vertex_keys = vec!["rust:a".into(); 1_025];
            c
        },
        {
            let mut c = chunk();
            c.hlc.as_mut().unwrap().node_id = [0x71; 16].to_vec();
            c
        },
        {
            let mut c = chunk();
            c.vertex_keys = vec!["".into()];
            c
        },
    ] {
        let mut stream = make_stream(false);
        assert!(
            matches!(
                stream.process_frame(frame(invalid)),
                Err(LanternError::CdcGap(_))
            ),
            "invalid identity fragment should fail closed"
        );
        assert_eq!(stream.cursor.next_expected(origin()), 1);
    }
    let mut nonfinal_empty = chunk();
    nonfinal_empty.vertex_keys.clear();
    nonfinal_empty.is_last = false;
    assert!(matches!(
        make_stream(false).process_frame(frame(nonfinal_empty)),
        Err(LanternError::CdcGap(_))
    ));
    let mut receipt_with_value = chunk();
    receipt_with_value.operation = IdentityOperation::ReceiptOnly as i32;
    assert!(matches!(
        make_stream(false).process_frame(frame(receipt_with_value)),
        Err(LanternError::CdcGap(_))
    ));
}
