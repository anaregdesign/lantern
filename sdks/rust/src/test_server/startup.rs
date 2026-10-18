// Fixed startup diagnostic decoding only; never consulted for readiness or time.
use serde::{Deserialize, Serialize};

#[derive(Deserialize, Serialize, PartialEq)]
#[serde(rename_all = "snake_case")]
enum StartupContext {
    #[serde(rename = "")]
    Unset,
    Unobserved,
    Live,
    Canceled,
    DeadlineExceeded,
    OtherError,
}

#[derive(Deserialize, Serialize, PartialEq)]
#[serde(rename_all = "snake_case")]
enum StartupLabel {
    #[serde(rename = "")]
    Unset,
    Dns,
    Random,
    Dial,
    NativeSend,
    Send,
    Read,
    NativeReceive,
    Packet,
    NativeBinding,
    Measured,
    Binding,
    Rtt,
    SourceBound,
    Transmit,
    Receive,
    Reference,
    Overflow,
    Interval,
    NativeInstall,
    Elapsed,
    Age,
    AnchorInterval,
    UtcWindow,
    Width,
    Contradiction,
    Installed,
    Ok,
    ReadTimeout,
    Rate,
    Deny,
    SourceFault,
    InstallRejected,
    NativeTerminal,
}

#[derive(Deserialize, Serialize, PartialEq)]
#[serde(rename_all = "snake_case")]
enum StartupReadError {
    None,
    Aborted,
    Timeout,
    Canceled,
    Closed,
    Other,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct StartupAttempt {
    attempt: u64,
    stage: StartupLabel,
    reason: StartupLabel,
    has_read_budget: bool,
    read_remaining_ns: i64,
    exchange_context: StartupContext,
    incoming_context: StartupContext,
    read_count: u8,
    #[serde(skip_serializing_if = "Option::is_none")]
    first_read_error: Option<StartupReadError>,
    #[serde(skip_serializing_if = "Option::is_none")]
    final_read_error: Option<StartupReadError>,
}

impl StartupAttempt {
    fn empty(&self) -> bool {
        self.attempt == 0
            && self.stage == StartupLabel::Unset
            && self.reason == StartupLabel::Unset
            && !self.has_read_budget
            && self.read_remaining_ns == 0
            && self.exchange_context == StartupContext::Unset
            && self.incoming_context == StartupContext::Unset
            && self.read_count == 0
            && self.first_read_error.is_none()
            && self.final_read_error.is_none()
    }

    fn valid(&self) -> bool {
        self.stage != StartupLabel::Unset
            && self.reason != StartupLabel::Unset
            && self.exchange_context != StartupContext::Unset
            && self.incoming_context != StartupContext::Unset
            && self.read_remaining_ns <= 3_000_000_000
            && match (
                self.read_count,
                &self.first_read_error,
                &self.final_read_error,
            ) {
                (0, None, None) => true,
                (1, Some(first), Some(last)) => self.has_read_budget && first == last,
                (2, Some(StartupReadError::Aborted), Some(_)) => self.has_read_budget,
                _ => false,
            }
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct StartupSnapshot {
    attempts: [StartupAttempt; 4],
    count: u8,
    overflow: bool,
    published_attempt: u64,
    started_attempt: u64,
    completed_attempt: u64,
    in_flight: bool,
    owner_failed: bool,
    owner_closed: bool,
    remaining_ns: u64,
    anchoring_context: StartupContext,
    incoming_context: StartupContext,
    owner_context: StartupContext,
}

impl StartupSnapshot {
    fn valid(&self) -> bool {
        if self.count > 4
            || self.published_attempt > self.completed_attempt
            || self.completed_attempt > self.started_attempt
            || self.remaining_ns > 45_000_000_000
            || self.anchoring_context == StartupContext::Unset
            || self.incoming_context == StartupContext::Unset
            || self.owner_context == StartupContext::Unset
        {
            return false;
        }
        let mut previous = 0;
        for (index, attempt) in self.attempts.iter().enumerate() {
            if index >= usize::from(self.count) {
                if !attempt.empty() {
                    return false;
                }
            } else {
                if attempt.attempt <= previous || !attempt.valid() {
                    return false;
                }
                previous = attempt.attempt;
            }
        }
        previous == self.published_attempt
    }
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
enum StartupObservationStatus {
    NotObserved,
    Observed,
    Unavailable,
    Truncated,
    Incomplete,
    Invalid,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(super) struct StartupObservation {
    status: StartupObservationStatus,
    #[serde(skip_serializing_if = "Option::is_none")]
    state: Option<StartupSnapshot>,
}

impl StartupObservation {
    pub(super) fn valid(&self) -> bool {
        match (&self.status, &self.state) {
            (StartupObservationStatus::Observed, Some(state)) => state.valid(),
            (StartupObservationStatus::Observed, None) | (_, Some(_)) => false,
            (_, None) => true,
        }
    }
}

#[cfg(test)]
pub(super) fn fixture_record() -> serde_json::Value {
    let empty = serde_json::json!({
        "attempt":0, "stage":"", "reason":"", "has_read_budget":false,
        "read_remaining_ns":0, "exchange_context":"", "incoming_context":"",
        "read_count":0
    });
    let attempt = serde_json::json!({
        "attempt":1, "stage":"read", "reason":"read_timeout", "has_read_budget":true,
        "read_remaining_ns":2_999_000_000_i64, "exchange_context":"deadline_exceeded",
        "incoming_context":"live", "read_count":2,
        "first_read_error":"aborted", "final_read_error":"timeout"
    });
    serde_json::json!({"status":"observed", "state":{
        "attempts":[attempt, empty, empty, empty], "count":1, "overflow":false,
        "published_attempt":1, "started_attempt":1, "completed_attempt":1,
        "in_flight":false, "owner_failed":false, "owner_closed":false,
        "remaining_ns":0, "anchoring_context":"deadline_exceeded",
        "incoming_context":"live", "owner_context":"live"
    }})
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn startup_record_retains_only_fixed_bounded_values() {
        let record = fixture_record();
        let observation: StartupObservation = serde_json::from_value(record.clone()).unwrap();
        assert!(observation.valid());
        assert_eq!(serde_json::to_value(observation).unwrap(), record);
        for (field, value) in [
            ("count", serde_json::json!(5)),
            ("published_attempt", serde_json::json!(2)),
            ("remaining_ns", serde_json::json!(-1)),
            ("owner_context", serde_json::json!("private-token-body")),
            ("body", serde_json::json!("private-token-body")),
        ] {
            let mut invalid = record.clone();
            invalid["state"][field] = value;
            assert!(
                !serde_json::from_value::<StartupObservation>(invalid).is_ok_and(|s| s.valid())
            );
        }
        for (field, value) in [
            ("read_count", serde_json::json!(3)),
            ("first_read_error", serde_json::json!("private-token-body")),
            ("final_read_error", serde_json::json!("private-token-body")),
            ("stage", serde_json::json!("private-endpoint")),
            ("read_remaining_ns", serde_json::json!(3_000_000_001_i64)),
        ] {
            let mut invalid = record.clone();
            invalid["state"]["attempts"][0][field] = value;
            assert!(
                !serde_json::from_value::<StartupObservation>(invalid).is_ok_and(|s| s.valid())
            );
        }
        let mut invalid = record.clone();
        invalid["state"]["attempts"][1]["attempt"] = serde_json::json!(9);
        assert!(
            !serde_json::from_value::<StartupObservation>(invalid)
                .unwrap()
                .valid()
        );
        let mut invalid = record;
        invalid["status"] = serde_json::json!("invalid");
        assert!(
            !serde_json::from_value::<StartupObservation>(invalid)
                .unwrap()
                .valid()
        );
    }
}
