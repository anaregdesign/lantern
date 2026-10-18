package provider

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/server/replication"
)

type workloadPeerSource struct{ peer *PeerIdentityRuntime }

func (s workloadPeerSource) Resolve(ctx context.Context) ([]string, error) {
	if err := s.peer.CheckWorkload(ctx); err != nil {
		return nil, err
	}
	return s.peer.ApprovedOrigins()
}
func NewWorkloadPeerResolver(peer *PeerIdentityRuntime) *PeerResolver {
	if peer == nil {
		return &PeerResolver{}
	}
	return &PeerResolver{Source: workloadPeerSource{peer}}
}
func NewWorkloadPeerTransport(peer *PeerIdentityRuntime) (*replication.PeerTransport, error) {
	if peer == nil {
		return nil, nil
	}
	return replication.NewVerifiedPeerTransport(peer.HTTPClient(), func(origin string) bool {
		origins, err := peer.ApprovedOrigins()
		if err != nil {
			return false
		}
		for _, current := range origins {
			if current == origin {
				return true
			}
		}
		return false
	})
}

// bindWorkload joins the exact independently authenticated data-plane profile
// to the security owner before public certification. Consensus has its own owner.
func (runtime *SecurityRuntime) bindWorkload(peer *PeerIdentityRuntime) error {
	if runtime == nil || runtime.peer != nil {
		return errors.New("missing or already-bound security runtime")
	}
	if peer != nil && peer.CheckWorkload(context.Background()) != nil {
		return errors.New("public serving requires a current authenticated data workload")
	}
	if runtime.mode == "oidc" {
		if runtime.current == nil {
			return errors.New("OIDC requires its owned quorum authority")
		}
		if peer != nil {
			d, p := peer.store.Domain(), runtime.current.Profile()
			if d.AuthMode != "oidc" || d.CurrentProfile != p.Binding() || d.SecurityGeneration != p.Generation || d.WriterPublicKey != [32]byte{} {
				return errors.New("data workload authority profile mismatch")
			}
		}
	}
	runtime.peer = peer
	return nil
}
