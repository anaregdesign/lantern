package security

// A distinct, conditional container profile. CLOCK_MONOTONIC_RAW progresses
// during ordinary process pauses, but host suspend/live VM restore is excluded.
// Rate and sampling error remain admitted operating premises, not API promises.
const authorityTimePlatformProfile = authorityTimeLinuxProfile
