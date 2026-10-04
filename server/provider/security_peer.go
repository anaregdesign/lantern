package provider

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
)

const securityRenewalInterval = 5 * time.Second
const securityRenewalTimeout = 5 * time.Second
const securityPeerMaxMessageBytes = 5 << 20 // 4 MiB image + bounded change history/proof

// SecurityPeerRuntime binds the separate workload transport to the policy
// authority. Construction happens before public serving; Renew serializes one
// fresh process-local challenge and never runs in the data RPC hot path.
type SecurityPeerRuntime struct {
	runtime *SecurityRuntime
	peer    *PeerIdentityRuntime
	client  graphv1connect.LanternSecurityPeerServiceClient
	writer  bool
	mu      sync.Mutex
}

func NewSecurityPeerRuntime(runtime *SecurityRuntime, peer *PeerIdentityRuntime) (*SecurityPeerRuntime, error) {
	if runtime == nil || peer == nil || runtime.mode != "oidc" || runtime.native == nil || runtime.receiver != nil || runtime.peer != nil {
		return nil, security.ErrAuthorityUnavailable
	}
	key, err := loadSecurityWriterPublicKey(runtime.config.WriterPublicKeyFile)
	if err != nil {
		return nil, err
	}
	domain := peer.store.Domain()
	if domain.AuthMode != runtime.mode || domain.SecurityGeneration != runtime.config.Generation || !bytes.Equal(domain.WriterPublicKey[:], key) {
		return nil, security.ErrAuthorityUnavailable
	}
	self, _, err := peer.store.Member(peer.config.SelfIdentity)
	if err != nil {
		return nil, err
	}
	writer, _, err := peer.store.MemberAtOrigin(runtime.config.WriterEndpoint)
	if err != nil {
		return nil, err
	}
	result := &SecurityPeerRuntime{runtime: runtime, peer: peer, writer: runtime.authority != nil}
	if result.writer {
		if self != writer {
			return nil, security.ErrAuthorityUnavailable
		}
		runtime.peer = peer
		return result, nil
	}
	if self == writer {
		return nil, security.ErrAuthorityUnavailable
	}
	receiver, err := security.NewLeaseReceiver(runtime.native.Store(), self.ID, key, securityRuntimeClock{runtime.now}, 2*time.Second)
	if err != nil {
		return nil, err
	}
	result.client = graphv1connect.NewLanternSecurityPeerServiceClient(peer.HTTPClient(), writer.Origin,
		connect.WithReadMaxBytes(securityPeerMaxMessageBytes), connect.WithSendMaxBytes(1024))
	runtime.receiver = receiver
	runtime.peer = peer
	return result, nil
}

// PrivateHTTPHandler is mounted under the caller's protected private peer mux.
// The handler independently requires a current workload Admission.
func (p *SecurityPeerRuntime) PrivateHTTPHandler() (string, http.Handler) {
	return graphv1connect.NewLanternSecurityPeerServiceHandler(service.NewSecurityPeerConnectHandler(p.runtime.authority),
		connect.WithReadMaxBytes(1024), connect.WithSendMaxBytes(securityPeerMaxMessageBytes))
}

func (p *SecurityPeerRuntime) Renew(ctx context.Context) error {
	if p == nil || p.runtime == nil || p.writer || p.client == nil {
		return security.ErrAuthorityUnavailable
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.peer.CheckWorkload(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, securityRenewalTimeout)
	defer cancel()
	request, err := p.runtime.receiver.Challenge()
	if err != nil {
		return err
	}
	var known []byte
	if current, ok := p.runtime.native.Store().Current(); ok {
		digest := current.Digest()
		known = digest[:]
	}
	response, err := p.client.RenewPolicyLease(ctx, connect.NewRequest(&pb.RenewPolicyLeaseRequest{
		Receiver: request.Receiver[:], BootNonce: request.BootNonce[:], Challenge: request.Challenge[:], KnownDigest: known}))
	if err != nil {
		return err
	}
	if response.Msg == nil || len(response.Msg.GetSignedLease()) == 0 || len(response.Msg.GetSignedLease()) > 1024 {
		return security.ErrInvalidLease
	}
	if len(response.Msg.GetSignedCheckpoint()) == 0 {
		return p.runtime.receiver.Accept(response.Msg.GetSignedLease())
	}
	proof, err := p.runtime.receiver.ProveCheckpoint(response.Msg.GetSignedLease())
	if err != nil {
		return err
	}
	if err := p.runtime.native.InstallCheckpoint(ctx, response.Msg.GetSignedCheckpoint(), proof); err != nil {
		return err
	}
	return p.runtime.receiver.ActivateCheckpoint(proof)
}

// Run tolerates transport outages only until the already-issued lease expires.
// Check never consults this worker or the network; restart restores no lease.
func (p *SecurityPeerRuntime) Run(ctx context.Context, report func(error)) {
	if p == nil || p.writer {
		return
	}
	for {
		err := p.Renew(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil && report != nil {
			report(err)
		}
		timer := time.NewTimer(securityRenewalInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Ready checks both policy lease and local workload membership. It does not
// claim data convergence or replace replication readiness.
func (p *SecurityPeerRuntime) Ready(ctx context.Context) bool {
	if p == nil || p.runtime == nil {
		return false
	}
	if p.peer.CheckWorkload(ctx) != nil {
		return false
	}
	current, known := p.runtime.native.Store().Current()
	return known && p.runtime.authorityCheck(ctx, current) == nil
}
