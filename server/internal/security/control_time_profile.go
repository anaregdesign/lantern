package security

// A semantic revision, bound into provisioning and historical authority.
// Scheduling targets/retries are deliberately absent. The source/path and
// oscillator envelope are conditional premises, not vendor certification or
// authenticated NTP. Ordinary +/-1s month-end leaps are covered; arbitrary
// unannounced steps and unsupported smear are not.
const authorityTimeProfileDescription = "lantern/current-authority/time/v2;" + authorityTimePlatformProfile + ";rate=1000ppm;sample=1000ns;source-host=time.asia.apple.com;source=root-dispersion+nonnegative-root-delay/2+precision+10ms;source-max=1s;rtt-max-exclusive=1s;UTC=[2020,2080);whole-interval-expiry;month-end-leap-envelope=1s-per-possible-event;no-unsupported-smear-or-steps;trusted-source-and-path;no-NTP-authentication"

func authorityOperationalTimePremises() authorityTimePremises {
	return authorityTimePremises{
		ratePPB: 1_000_000, stampError: 1000, sourceAllowance: 10_000_000,
		maxSourceError: 1_000_000_000, maxRTT: 1_000_000_000,
		validUTC: authorityTimeRange{1_577_836_800_000_000_000, 3_471_292_800_000_000_000},
	}
}
