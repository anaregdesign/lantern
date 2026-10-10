package security

import (
	"context"
	"fmt"
	"slices"
)

type CurrentAuditRecord struct {
	Result    CurrentChangeResult
	Actor     [32]byte
	Operation string
}

// CurrentAuditView retains immutable original pointers, not another ledger or
// an authorization. The existing ledger limit bounds this index to 100000
// pointers (800 KiB on 64-bit hosts). Only the requested page is projected.
type CurrentAuditView struct {
	profile   CurrentProfile
	originals []*OriginalOutcome
}

func (v *CurrentAuditView) Len() int { return len(v.originals) }

func (v *CurrentAuditView) Page(start, end int) ([]CurrentAuditRecord, error) {
	if start < 0 || end < start || end > v.Len() || end-start > 200 {
		return nil, ErrS1Contract
	}
	records := make([]CurrentAuditRecord, 0, end-start)
	for _, original := range v.originals[start:end] {
		if len(original.items) == 0 {
			return nil, ErrS1Contract
		}
		// Apply emits the session command kind directly; management outcomes
		// contain each validated Change kind. Avoid decoding command payloads
		// (including secrets) merely to classify an audit row.
		kind := S1Management
		if original.items[0].Kind == S1IssueSession || original.items[0].Kind == S1RevokeSession {
			kind = original.items[0].Kind
		}
		records = append(records, CurrentAuditRecord{Result: CurrentChangeResult{Profile: v.profile, ID: original.id, Intent: original.operation.digest, Progress: CurrentApplied, Original: original}, Actor: s2cHash("public-original-actor", original.operation.actor), Operation: kind})
	}
	return records, nil
}

// Read the authenticated original ledger at this exact installed cut. The
// legacy Image audit/scalar revision is not an S1-current commit log. Original
// results expose no private H or credential transcript; no wall time is invented.
func (o *CurrentAuthority) Audit(ctx context.Context, a *Admission, exact string) (*CurrentAuditView, error) {
	if err := o.currentAdmission(ctx, a, false); err != nil {
		return nil, err
	}
	if !a.Access().AllowsGlobal(SecurityManage) || len(exact) > 2048 || !o.origin.network.enterCall() {
		return nil, ErrPermissionDenied
	}
	defer o.origin.network.calls.Done()
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if _, err := o.origin.authorizeCurrentCredentialLocked(ctx, a.current.credential); err != nil {
		return nil, err
	}
	view := &CurrentAuditView{profile: o.Profile(), originals: make([]*OriginalOutcome, 0, len(k.replayState.ledger))}
	for id, original := range k.replayState.ledger {
		if exact != "" && exact != fmt.Sprintf("%d:%x", id.Namespace, id.Nonce) {
			continue
		}
		view.originals = append(view.originals, original)
	}
	slices.SortFunc(view.originals, func(a, b *OriginalOutcome) int {
		if a.commit.Slot < b.commit.Slot {
			return -1
		}
		if a.commit.Slot > b.commit.Slot {
			return 1
		}
		return 0
	})
	return view, nil
}
