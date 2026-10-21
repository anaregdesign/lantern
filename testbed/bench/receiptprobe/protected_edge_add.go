package main

import (
	"crypto/sha256"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
	graphv1 "github.com/anaregdesign/lantern/pb/graph/v1"
)

// The canonical receipt benchmark uses OIDC and existing live endpoints. That
// immutable Edge-only effect is part of the server's receipt intent. Capability
// discovery does not identify auth mode; this is the protected benchmark profile,
// rather than an inference from HTTPS or the supplied credential.
func protectedEdgeAddIntentSHA256(edge *graphv1.Edge, contribID []byte) ([sha256.Size]byte, error) {
	digest, err := edgeAddIntentSHA256(edge, contribID)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return mutationreceipt.IntentDigest(append([]byte("edge-only-v1\x00"), digest[:]...)), nil
}
