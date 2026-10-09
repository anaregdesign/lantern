package security

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthorityOriginOwnerDurableConsumeAndLateRetry(t *testing.T) {
	n, owners, _ := authorityTestComposite(t)
	o := owners[1]
	ctx := s3aTestContext(t)
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	cut := o.network.kernel.replayState.projection.cut
	r, err := o.prepare(ctx, authorityFakeCredentialProducer{}, cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := o.consume(ctx, r, authorityFakeCredentialProducer{}, [32]byte{})
	if err != nil || result.raw == "" || result.outcome != nil {
		t.Fatal("durable origin acknowledgement", err)
	}
	if o.network.kernel.p.count != 4 || o.network.kernel.originSerial != 1 || len(o.requests) != 0 || o.requestBytes != 0 {
		t.Fatal("reserve -> H accounting")
	}
	original, err := verifyHistoricalH(n.f.trust, []byte(result.raw))
	if err != nil || original.handoff.id != r.id {
		t.Fatal(err)
	}
	before := o.network.kernel.p.floor
	again, err := o.consume(ctx, r, nil, [32]byte{})
	if err != nil || again.raw != result.raw || o.network.kernel.p.floor != before {
		t.Fatal("exact retry demanded fresh evidence", err)
	}
	if _, err := o.network.Drive(ctx, result.digest); err != nil {
		t.Fatal(err)
	}
	again, err = o.consume(ctx, r, nil, [32]byte{})
	if err != nil || again.outcome == nil || again.outcome.disposition != S1Applied {
		t.Fatal("original outcome lookup", err)
	}
	if err := o.network.Close(); err != nil || len(o.key) != 0 || !o.closed || !o.purposes.closed {
		t.Fatal("origin close/key clearing", err)
	}
}

func TestAuthorityOriginOwnerFinalSampleAndImmutableCompletion(t *testing.T) {
	for _, phase := range []string{"before sample", "after consume"} {
		t.Run(phase, func(t *testing.T) {
			_, owners, counters := authorityTestComposite(t)
			o := owners[1]
			ctx := s3aTestContext(t)
			if err := o.network.renewAuthority(ctx); err != nil {
				t.Fatal(err)
			}
			r, err := o.prepare(ctx, authorityFakeCredentialProducer{}, o.network.kernel.replayState.projection.cut, s1Changes(s1ReaderRole()))
			if err != nil {
				t.Fatal(err)
			}
			pause := func() { counters[1].Add(uint64(20 * time.Second)) }
			o.hooks = &authorityOriginHooks{}
			if phase == "before sample" {
				o.hooks.beforeSample = pause
			} else {
				o.hooks.afterConsume = pause
			}
			result, err := o.consume(ctx, r, authorityFakeCredentialProducer{expiry: 5 * time.Second}, [32]byte{})
			if phase == "before sample" {
				if err == nil || result.raw != "" || o.network.kernel.originSerial != 1 || len(o.network.kernel.origins) != 0 {
					t.Fatal("pre-event expiry authorized H", err)
				}
				if _, err := o.consume(ctx, r, nil, [32]byte{}); !errors.Is(err, errS2CUnknown) {
					t.Fatal("bare reservation restamped", err)
				}
			} else {
				if err != nil || result.raw == "" {
					t.Fatal("immutable consumed H could not finish late", err)
				}
				reader := s2CapsuleReader{[]byte(result.raw)[len(authorityHistoricalMagic):]}
				raw, _ := reader.record()
				var h authorityHistoricalHeader
				if err := json.Unmarshal(raw, &h); err != nil || !h.ConsumeUpper.Before(h.CredentialDeadline) {
					t.Fatal("H was restamped", err)
				}
			}
		})
	}
}

func TestAuthorityOriginKeyAndCloseBoundaries(t *testing.T) {
	n, owners, _ := authorityTestComposite(t)
	c := n.configs[1]
	keyPath := filepath.Join(filepath.Dir(c.Identity.VotingKey), "origin.key")
	if _, err := loadAuthorityOriginKey(c, c.Identity.VotingKey); err == nil {
		t.Fatal("voting key became origin key")
	}
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(err)
	}
	if _, err := attachAuthorityOrigin(n.nodes[1], owners[1].key, "https://admin.example"); err == nil {
		t.Fatal("two signing owners")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := owners[1].prepare(ctx, authorityFakeCredentialProducer{}, n.f.genesis.state.projection.cut, s1Changes(s1ReaderRole())); err == nil {
		t.Fatal("canceled producer entered")
	}
}

func TestAuthorityOriginOwnerNativeFaultRecovery(t *testing.T) {
	for _, phase := range []string{"before reservation", "after reservation WAL", "before consume", "after consume", "after seal", "after H WAL", "after durable"} {
		t.Run(phase, func(t *testing.T) {
			n, owners, _ := authorityTestComposite(t)
			o := owners[1]
			ctx := s3aTestContext(t)
			if err := o.network.renewAuthority(ctx); err != nil {
				t.Fatal(err)
			}
			r, err := o.prepare(ctx, authorityFakeCredentialProducer{}, o.network.kernel.replayState.projection.cut, s1Changes(s1ReaderRole()))
			if err != nil {
				t.Fatal(err)
			}
			floors, err := o.network.Floors()
			if err != nil {
				t.Fatal(err)
			}
			boom := func() { panic("controlled origin interruption") }
			appends := 0
			durable := ""
			o.hooks = &authorityOriginHooks{}
			switch phase {
			case "before reservation":
				o.network.kernel.p.hooks = &s2cJournalHooks{beforeAppend: boom}
			case "after reservation WAL":
				o.network.kernel.p.hooks = &s2cJournalHooks{afterLog: boom}
			case "before consume":
				o.hooks.beforeSample = boom
			case "after consume":
				o.hooks.afterConsume = boom
			case "after seal":
				o.hooks.afterSeal = boom
			case "after H WAL":
				o.network.kernel.p.hooks = &s2cJournalHooks{afterLog: func() {
					appends++
					if appends == 2 {
						boom()
					}
				}}
			case "after durable":
				o.hooks.afterDurable = func() {
					for _, h := range o.network.kernel.origins {
						durable = h.raw
					}
					boom()
				}
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Error("fault checkpoint missed")
					}
				}()
				_, _ = o.consume(ctx, r, authorityFakeCredentialProducer{}, [32]byte{})
			}()
			if o.network.kernel.unknown == nil {
				t.Fatal("panic did not fail-stop")
			}
			if _, err := o.consume(ctx, r, authorityFakeCredentialProducer{}, [32]byte{}); err == nil {
				t.Fatal("uncertainty resealed")
			}
			if err := o.network.Close(); err != nil {
				t.Fatal(err)
			}
			recovered, err := resumeS3AOwner(n.configs[1], floors)
			if err != nil {
				t.Fatal(err)
			}
			n.nodes[1] = recovered
			key, err := loadAuthorityOriginKey(n.configs[1], filepath.Join(filepath.Dir(n.configs[1].Identity.VotingKey), "origin.key"))
			if err != nil {
				t.Fatal(err)
			}
			current, err := attachAuthorityOrigin(recovered, key, "https://admin.example")
			if err != nil {
				t.Fatal(err)
			}
			wantSerial := uint64(1)
			if phase == "before reservation" {
				wantSerial = 0
			}
			if recovered.kernel.originSerial != wantSerial || recovered.receiver.active != nil || len(current.purposes.pending) != 0 {
				t.Fatal("serial/capability recovery", recovered.kernel.originSerial)
			}
			result, err := current.lookupOriginal(ctx, r.id, r.operation)
			if phase == "after H WAL" || phase == "after durable" {
				if err != nil || result.raw == "" {
					t.Fatal("complete native H lost", err)
				}
				if durable != "" && result.raw != durable {
					t.Fatal("acknowledged H changed")
				}
				parsed, err := verifyHistoricalH(n.f.trust, []byte(result.raw))
				if err != nil || parsed.handoff.id != r.id || parsed.handoff.operation != r.operation || parsed.handoff.serial != 1 {
					t.Fatal("original identity changed", err)
				}
			} else if !errors.Is(err, errS2CUnknown) || result.raw != "" {
				t.Fatal("bare reservation became authority", err)
			}
		})
	}
}

func TestAuthorityOriginOwnerCloseJoinsBoundedCredentialProducers(t *testing.T) {
	_, owners, _ := authorityTestComposite(t)
	o := owners[1]
	producer := &authorityBlockingCredentialProducer{entered: make(chan struct{}, 4)}
	cut := o.network.kernel.replayState.projection.cut
	results := make(chan error, 4)
	for range 4 {
		go func() {
			_, err := o.prepare(context.Background(), producer, cut, s1Changes(s1ReaderRole()))
			results <- err
		}()
	}
	for range 4 {
		select {
		case <-producer.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("producer did not enter")
		}
	}
	if _, err := o.prepare(context.Background(), producer, cut, s1Changes(s1ReaderRole())); !errors.Is(err, errS3ACredit) {
		t.Fatal("unbounded prepare encoder", err)
	}
	if err := o.network.Close(); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("closed producer minted request")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Close did not join")
		}
	}
	if len(o.key) != 0 || len(o.assemblies) != 0 || len(o.inputs) != 0 {
		t.Fatal("owned producer/key leaked")
	}
}

type authorityBlockingCredentialProducer struct{ entered chan struct{} }

func (p *authorityBlockingCredentialProducer) VerifyCurrentCredential(ctx context.Context, _ CurrentCredentialView) (CurrentCredentialFacts, error) {
	p.entered <- struct{}{}
	<-ctx.Done()
	return CurrentCredentialFacts{}, ctx.Err()
}
