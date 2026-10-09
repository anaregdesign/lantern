//go:build !linux

package security

// Preserve the original Darwin profile and historical fixture encoding. Other
// unsupported targets still refuse construction; this constant is not a grant.
const authorityTimePlatformProfile = "darwin-arm64;continuous"
