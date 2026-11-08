package security

import "context"

// EnrollBrowser applies one restricted first-login operation from an actual
// consumed Code/PKCE transaction. Known active Principals are left untouched,
// including their withdrawn assignments. No ordinary Admission is minted for
// an unknown Principal and no session is issued until registration is durable.
func (o *CurrentAuthority) EnrollBrowser(ctx context.Context, producer CurrentCredentialProducer) error {
	c, err := o.registrationCredential(ctx, producer)
	if err != nil || c.claim.Kind != "browser-registration" {
		return ErrPermissionDenied
	}
	if c.state.Present {
		return nil
	}
	proof, err := o.PrepareRegistration(ctx, producer)
	if err != nil {
		return err
	}
	defer o.DiscardRegistration(proof)
	result, err := o.ApplyRegistration(ctx, proof, producer)
	if err != nil || result.Original == nil || result.Original.Disposition() != S1Applied {
		// A possible dispatch is never followed by a replacement operation.
		// A later fresh login observes the committed Principal if Apply won.
		return ErrAuthorityUnavailable
	}
	return o.RefreshRegistration(ctx, proof, producer)
}
