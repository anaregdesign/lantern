package security

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCurrentPublicAdmissionClassifiesAuthorityTransition(t *testing.T) {
	for _, duringVerification := range []bool{false, true} {
		t.Run(map[bool]string{false: "renewal binding", true: "captured cut"}[duringVerification], func(t *testing.T) {
			_, origins, _ := authorityTestComposite(t)
			o := &CurrentAuthority{origin: origins[1]}
			ctx := s3aTestContext(t)
			if err := o.origin.network.renewAuthority(ctx); err != nil {
				t.Fatal(err)
			}
			var facts CurrentCredentialFacts
			producer := authorityPublicProducerFunc(func(ctx context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
				if facts.Token == nil {
					var err error
					facts, err = (authorityFakeCredentialProducer{}).VerifyCurrentCredential(ctx, view)
					if err != nil {
						return facts, err
					}
				}
				return facts, nil
			})
			before, err := o.Admit(ctx, producer)
			if err != nil {
				t.Fatal(err)
			}
			original := *facts.Token
			advance := func() {
				cut, _ := before.CurrentCut()
				r, err := o.origin.prepare(ctx, producer, cut, s1Changes(s1ReaderRole()))
				if err != nil {
					t.Fatal(err)
				}
				result, err := o.origin.consume(ctx, r, producer, [32]byte{})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := o.origin.network.Drive(ctx, result.digest); err != nil {
					t.Fatal(err)
				}
				authorityTestConverge(t, origins)
			}
			var rejected *Admission
			if duringVerification {
				rejected, err = o.Admit(ctx, authorityPublicProducerFunc(func(context.Context, CurrentCredentialView) (CurrentCredentialFacts, error) {
					advance()
					return facts, nil
				}))
			} else {
				advance()
				rejected, err = o.Admit(ctx, producer)
			}
			if rejected != nil || !errors.Is(err, ErrAuthorityUnavailable) {
				t.Fatal("owner transition was classified as a bad credential", rejected, err)
			}
			if err := before.Check(ctx, time.Time{}); !errors.Is(err, ErrAuthorityUnavailable) {
				t.Fatal("captured old cut was classified as a bad credential", err)
			}
			if err := o.origin.network.renewAuthority(ctx); err != nil {
				t.Fatal(err)
			}
			after, err := o.Admit(ctx, producer)
			if err != nil || after.ScopeBinding() == before.ScopeBinding() || *facts.Token != original {
				t.Fatal("unchanged credential did not obtain fresh authority", err)
			}
		})
	}
}
