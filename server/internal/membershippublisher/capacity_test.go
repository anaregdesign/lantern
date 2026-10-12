package membershippublisher

import (
	"testing"
	"time"
)

func TestCapacityUsesCurrentCompleteChains(t *testing.T) {
	config, state, c, now, renew := publisherTrustFixture(t, 90*24*time.Hour)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	}()
	if !p.capacity.Equal(now.Add(90 * 24 * time.Hour)) {
		t.Fatal("leaf capacity missing")
	}
	// A new leaf must be valid now, not merely at a future maintenance date.
	renew(now.Add(time.Hour), now.Add(100*24*time.Hour))
	if _, e = p.validateCapacity(now, c.CleanupBy); e == nil {
		t.Fatal("not-yet-valid leaf accepted")
	}
	renew(now.Add(-time.Minute), now.Add(100*24*time.Hour))
	if _, e = p.validateCapacity(now, now.Add(100*24*time.Hour)); e == nil {
		t.Fatal("clock margin missing")
	}
	// Every currently accepted chain's root is also bounded, even if leaf
	// NotAfter extends past it. The fixture root expires in ten years.
	renew(now.Add(-time.Minute), now.Add(11*365*24*time.Hour))
	capacity, e := p.validateCapacity(now, now.Add(9*365*24*time.Hour))
	if e != nil || !capacity.Equal(now.Add(10*365*24*time.Hour)) {
		t.Fatal("issuer capacity ignored", capacity, e)
	}
	if _, e = p.validateCapacity(now, now.Add(10*365*24*time.Hour)); e == nil {
		t.Fatal("root lifetime ignored")
	}
}
