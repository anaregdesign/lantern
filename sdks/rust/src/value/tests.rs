use super::*;
use crate::VertexKind;

#[test]
fn exact_oneof_kinds_and_accessors_are_not_coerced() {
    let values = [
        (VertexInput::int32("a", -3), VertexKind::Int32),
        (VertexInput::int64("a", i64::MIN), VertexKind::Int64),
        (VertexInput::uint32("a", u32::MAX), VertexKind::Uint32),
        (VertexInput::uint64("a", u64::MAX), VertexKind::Uint64),
        (VertexInput::float32("a", -1.25), VertexKind::Float32),
        (VertexInput::float64("a", 2.75), VertexKind::Float64),
        (VertexInput::boolean("a", false), VertexKind::Bool),
        (VertexInput::string("a", "🍂"), VertexKind::String),
        (VertexInput::bytes("a", [0, 255]), VertexKind::Bytes),
        (
            VertexInput::timestamp(
                "a",
                Timestamp {
                    seconds: -1,
                    nanos: 123_456_789,
                },
            )
            .unwrap(),
            VertexKind::Timestamp,
        ),
        (
            VertexInput::duration(
                "a",
                ProtoDuration {
                    seconds: -1,
                    nanos: -1,
                },
            )
            .unwrap(),
            VertexKind::Duration,
        ),
        (VertexInput::nil("a"), VertexKind::Nil),
        (VertexInput::unset("a"), VertexKind::Unset),
    ];
    for (input, kind) in values {
        let vertex = input.into_wire(UNIX_EPOCH).unwrap();
        assert_eq!(vertex.kind(), kind);
        vertex.validate_response().unwrap();
        assert_eq!(vertex.is_nil(), kind == VertexKind::Nil);
        assert_eq!(vertex.is_unset(), kind == VertexKind::Unset);
    }
    assert_eq!(
        VertexInput::int32("a", -3)
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .int32_value(),
        Some(-3)
    );
    assert_eq!(
        VertexInput::int64("a", i64::MIN)
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .int64_value(),
        Some(i64::MIN)
    );
    assert_eq!(
        VertexInput::uint32("a", u32::MAX)
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .uint32_value(),
        Some(u32::MAX)
    );
    assert_eq!(
        VertexInput::uint64("a", u64::MAX)
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .uint64_value(),
        Some(u64::MAX)
    );
    assert_eq!(
        VertexInput::float32("a", -1.25)
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .float32_value(),
        Some(-1.25)
    );
    assert_eq!(
        VertexInput::float64("a", 2.75)
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .float64_value(),
        Some(2.75)
    );
    assert_eq!(
        VertexInput::boolean("a", false)
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .bool_value(),
        Some(false)
    );
    assert_eq!(
        VertexInput::string("a", "foo")
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .string_value(),
        Some("foo")
    );
    assert_eq!(
        VertexInput::bytes("a", [0, 255])
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .bytes_value(),
        Some(&[0, 255][..])
    );
    assert_eq!(
        VertexInput::timestamp(
            "a",
            Timestamp {
                seconds: -1,
                nanos: 12
            }
        )
        .unwrap()
        .into_wire(UNIX_EPOCH)
        .unwrap()
        .timestamp_value()
        .unwrap()
        .nanos,
        12
    );
    assert_eq!(
        VertexInput::duration(
            "a",
            ProtoDuration {
                seconds: -1,
                nanos: -12
            }
        )
        .unwrap()
        .into_wire(UNIX_EPOCH)
        .unwrap()
        .duration_value()
        .unwrap()
        .nanos,
        -12
    );
}

#[test]
fn timestamps_validate_full_proto_range_and_distinguish_zero_time() {
    let at = |seconds, nanos| Expiration::At(Timestamp { seconds, nanos });
    for (seconds, nanos) in [
        (TIMESTAMP_MIN, 1),
        (-1, 0),
        (0, 0),
        (0, 500_000_000),
        (TIMESTAMP_MAX, 999_999_999),
    ] {
        assert!(resolve_expiration(&at(seconds, nanos), UNIX_EPOCH).is_ok());
    }
    let one_ns_before_epoch = Timestamp {
        seconds: -1,
        nanos: 999_999_999,
    };
    assert_eq!(
        resolve_expiration(&Expiration::At(one_ns_before_epoch), UNIX_EPOCH).unwrap(),
        Some(one_ns_before_epoch)
    );
    assert_eq!(
        VertexInput::timestamp("precise", one_ns_before_epoch)
            .unwrap()
            .into_wire(UNIX_EPOCH)
            .unwrap()
            .timestamp_value(),
        Some(&one_ns_before_epoch)
    );
    assert!(matches!(
        resolve_expiration(&at(TIMESTAMP_MIN, 0), UNIX_EPOCH),
        Err(LanternError::InvalidInput(_))
    ));
    for (seconds, nanos) in [
        (TIMESTAMP_MIN - 1, 0),
        (TIMESTAMP_MAX + 1, 0),
        (0, -1),
        (0, 1_000_000_000),
    ] {
        assert!(matches!(
            resolve_expiration(&at(seconds, nanos), UNIX_EPOCH),
            Err(LanternError::InvalidInput(_))
        ));
    }
    assert!(
        VertexInput::timestamp(
            "year-one-value",
            Timestamp {
                seconds: TIMESTAMP_MIN,
                nanos: 0,
            }
        )
        .is_ok()
    );
    assert!(matches!(
        VertexInput::timestamp(
            "invalid",
            Timestamp {
                seconds: TIMESTAMP_MAX + 1,
                nanos: 0
            }
        ),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        Vertex {
            expiration: Some(Timestamp {
                seconds: TIMESTAMP_MIN,
                nanos: 0
            }),
            ..VertexInput::nil("v").into_wire(UNIX_EPOCH).unwrap()
        }
        .validate_response(),
        Err(LanternError::Protocol(_))
    ));
    assert_eq!(
        system_time_timestamp(UNIX_EPOCH - Duration::from_nanos(100)).unwrap(),
        Timestamp {
            seconds: -1,
            nanos: 999_999_900
        }
    );
}

#[test]
fn signed_duration_and_positive_ttl_have_independent_validations() {
    for duration in [
        ProtoDuration {
            seconds: -DURATION_MAX,
            nanos: -999_999_999,
        },
        ProtoDuration {
            seconds: 0,
            nanos: -1,
        },
        ProtoDuration {
            seconds: DURATION_MAX,
            nanos: 999_999_999,
        },
    ] {
        VertexInput::duration("valid", duration).unwrap();
    }
    for duration in [
        ProtoDuration {
            seconds: 1,
            nanos: -1,
        },
        ProtoDuration {
            seconds: -1,
            nanos: 1,
        },
        ProtoDuration {
            seconds: 0,
            nanos: 1_000_000_000,
        },
        ProtoDuration {
            seconds: DURATION_MAX + 1,
            nanos: 0,
        },
    ] {
        assert!(matches!(
            VertexInput::duration("invalid", duration),
            Err(LanternError::InvalidInput(_))
        ));
    }
    for duration in [
        ProtoDuration {
            seconds: 0,
            nanos: -1,
        },
        ProtoDuration {
            seconds: 0,
            nanos: 0,
        },
        ProtoDuration {
            seconds: -1,
            nanos: 0,
        },
    ] {
        assert!(matches!(
            Expiration::after_proto(duration),
            Err(LanternError::InvalidInput(_))
        ));
    }
    assert_eq!(
        Expiration::after_proto(ProtoDuration {
            seconds: 0,
            nanos: 17
        })
        .unwrap(),
        Expiration::After(Duration::from_nanos(17))
    );
    assert!(matches!(
        resolve_expiration(&Expiration::After(Duration::ZERO), UNIX_EPOCH),
        Err(LanternError::InvalidInput(_))
    ));
    let after = Expiration::After(Duration::from_nanos(1));
    assert_eq!(
        resolve_expiration(&after, UNIX_EPOCH).unwrap(),
        Some(Timestamp {
            seconds: 0,
            nanos: 1
        })
    );
    assert_eq!(
        resolve_expiration(
            &Expiration::After(Duration::from_nanos(101)),
            UNIX_EPOCH - Duration::from_nanos(100)
        )
        .unwrap(),
        Some(Timestamp {
            seconds: 0,
            nanos: 1
        })
    );
    assert_eq!(
        resolve_expiration(
            &Expiration::After(Duration::from_nanos(101)),
            UNIX_EPOCH + Duration::from_nanos(999_999_900)
        )
        .unwrap(),
        Some(Timestamp {
            seconds: 1,
            nanos: 1
        })
    );
    assert!(matches!(
        resolve_expiration(&Expiration::After(Duration::MAX), UNIX_EPOCH),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        resolve_expiration(
            &Expiration::After(Duration::from_secs(1)),
            UNIX_EPOCH + Duration::from_secs(TIMESTAMP_MAX as u64)
        ),
        Err(LanternError::InvalidInput(_))
    ));
}

#[test]
fn malformed_values_and_nonfinite_edge_sources_fail_closed() {
    assert!(matches!(
        VertexInput::new("a", Some(VertexValue::Nil(false))).into_wire(UNIX_EPOCH),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        VertexInput::nil("").into_wire(UNIX_EPOCH),
        Err(LanternError::InvalidInput(_))
    ));
    for weight in [f32::NAN, f32::INFINITY, f32::NEG_INFINITY] {
        assert!(matches!(
            EdgeInput::new("tail", "head", weight).into_wire(UNIX_EPOCH),
            Err(LanternError::InvalidInput(_))
        ));
    }
    assert!(matches!(
        EdgeInput::new("", "head", 1.0).into_wire(UNIX_EPOCH),
        Err(LanternError::InvalidInput(_))
    ));
    assert!(matches!(
        EdgeRef::new("tail", "").validate(),
        Err(LanternError::InvalidInput(_))
    ));
}
