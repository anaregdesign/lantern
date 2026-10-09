package security

// A distinct, conditional container profile. CLOCK_MONOTONIC_RAW progresses
// during ordinary process pauses, but host suspend/live VM restore is excluded.
// Rate and sampling error remain admitted operating premises, not API promises.
const authorityTimePlatformProfile = "linux-amd64-arm64;monotonic-raw;fixed-zero-time-namespace;host-suspend-unsupported"
