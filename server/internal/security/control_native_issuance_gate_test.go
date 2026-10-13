package security

// Focused current-owner proof with explicit factual provider evidence and fake
// qualified time. Signed provider-to-session composition has a separate wire
// gate. This crosses real Prepare/Apply, fresh admission and the final writer.
import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeIssuanceCurrentOutputBoundary(t *testing.T) {
	for _, change := range []string{"live", "expired-attempt", "policy-before-refresh", "policy-before-output", "authority-before-output"} {
		t.Run(change, func(t *testing.T) {
			image, nativeSession, raw := nativeSessionBenchmarkImage()
			image.Sessions = nil
			_, owners, ticks := authorityTestCompositeImage(t, 3, &image)
			o := &CurrentAuthority{origin: owners[1]}
			renew := func() {
				authorityTestConverge(t, owners)
				for _, owner := range owners {
					if err := owner.network.renewAuthority(s3aTestContext(t)); err != nil {
						t.Fatal(err)
					}
				}
			}
			renew()
			server := httptest.NewUnstartedServer(nil)
			output, err := o.ConfigurePublicOutput(server.Config, CurrentOutputLimits{ReadBytes: 1 << 20, SendBytes: 1 << 20, ConcurrentStreams: 16})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			var event *TokenAuthenticationEvidence
			producer := authorityPublicProducerFunc(func(_ context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
				if event == nil {
					low, _, err := view.TimeBounds()
					if err != nil {
						return CurrentCredentialFacts{}, err
					}
					issuer, _ := view.Snapshot().Issuer(GoogleIssuer)
					client := issuer.NativeClients[0]
					event = &TokenAuthenticationEvidence{Version: 2, Policy: "lantern-native-login-v1", Mode: "native-login", Profile: NativeEndUserProfile, Identity: nativeSession.Identity, Algorithm: "RS256", KeyID: "explicit-unit-facts", Credential: [32]byte{1}, Key: [32]byte{2}, Configuration: authorityIssuerCommitment(issuer, view.Cut().Generation), AudienceClient: [32]byte{3}, Generation: view.Cut().Generation, ConfigRevision: issuer.ConfigRevision, IssuedAt: authorityNumericTime(low.Add(-time.Minute)), ExpiresAt: authorityNumericTime(low.Add(time.Hour)), Native: NativeAuthenticationEvidence{Kind: GoogleIDToken, ClientProfileID: client.ID, ServiceOrigin: client.ServiceOrigin, NoncePolicy: "google-initialize-unbound", Client: NativeClientCommitment(client), Transaction: [32]byte{4}, CreatedAt: low.Add(-time.Minute), ConsumedAt: low.Add(-time.Second), ExpiresAt: low.Add(9 * time.Minute)}}
					if change == "expired-attempt" {
						event.Native.ExpiresAt = low
					}
				}
				return CurrentCredentialFacts{Token: event}, nil
			})
			if change == "expired-attempt" {
				if _, _, err := o.WithRequestCredential(t.Context(), producer); err == nil {
					t.Fatal("expired attempt admitted")
				}
				if len(o.origin.network.kernel.replayState.projection.snapshot.Image().Sessions) != 0 {
					t.Fatal("expired attempt issued")
				}
				return
			}
			writer, recorder, before := currentPublicCredentialWriter(t, o, output, producer)
			ctx := writer.ctx
			review, session, err := o.PrepareSession(ctx, before, producer, nativeSession.Digest, "", "")
			if err != nil {
				t.Fatal("prepare native session", err)
			}
			applied, err := o.Apply(ctx, review, producer, [32]byte{})
			if err != nil || applied.Original == nil || applied.Original.Disposition() != S1Applied {
				t.Fatal("native session Apply", err)
			}
			renew()
			installed, known := o.origin.network.kernel.replayState.projection.snapshot.Session(session.Digest)
			if !known || installed != session {
				t.Fatal("test did not reach durable native session installation")
			}
			suspend := func() {
				admin := authorityFakeCredentialProducer{}
				a, err := o.Admit(t.Context(), admin)
				if err != nil {
					t.Fatal(err)
				}
				cut, _ := a.CurrentCut()
				command := S1Command{Kind: S1Management, Changes: []Change{{Kind: PutPrincipal, Identity: &session.Identity, State: Suspended}}}
				r, _, err := o.Prepare(t.Context(), a, admin, o.Profile(), cut, command)
				if err != nil {
					t.Fatal(err)
				}
				result, err := o.Apply(t.Context(), r, admin, [32]byte{})
				if err != nil || result.Original == nil || result.Original.Disposition() != S1Applied {
					t.Fatal("policy Apply", err)
				}
				renew()
			}
			if change == "policy-before-refresh" {
				suspend()
			}
			ctx, after, err := o.RefreshRequest(ctx)
			if change == "policy-before-refresh" {
				if err == nil {
					t.Fatal("suspended login obtained post-Apply admission")
				}
			} else {
				if err != nil || o.BindIssuedSessionOutput(ctx, after, session) != nil {
					t.Fatal("fresh native issuance binding", err)
				}
				if change == "policy-before-output" {
					suspend()
				}
				if change == "authority-before-output" {
					ticks[1].Add(uint64(20 * time.Second))
				}
			}
			// The exact queued credential never gets a grandfathered output permit.
			n, err := writer.Write([]byte(raw))
			if change == "live" {
				if err != nil || recorder.Body.String() != raw {
					t.Fatal("live control did not output", err)
				}
			} else if err == nil || n != 0 || recorder.Body.Len() != 0 {
				t.Fatal("changed current authority disclosed issued credential", err)
			}
		})
	}
}
