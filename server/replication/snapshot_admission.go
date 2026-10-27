package replication

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

type snapshotAdmissionContextKey struct{}

// snapshotResponseAdmission belongs to one Snapshot request. The transport has
// finished restricting admission before Do returns; this owner never renews or
// replaces that immutable evidence while staged response bytes are installed.
type snapshotResponseAdmission struct {
	mu         sync.Mutex
	native     bool
	started    bool
	admission  *peerauth.Admission
	diagnostic peerauth.AdmissionDiagnostics
}

type snapshotAdmissionClient struct {
	client connect.HTTPClient
	owner  *snapshotResponseAdmission
}

// snapshotRequestContext captures one response's admission without changing the
// shared HTTP client. Callers supply their existing non-nil client and set native
// only for the currently verified workload transport. Legacy test transports do
// not carry a native response admission.
func snapshotRequestContext(ctx context.Context, client connect.HTTPClient, native bool) (context.Context, connect.HTTPClient) {
	owner := &snapshotResponseAdmission{native: native}
	return context.WithValue(ctx, snapshotAdmissionContextKey{}, owner), &snapshotAdmissionClient{client: client, owner: owner}
}

func (c *snapshotAdmissionClient) Do(request *http.Request) (*http.Response, error) {
	c.owner.mu.Lock()
	if c.owner.started {
		c.owner.mu.Unlock()
		return nil, errors.New("Snapshot request context cannot be reused")
	}
	c.owner.started = true
	c.owner.mu.Unlock()
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return response, err
	}
	admission, captured := peerauth.ResponseAdmission(response)
	if c.owner.native && !captured {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, peerauth.ErrMembership
	}
	if response == nil {
		return nil, errors.New("Snapshot response is missing")
	}
	c.owner.mu.Lock()
	c.owner.admission = admission
	c.owner.mu.Unlock()
	return response, nil
}

// checkSnapshotContext intentionally uses the install caller's context. The
// workload body closes and cancels its internal transport context at EOF, but
// its captured Admission still checks the same original native/absolute budget.
func checkSnapshotContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	owner, _ := ctx.Value(snapshotAdmissionContextKey{}).(*snapshotResponseAdmission)
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	admission, native := owner.admission, owner.native
	owner.mu.Unlock()
	if admission != nil {
		diagnostic, err := admission.CheckWithDiagnostics(ctx)
		owner.mu.Lock()
		owner.diagnostic = diagnostic
		owner.mu.Unlock()
		return err
	}
	if native {
		owner.mu.Lock()
		owner.diagnostic = peerauth.AdmissionDiagnostics{Reason: "admission_missing"}
		owner.mu.Unlock()
		return peerauth.ErrMembership
	}
	return nil
}

// Read the last actual check result only. Diagnostics never sample time or
// revalidate/refresh authority after an install has failed.
func snapshotAdmissionDiagnostic(ctx context.Context) peerauth.AdmissionDiagnostics {
	owner, _ := ctx.Value(snapshotAdmissionContextKey{}).(*snapshotResponseAdmission)
	if owner == nil {
		return peerauth.AdmissionDiagnostics{}
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.diagnostic
}
