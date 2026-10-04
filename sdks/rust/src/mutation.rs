use crate::{
    LanternError,
    generated::graph::v1::{MutationAcceptance, MutationAcceptanceKind},
};

/// A disclosed effect or an explicit acknowledgement without effect details.
/// AcceptedUndisclosed never implies a change, absence, or permission to retry.
#[derive(Clone, Debug, PartialEq)]
pub enum MutationReply<T> {
    KnownEffect(T),
    AcceptedUndisclosed,
}

impl<T> MutationReply<T> {
    /// Adapt a complete logical mutation. A real or partial batch failure is
    /// preserved; only the direct acknowledgement becomes a successful reply.
    pub fn from_result(result: Result<T, LanternError>) -> Result<Self, LanternError> {
        match result {
            Ok(effect) => Ok(Self::KnownEffect(effect)),
            Err(LanternError::MutationAcceptedUndisclosed) => Ok(Self::AcceptedUndisclosed),
            Err(error) => Err(error),
        }
    }
}

pub(crate) fn accepted_undisclosed(
    acceptance: Option<&MutationAcceptance>,
    has_effect: bool,
) -> Result<bool, LanternError> {
    let Some(acceptance) = acceptance else {
        return Ok(false);
    };
    if acceptance.kind != MutationAcceptanceKind::HandledEffectUndisclosed as i32 || has_effect {
        return Err(LanternError::Protocol(
            "unknown or mixed mutation acceptance",
        ));
    }
    Ok(true)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::BatchError;
    use std::error::Error;

    #[test]
    fn reply_preserves_real_and_partial_failures() {
        assert_eq!(
            MutationReply::from_result(Ok(17)).unwrap(),
            MutationReply::KnownEffect(17)
        );
        assert_eq!(
            MutationReply::<bool>::from_result(Err(LanternError::MutationAcceptedUndisclosed))
                .unwrap(),
            MutationReply::AcceptedUndisclosed
        );
        assert!(matches!(
            MutationReply::<bool>::from_result(Err(LanternError::Batch(BatchError {
                completed_items: 2,
                source: Box::new(LanternError::MutationAcceptedUndisclosed),
            }))),
            Err(LanternError::Batch(_))
        ));
        assert!(matches!(
            MutationReply::<bool>::from_result(Err(LanternError::InvalidInput("invalid"))),
            Err(LanternError::InvalidInput(_))
        ));
        assert!(LanternError::MutationAcceptedUndisclosed.source().is_none());
    }

    #[test]
    fn unknown_or_mixed_acceptance_fails_closed() {
        assert!(!accepted_undisclosed(None, true).unwrap());
        for kind in [0, 2, -1, i32::MAX] {
            assert!(accepted_undisclosed(Some(&MutationAcceptance { kind }), false).is_err());
        }
        let acceptance = MutationAcceptance {
            kind: MutationAcceptanceKind::HandledEffectUndisclosed as i32,
        };
        assert!(accepted_undisclosed(Some(&acceptance), false).unwrap());
        assert!(accepted_undisclosed(Some(&acceptance), true).is_err());
    }
}
