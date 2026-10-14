package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegistrationOutputUsesCurrentProofAndSession(t *testing.T) {
	for _, sessionOutput := range []bool{false, true} {
		for _, change := range []string{"live", "credential-expiry", "suspended"} {
			t.Run(map[bool]string{false: "review", true: "session"}[sessionOutput]+"/"+change, func(t *testing.T) {
				image := registrationImage()
				_, owners, ticks := authorityTestCompositeImage(t, 3, &image)
				registrationTestRenew(t, owners)
				o := &CurrentAuthority{origin: owners[1]}
				actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "new-user"}
				var event *TokenAuthenticationEvidence
				producer := authorityPublicProducerFunc(func(_ context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
					if event == nil {
						e, err := registrationTestEvent(view, actor, NativeRegistrationPurpose{Kind: "registration"})
						if err != nil {
							return CurrentCredentialFacts{}, err
						}
						if change == "credential-expiry" {
							low, _, _ := view.TimeBounds()
							e.Native.ExpiresAt = low.Add(2 * time.Second)
						}
						event = &e
					}
					return CurrentCredentialFacts{Token: event}, nil
				})
				proof, err := o.PrepareRegistration(t.Context(), producer)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewUnstartedServer(nil)
				output, err := o.ConfigurePublicOutput(server.Config, CurrentOutputLimits{ReadBytes: 1 << 20, SendBytes: 1 << 20, ConcurrentStreams: 16})
				if err != nil {
					t.Fatal(err)
				}
				defer server.Close()
				wrong, _ := currentPublicRequestWriter(t, output, "/graph.v1.LanternService/GetVertex")
				wrong.request.method = http.MethodPost
				if err := o.BindRegistrationOutput(wrong.ctx, proof, producer); err == nil {
					t.Fatal("registration authorized data output")
				}
				writer, recorder := currentPublicRequestWriter(t, output, "/graph.v1.LanternNativeAuthService/ApplyNativeRegistration")
				writer.request.method = http.MethodPost
				if sessionOutput || change == "suspended" {
					result, err := o.ApplyRegistration(s3aTestContext(t), proof, producer)
					if err != nil || result.Original == nil || result.Original.Disposition() != S1Applied {
						t.Fatal("registration", err)
					}
					registrationTestRenew(t, owners)
				}
				if sessionOutput {
					review, session, err := o.PrepareRegistrationSession(t.Context(), proof, producer, strings.Repeat("d", 64))
					if err != nil {
						t.Fatal(err)
					}
					result, err := o.Apply(s3aTestContext(t), review, producer, [32]byte{})
					if err != nil || result.Original == nil || result.Original.Disposition() != S1Applied {
						t.Fatal("session", err)
					}
					registrationTestRenew(t, owners)
					sessionProducer := authorityPublicProducerFunc(func(context.Context, CurrentCredentialView) (CurrentCredentialFacts, error) {
						return CurrentCredentialFacts{Session: &session}, nil
					})
					ctx, a, err := o.WithRequestCredential(writer.ctx, sessionProducer)
					if err != nil {
						t.Fatal(err)
					}
					writer.ctx = ctx
					if err := o.BindRegistrationSessionOutput(ctx, proof, producer, a, session); err != nil {
						t.Fatal("bind session", err)
					}
				} else if err := o.BindRegistrationOutput(writer.ctx, proof, producer); err != nil {
					t.Fatal("bind registration", err)
				}
				if change == "credential-expiry" {
					ticks[1].Add(uint64(3 * time.Second))
				}
				if change == "suspended" {
					admin := authorityFakeCredentialProducer{}
					a, err := o.Admit(t.Context(), admin)
					if err != nil {
						t.Fatal(err)
					}
					cut, _ := a.CurrentCut()
					review, _, err := o.Prepare(t.Context(), a, admin, o.Profile(), cut, s1Changes(Change{Kind: PutPrincipal, Identity: &actor, State: Suspended}))
					if err != nil {
						t.Fatal(err)
					}
					result, err := o.Apply(s3aTestContext(t), review, admin, [32]byte{})
					if err != nil || result.Original == nil || result.Original.Disposition() != S1Applied {
						t.Fatal(err)
					}
					registrationTestRenew(t, owners)
				}
				n, err := writer.Write([]byte("restricted output"))
				if change == "live" {
					if err != nil || recorder.Body.String() != "restricted output" {
						t.Fatal("valid output blocked", err)
					}
				} else if err == nil || n != 0 || recorder.Body.Len() != 0 {
					t.Fatal("stale registration output escaped", err)
				}
			})
		}
	}
}
