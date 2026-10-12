package membershippublisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPublisherPartialDistributionExactResumeAndBudget(t *testing.T) {
	config, state, c, now := publisherFixture(t)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	defer closePublisher(t, p)
	// A second journal cannot compete for the same publication targets.
	if other, e := Open(config, state+".other", "init"); e == nil {
		closePublisher(t, other)
		t.Fatal("second publisher admitted")
	}
	base := p.files.rename
	p.files.rename = func(a, b string) error {
		if b != state {
			raw, e := readPrivate(state, maxStateBytes)
			if e != nil {
				t.Fatal(e)
			}
			var j journal
			if decode(raw, &j) != nil {
				t.Fatal("unreadable candidate")
			}
			found := false
			for _, s := range j.Streams {
				if s.Pending && s.Version == 2 && s.Digest == sha256.Sum256(s.Current) {
					found = true
				}
			}
			if !found {
				t.Fatal("publication preceded durable exact candidate")
			}
		}
		if b == c.Streams[0].Targets[1] {
			return errInjected
		}
		return base(a, b)
	}
	if e = p.Step(now); !errors.Is(e, ErrPublication) {
		t.Fatal(e)
	}
	control := append([]byte{}, p.state.Streams[0].Current...)
	if !p.state.Streams[0].Pending || p.state.Streams[1].Pending || p.state.Streams[1].Version != 2 {
		t.Fatal("partial control blocked data")
	}
	if e = p.Close(); e != nil {
		t.Fatal(e)
	}
	p, e = Open(config, state, "resume")
	if e != nil {
		t.Fatal(e)
	}
	defer closePublisher(t, p)
	// The saved pending control interval is expired here. Resume still sends
	// those exact bytes first, then a later step may issue its next revision.
	if e = p.Step(now.Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	for _, target := range c.Streams[0].Targets {
		raw, _ := os.ReadFile(target)
		if !bytes.Equal(raw, control) {
			t.Fatal("pending revision was re-signed")
		}
	}
	if e = p.Step(now.Add(time.Minute + time.Second)); e != nil {
		t.Fatal(e)
	}
	if p.state.Streams[0].Version != 3 {
		t.Fatal("expired predecessor could not renew")
	}
	before, _ := os.ReadFile(state)
	if e = p.Step(c.End); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(state)
	if !bytes.Equal(before, after) {
		t.Fatal("budget generated another signature")
	}
}

func TestPublisherUncertainJournalAndKnownStateLoss(t *testing.T) {
	for _, fail := range []string{"rename", "directory"} {
		t.Run(fail, func(t *testing.T) {
			config, state, c, now := publisherFixture(t)
			p, e := Open(config, state, "init")
			if e != nil {
				t.Fatal(e)
			}
			defer closePublisher(t, p)
			if fail == "rename" {
				base := p.files.rename
				p.files.rename = func(a, b string) error {
					if b == state {
						return errInjected
					}
					return base(a, b)
				}
			} else {
				p.files.directory = func(string) error { return errInjected }
			}
			if !errors.Is(p.Step(now), ErrState) || !errors.Is(p.Step(now.Add(time.Second)), ErrState) {
				t.Fatal("uncertain write did not latch")
			}
			for _, s := range c.Streams {
				for _, path := range s.Targets {
					raw, _ := os.ReadFile(path)
					if !bytes.Equal(raw, s.Initial) {
						t.Fatal("distributed before durable journal")
					}
				}
			}
			closePublisher(t, p)
			p, e = Open(config, state, "resume")
			if e != nil {
				t.Fatal("intact retained journal refused", e)
			}
			defer closePublisher(t, p)
			if e = p.Step(now.Add(time.Second)); e != nil {
				t.Fatal(e)
			}
			closePublisher(t, p)
			if e = os.Remove(state); e != nil {
				t.Fatal(e)
			}
			for _, mode := range []string{"init", "resume"} {
				if q, e := Open(config, state, mode); e == nil {
					closePublisher(t, q)
					t.Fatal("lost state recreated", mode)
				}
			}
		})
	}
}

func TestPublisherRejectsDriftAndCertificateCapacity(t *testing.T) {
	config, state, c, now := publisherFixture(t)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	defer closePublisher(t, p)
	if e = p.Step(now); e != nil {
		t.Fatal(e)
	}
	closePublisher(t, p)
	if e = os.WriteFile(c.Streams[0].Targets[0], c.Streams[0].Initial, 0600); e != nil {
		t.Fatal(e)
	}
	if q, e := Open(config, state, "resume"); e == nil {
		closePublisher(t, q)
		t.Fatal("rolled-back target accepted")
	}
	c.CleanupBy = now.Add(2 * time.Hour)
	raw, _ := json.Marshal(c)
	if e = os.WriteFile(config, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if q, e := Open(config, state+".new", "init"); e == nil {
		closePublisher(t, q)
		t.Fatal("insufficient certificate lifetime")
	}
}

func TestPublisherOverflowAliasAndNoncanonicalState(t *testing.T) {
	for _, kind := range []string{"overflow", "alias", "noncanonical"} {
		t.Run(kind, func(t *testing.T) {
			config, state, c, _ := publisherFixture(t)
			if kind == "noncanonical" {
				p, e := Open(config, state, "init")
				if e != nil {
					t.Fatal(e)
				}
				closePublisher(t, p)
				raw, e := os.ReadFile(state)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(state, append(raw, '\n'), 0600); e != nil {
					t.Fatal(e)
				}
				if p, e := Open(config, state, "resume"); e == nil {
					closePublisher(t, p)
					t.Fatal("noncanonical retained record accepted")
				}
				return
			}
			if kind == "overflow" {
				c.Interval = time.Duration(1 << 62)
			} else {
				c.Streams[1].Targets[0] = c.Streams[0].Targets[0]
			}
			raw, _ := json.Marshal(c)
			if e := os.WriteFile(config, raw, 0600); e != nil {
				t.Fatal(e)
			}
			if p, e := Open(config, state, "init"); e == nil {
				closePublisher(t, p)
				t.Fatal("invalid immutable plan accepted")
			}
		})
	}
}

func TestPublisherTargetDirectoryUncertaintyReusesExactCandidate(t *testing.T) {
	config, state, c, now := publisherFixture(t)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	defer closePublisher(t, p)
	base := p.files.rename
	last := ""
	p.files.rename = func(a, b string) error { last = b; return base(a, b) }
	directory := p.files.directory
	p.files.directory = func(path string) error {
		if last == c.Streams[0].Targets[0] {
			return errInjected
		}
		return directory(path)
	}
	if !errors.Is(p.Step(now), ErrPublication) {
		t.Fatal("directory uncertainty was counted as durable publication")
	}
	saved := append([]byte{}, p.state.Streams[0].Current...)
	p.files = systemFiles()
	if e = p.Step(now.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(saved, p.state.Streams[0].Current) || p.state.Streams[0].Pending {
		t.Fatal("uncertain target caused new signing")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(p.Run(ctx), context.Canceled) {
		t.Fatal("canceled run continued")
	}
	if e = p.Close(); e != nil {
		t.Fatal(e)
	}
	if !errors.Is(p.Step(now.Add(2*time.Second)), ErrState) {
		t.Fatal("closed publisher accepted work")
	}
}
