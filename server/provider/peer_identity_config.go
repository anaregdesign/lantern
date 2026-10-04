package provider

import "time"

// PeerIdentityConfig is operator-owned, independent of public Bearer/session
// configuration. Composition supplies its own auth mode, namespace, generation
// and writer trust rather than accepting them from a peer.
type PeerIdentityConfig struct {
	AuthMode           string
	Deployment         [16]byte
	SecurityGeneration [16]byte
	WriterPublicKey    [32]byte
	CAFile             string
	OperatorKeyFile    string
	ManifestFile       string
	StateFile          string
	StateMode          string
	CertFile           string
	KeyFile            string
	SelfIdentity       string
	Now                func() time.Time
}
