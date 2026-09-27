use std::sync::Arc;

use super::*;

fn generator(nonce: [u8; 16], sequence: u64) -> ContribIdGenerator {
    ContribIdGenerator {
        nonce,
        sequence: AtomicU64::new(sequence),
    }
}

#[test]
fn exact_go_node_golden_vectors_and_clone_shared_sequence() {
    let shared = Arc::new(generator(
        [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15],
        0,
    ));
    let ids = shared.next_ids(&[0, 1]).unwrap();
    assert_eq!(
        ids[0].as_bytes(),
        &[
            0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 0, 0, 0, 0, 0, 1, 0, 0
        ]
    );
    assert_eq!(
        ids[1].as_bytes(),
        &[
            0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 0, 0, 0, 0, 0, 1, 0, 1
        ]
    );
    let next = shared.clone().next_ids(&[0]).unwrap();
    assert_eq!(&next[0].as_bytes()[16..], &[0, 0, 0, 0, 0, 2, 0, 0]);

    let tail = generator([0; 16], 0xabcc).next_ids(&[0xffff]).unwrap();
    assert_eq!(
        &tail[0].as_bytes()[16..],
        &[0, 0, 0, 0, 0xab, 0xcd, 0xff, 0xff]
    );
}

#[test]
fn reject_bad_ids_and_exhaustion_without_wrap_or_reuse() {
    assert!(matches!(
        ContribId::try_from(&[1; 23][..]),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        ContribId::new([0; 24]),
        Err(LanternError::InvalidInput(_))
    ));
    let source = generator([7; 16], MAX_SEQUENCE - 1);
    assert_eq!(
        &source.next_ids(&[0]).unwrap()[0].as_bytes()[16..],
        &[0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0]
    );
    assert!(matches!(
        source.next_ids(&[0]),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        source.next_ids(&[MAX_LOGICAL_ITEMS]),
        Err(LanternError::InvalidInput(_))
    ));
}
