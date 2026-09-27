use crate::generated::graph::v1::GetVerticesRequest;

use super::*;

#[test]
fn planning_respects_count_and_exact_encoded_bytes() {
    let keys = ["a".repeat(2), "b".repeat(20), "c".repeat(2)];
    let make = |range: Range<usize>| GetVerticesRequest {
        keys: keys[range].to_vec(),
    };
    let ranges = chunk_plan(keys.len(), 2, make(0..2).encoded_len() - 1, make).unwrap();
    assert_eq!(ranges, vec![0..1, 1..2, 2..3]);
    assert!(
        ranges
            .iter()
            .all(|range| make(range.clone()).encoded_len() < make(0..2).encoded_len())
    );
    let max = make(1..2).encoded_len() - 1;
    assert!(matches!(
        chunk_plan(keys.len(), 1, max, make),
        Err(LanternError::MessageTooLarge { actual, limit }) if actual == max + 1 && limit == max
    ));
    assert_eq!(chunk_plan(0, 1, 1, make).unwrap(), Vec::new());
    assert!(matches!(
        chunk_plan(65_537, 1, 100, |_| GetVerticesRequest { keys: vec![] }),
        Err(LanternError::InvalidInput(_))
    ));
}
