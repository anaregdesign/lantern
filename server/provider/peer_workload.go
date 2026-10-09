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
func NewConfiguredSecurityPeer(runtime *SecurityRuntime, peer *PeerIdentityRuntime) (*SecurityPeerRuntime, error) {
	if runtime == nil {
		return nil, errors.New("missing security runtime")
	}
	if runtime.mode == "off" {
		if runtime.peer != nil {
			return nil, errors.New("workload runtime is already bound")
		}
		runtime.peer = peer
		return nil, nil
	}
	if runtime.current != nil {
		if runtime.native != nil || runtime.authority != nil || runtime.receiver != nil || runtime.peer != nil {
			return nil, errors.New("current cohort cannot share a legacy policy authority")
		}
		if peer != nil {
			d, p := peer.store.Domain(), runtime.current.Profile()
			if d.AuthMode != "oidc" || d.CurrentProfile != p.Binding() || d.SecurityGeneration != p.Generation || d.WriterPublicKey != [32]byte{} {
				return nil, errors.New("data workload current profile mismatch")
			}
		}
		runtime.peer = peer
		return nil, nil // Current private consensus/renewal has its own listener.
	}
	if peer == nil {
		if runtime.authority == nil {
			return nil, errors.New("security replica requires workload membership")
		}
		return nil, nil
	}
	return NewSecurityPeerRuntime(runtime, peer)
}
