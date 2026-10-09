package client

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// CurrentSecurityVersionBinding is a complete cache partition, never a grant.
// Discard granting views/cursors when it changes, while retaining possibly sent
// mutation identities for status-only recovery. A cut sequence alone is not a
// version, and a local timer cannot establish current server authority.
func CurrentSecurityVersionBinding(v *pb.SecurityVersion) (string, error) {
	invalid := func() (string, error) {
		return "", fmt.Errorf("%w: invalid current authority contract", ErrFailedPrecondition)
	}
	if v == nil || v.Revision != 0 || len(v.Digest) != 0 || len(v.Generation) != 0 {
		return invalid()
	}
	p, c := v.CurrentProfile, v.CurrentCut
	if p == nil || p.Version != 2 || c == nil || c.Version != 1 || c.Sequence == 0 {
		return invalid()
	}
	full := func(b []byte, size int, zero bool) bool {
		return len(b) == size && (zero || !bytes.Equal(b, make([]byte, size)))
	}
	for _, b := range [][]byte{p.Domain, p.Cohort, p.Protocol, p.TimeProfile, p.Membership, p.Configuration, c.Domain, c.Cohort, c.Projection, c.Frontier, c.Fences, c.Policy, v.AdmissionBinding} {
		if !full(b, 32, false) {
			return invalid()
		}
	}
	if !full(p.Generation, 16, false) || !full(c.Generation, 16, false) || !full(c.Previous, 32, true) || !bytes.Equal(p.Domain, c.Domain) || !bytes.Equal(p.Cohort, c.Cohort) || !bytes.Equal(p.Generation, c.Generation) {
		return invalid()
	}
	encode := func(items ...[]byte) string {
		parts := make([]string, len(items))
		for i, b := range items {
			parts[i] = hex.EncodeToString(b)
		}
		return strings.Join(parts, ":")
	}
	return "current-v2:" + encode(p.Domain, p.Cohort, p.Generation, p.Protocol, p.TimeProfile, p.Membership, p.Configuration) + "/cut-v1:" + encode(c.Domain, c.Cohort, c.Generation) + fmt.Sprintf(":%d:", c.Sequence) + encode(c.Previous, c.Projection, c.Frontier, c.Fences, c.Policy) + "/" + hex.EncodeToString(v.AdmissionBinding), nil
}

// GetCurrentPrincipal reads the current-v2 identity and effective roles through
// the normal authenticated transport. It does not confer authority on later
// calls; the server admits each call independently. Explicit legacy responses
// are refused rather than reinterpreted as current cuts. This call makes one
// attempt even with WithRetry; the caller owns explicit binding refresh.
func (l *Lantern) GetCurrentPrincipal(ctx context.Context) (*pb.GetCurrentPrincipalResponse, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	control := graphv1connect.NewLanternSecurityServiceClient(l.httpClient, l.baseURL, l.opts.clientOptions...)
	response, err := unaryOnce(ctx, &pb.GetCurrentPrincipalRequest{}, control.GetCurrentPrincipal)
	if err != nil {
		return nil, err
	}
	if _, err = CurrentSecurityVersionBinding(response.Version); err != nil {
		return nil, err
	}
	return response, nil
}
