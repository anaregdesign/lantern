use std::{error::Error, fmt};

use prost::Message;
use tonic::{Code, Status};

use crate::generated::graph::v1::{SearchErrorDetail, SearchErrorReason};

const SEARCH_ERROR_TYPE: &str = "type.googleapis.com/graph.v1.SearchErrorDetail";

/// A classified gRPC failure. Unrecognized gRPC codes remain in [`Self::Other`].
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[non_exhaustive]
pub enum RpcErrorKind {
    InvalidArgument,
    NotFound,
    FailedPrecondition,
    ResourceExhausted,
    Unavailable,
    DeadlineExceeded,
    Cancelled,
    Unauthenticated,
    PermissionDenied,
    Internal,
    Other(Code),
}

impl From<Code> for RpcErrorKind {
    fn from(code: Code) -> Self {
        match code {
            Code::InvalidArgument => Self::InvalidArgument,
            Code::NotFound => Self::NotFound,
            Code::FailedPrecondition => Self::FailedPrecondition,
            Code::ResourceExhausted => Self::ResourceExhausted,
            Code::Unavailable => Self::Unavailable,
            Code::DeadlineExceeded => Self::DeadlineExceeded,
            Code::Cancelled => Self::Cancelled,
            Code::Unauthenticated => Self::Unauthenticated,
            Code::PermissionDenied => Self::PermissionDenied,
            Code::Internal => Self::Internal,
            other => Self::Other(other),
        }
    }
}

/// Search-specific gRPC details, without losing the original status payload.
///
/// A nonempty but unrecognized or malformed details field is never treated as
/// a known search reason. The original bytes remain available through
/// [`RpcFailure::status`].
#[derive(Debug, Eq, PartialEq)]
#[non_exhaustive]
pub enum SearchDetails {
    Absent,
    Known {
        reason: SearchErrorReason,
        work_kind: String,
    },
    Unknown {
        type_urls: Vec<String>,
        raw_reason: Option<i32>,
    },
    Malformed {
        error: String,
    },
}

/// The original Tonic status and its fail-closed, typed interpretation.
#[derive(Debug)]
pub struct RpcFailure {
    status: Status,
    kind: RpcErrorKind,
    search_details: SearchDetails,
}

impl RpcFailure {
    pub(crate) fn new(status: Status) -> Self {
        let kind = status.code().into();
        let search_details = decode_search_details(&status);
        Self {
            status,
            kind,
            search_details,
        }
    }

    /// Original gRPC code, message, metadata, and uninterpreted details bytes.
    pub fn status(&self) -> &Status {
        &self.status
    }

    pub fn kind(&self) -> RpcErrorKind {
        self.kind
    }

    pub fn search_details(&self) -> &SearchDetails {
        &self.search_details
    }

    /// A validated, typed Lantern search reason and its work-budget kind,
    /// when present in the original rich gRPC status.
    pub fn search_reason(&self) -> Option<(SearchErrorReason, &str)> {
        match &self.search_details {
            SearchDetails::Known { reason, work_kind } => Some((*reason, work_kind)),
            _ => None,
        }
    }
}

fn decode_search_details(status: &Status) -> SearchDetails {
    let bytes = status.details();
    if bytes.is_empty() {
        return SearchDetails::Absent;
    }

    let rich = match tonic_types::pb::Status::decode(bytes) {
        Ok(rich) => rich,
        Err(error) => {
            return SearchDetails::Malformed {
                error: error.to_string(),
            };
        }
    };
    if rich.code != status.code() as i32 {
        return SearchDetails::Malformed {
            error: "rich status code differs from gRPC status".into(),
        };
    }

    let mut type_urls = Vec::new();
    let mut found = None;
    for detail in rich.details {
        if detail.type_url == SEARCH_ERROR_TYPE {
            if found.is_some() {
                return SearchDetails::Malformed {
                    error: "multiple search error details".into(),
                };
            }
            found = Some(detail.value);
        } else {
            type_urls.push(detail.type_url);
        }
    }

    let Some(value) = found else {
        return SearchDetails::Unknown {
            type_urls,
            raw_reason: None,
        };
    };
    let detail = match SearchErrorDetail::decode(value.as_slice()) {
        Ok(detail) => detail,
        Err(error) => {
            return SearchDetails::Malformed {
                error: error.to_string(),
            };
        }
    };

    match SearchErrorReason::try_from(detail.reason) {
        Ok(reason) if reason != SearchErrorReason::Unspecified => SearchDetails::Known {
            reason,
            work_kind: detail.work_kind,
        },
        _ => SearchDetails::Unknown {
            type_urls,
            raw_reason: Some(detail.reason),
        },
    }
}

impl fmt::Display for RpcFailure {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "gRPC {:?}: {}", self.kind, self.status.message())
    }
}

impl Error for RpcFailure {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(&self.status)
    }
}

/// Client errors are non-exhaustive; inspect [`Self::Rpc`] for the exact wire
/// status. A local timeout or dropped mutation does not establish its outcome.
#[non_exhaustive]
pub enum LanternError {
    InvalidEndpoint(&'static str),
    InvalidConfig(&'static str),
    InvalidInput(&'static str),
    InvalidToken,
    TokenProvider(Box<dyn Error + Send + Sync>),
    Transport(tonic::transport::Error),
    Rpc(RpcFailure),
    NotFound,
    DeadlineExceeded,
    Protocol(&'static str),
    MessageTooLarge { actual: usize, limit: usize },
    HealthNotServing(i32),
    Entropy(getrandom::Error),
    Batch(BatchError),
    SearchContinuationLimited,
}

impl LanternError {
    /// Typed search reason without matching on status text. Page-tail limits
    /// originate in an otherwise successful response, not a gRPC error.
    pub fn search_reason(&self) -> Option<(SearchErrorReason, &str)> {
        match self {
            Self::Rpc(failure) => failure.search_reason(),
            Self::SearchContinuationLimited => {
                Some((SearchErrorReason::SearchContinuationLimited, ""))
            }
            _ => None,
        }
    }
}

/// A failed chunk of a logical mutation. `completed_items` counts only
/// validated responses from earlier chunks, not committed or live items.
/// The failed chunk may have applied despite its missing/invalid response.
pub struct BatchError {
    pub completed_items: usize,
    pub source: Box<LanternError>,
}

impl fmt::Debug for BatchError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("BatchError")
            .field("completed_items", &self.completed_items)
            .field("source", &self.source)
            .finish()
    }
}

impl fmt::Display for BatchError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "Lantern batch failed after {} fully observed items: {}",
            self.completed_items, self.source
        )
    }
}

impl Error for BatchError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(self.source.as_ref())
    }
}

impl fmt::Debug for LanternError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidEndpoint(reason) => {
                f.debug_tuple("InvalidEndpoint").field(reason).finish()
            }
            Self::InvalidConfig(reason) => f.debug_tuple("InvalidConfig").field(reason).finish(),
            Self::InvalidInput(reason) => f.debug_tuple("InvalidInput").field(reason).finish(),
            Self::InvalidToken => f.write_str("InvalidToken"),
            Self::TokenProvider(_) => f.debug_tuple("TokenProvider").field(&"[redacted]").finish(),
            Self::Transport(error) => f.debug_tuple("Transport").field(error).finish(),
            Self::Rpc(error) => f.debug_tuple("Rpc").field(error).finish(),
            Self::NotFound => f.write_str("NotFound"),
            Self::DeadlineExceeded => f.write_str("DeadlineExceeded"),
            Self::Protocol(reason) => f.debug_tuple("Protocol").field(reason).finish(),
            Self::MessageTooLarge { actual, limit } => f
                .debug_struct("MessageTooLarge")
                .field("actual", actual)
                .field("limit", limit)
                .finish(),
            Self::HealthNotServing(status) => {
                f.debug_tuple("HealthNotServing").field(status).finish()
            }
            Self::Entropy(error) => f.debug_tuple("Entropy").field(error).finish(),
            Self::Batch(error) => f.debug_tuple("Batch").field(error).finish(),
            Self::SearchContinuationLimited => f.write_str("SearchContinuationLimited"),
        }
    }
}

impl fmt::Display for LanternError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidEndpoint(reason) => write!(f, "invalid Lantern endpoint: {reason}"),
            Self::InvalidConfig(reason) => {
                write!(f, "invalid Lantern client configuration: {reason}")
            }
            Self::InvalidInput(reason) => write!(f, "invalid Lantern input: {reason}"),
            Self::InvalidToken => f.write_str("invalid bearer token from provider"),
            Self::TokenProvider(_) => f.write_str("bearer token provider failed"),
            Self::Transport(error) => write!(f, "Lantern transport failed: {error}"),
            Self::Rpc(error) => error.fmt(f),
            Self::NotFound => f.write_str("Lantern item not found"),
            Self::DeadlineExceeded => f.write_str("Lantern call deadline exceeded"),
            Self::Protocol(reason) => write!(f, "Lantern protocol error: {reason}"),
            Self::MessageTooLarge { actual, limit } => {
                write!(f, "Lantern message size {actual} exceeds limit {limit}")
            }
            Self::HealthNotServing(status) => {
                write!(f, "Lantern Health is not SERVING (status {status})")
            }
            Self::Entropy(error) => {
                write!(f, "Lantern contribution nonce generation failed: {error}")
            }
            Self::Batch(error) => error.fmt(f),
            Self::SearchContinuationLimited => f.write_str(
                "Lantern search retained a bounded prefix of hits; its continuation is incomplete",
            ),
        }
    }
}

impl Error for LanternError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::TokenProvider(error) => Some(error.as_ref()),
            Self::Transport(error) => Some(error),
            Self::Rpc(error) => Some(error),
            Self::Entropy(error) => Some(error),
            Self::Batch(error) => Some(error),
            _ => None,
        }
    }
}

impl From<Status> for LanternError {
    fn from(status: Status) -> Self {
        Self::Rpc(RpcFailure::new(status))
    }
}

#[cfg(test)]
mod tests {
    use prost::Message;
    use tonic::codegen::Bytes;
    use tonic_types::pb::Status as RichStatus;

    use super::*;

    fn with_details(code: Code, details: Vec<prost_types::Any>) -> RpcFailure {
        let rich = RichStatus {
            code: code as i32,
            message: "search failed".into(),
            details,
        };
        RpcFailure::new(Status::with_details(
            code,
            "search failed",
            Bytes::from(rich.encode_to_vec()),
        ))
    }

    #[test]
    fn known_and_unknown_search_details_preserve_original_status() {
        let known = with_details(
            Code::ResourceExhausted,
            vec![prost_types::Any {
                type_url: SEARCH_ERROR_TYPE.into(),
                value: SearchErrorDetail {
                    reason: SearchErrorReason::SearchWorkBudgetExhausted as i32,
                    work_kind: "candidate_visits".into(),
                }
                .encode_to_vec(),
            }],
        );
        assert_eq!(known.kind(), RpcErrorKind::ResourceExhausted);
        assert!(matches!(
            known.search_details(),
            SearchDetails::Known { reason: SearchErrorReason::SearchWorkBudgetExhausted, work_kind }
                if work_kind == "candidate_visits"
        ));
        assert!(!known.status().details().is_empty());

        let unknown = with_details(
            Code::Unavailable,
            vec![prost_types::Any {
                type_url: "other.example/SearchErrorDetail".into(),
                value: vec![1, 2],
            }],
        );
        assert_eq!(unknown.kind(), RpcErrorKind::Unavailable);
        assert!(matches!(
            unknown.search_details(),
            SearchDetails::Unknown { type_urls, raw_reason: None }
                if type_urls == &["other.example/SearchErrorDetail"]
        ));

        let future = with_details(
            Code::InvalidArgument,
            vec![prost_types::Any {
                type_url: SEARCH_ERROR_TYPE.into(),
                value: SearchErrorDetail {
                    reason: 9000,
                    work_kind: "future".into(),
                }
                .encode_to_vec(),
            }],
        );
        assert!(matches!(
            future.search_details(),
            SearchDetails::Unknown {
                raw_reason: Some(9000),
                ..
            }
        ));
    }

    #[test]
    fn malformed_and_mismatched_details_fail_closed() {
        let malformed = RpcFailure::new(Status::with_details(
            Code::InvalidArgument,
            "failure",
            Bytes::from_static(b"\xff"),
        ));
        assert!(matches!(
            malformed.search_details(),
            SearchDetails::Malformed { .. }
        ));
        assert_eq!(malformed.status().details(), b"\xff");

        let mismatch = RpcFailure::new(Status::with_details(
            Code::InvalidArgument,
            "gRPC error",
            Bytes::from(
                RichStatus {
                    code: Code::Internal as i32,
                    message: "different error".into(),
                    details: vec![],
                }
                .encode_to_vec(),
            ),
        ));
        assert!(matches!(
            mismatch.search_details(),
            SearchDetails::Malformed { .. }
        ));
        let invalid = with_details(
            Code::InvalidArgument,
            vec![prost_types::Any {
                type_url: SEARCH_ERROR_TYPE.into(),
                value: vec![0xff],
            }],
        );
        assert!(matches!(
            invalid.search_details(),
            SearchDetails::Malformed { .. }
        ));
        let duplicate = with_details(
            Code::InvalidArgument,
            vec![
                prost_types::Any {
                    type_url: SEARCH_ERROR_TYPE.into(),
                    value: vec![],
                },
                prost_types::Any {
                    type_url: SEARCH_ERROR_TYPE.into(),
                    value: vec![],
                },
            ],
        );
        assert!(matches!(
            duplicate.search_details(),
            SearchDetails::Malformed { .. }
        ));
        let absent = RpcFailure::new(Status::not_found("not found"));
        assert_eq!(absent.search_details(), &SearchDetails::Absent);
        assert_eq!(
            RpcErrorKind::from(Code::Unknown),
            RpcErrorKind::Other(Code::Unknown)
        );
    }

    #[test]
    fn required_grpc_failure_categories_remain_typed() {
        for (code, expected) in [
            (Code::InvalidArgument, RpcErrorKind::InvalidArgument),
            (Code::NotFound, RpcErrorKind::NotFound),
            (Code::FailedPrecondition, RpcErrorKind::FailedPrecondition),
            (Code::ResourceExhausted, RpcErrorKind::ResourceExhausted),
            (Code::Unavailable, RpcErrorKind::Unavailable),
            (Code::DeadlineExceeded, RpcErrorKind::DeadlineExceeded),
            (Code::Cancelled, RpcErrorKind::Cancelled),
            (Code::Unauthenticated, RpcErrorKind::Unauthenticated),
            (Code::PermissionDenied, RpcErrorKind::PermissionDenied),
            (Code::Internal, RpcErrorKind::Internal),
            (Code::Aborted, RpcErrorKind::Other(Code::Aborted)),
        ] {
            let failure = RpcFailure::new(Status::new(code, "original message"));
            assert_eq!(failure.kind(), expected);
            assert_eq!(failure.status().code(), code);
            assert_eq!(failure.status().message(), "original message");
        }
    }
}
