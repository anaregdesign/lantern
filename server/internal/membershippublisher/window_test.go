package membershippublisher

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func TestMaintenanceWindowsRetainExactJournal(t *testing.T) {
	config, state, c, now, _ := publisherTrustFixture(t, 90*24*time.Hour)
	p, err := Open(config, state, "init")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Step(now.Add(14 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	closePublisher(t, p)
	for cycle := 1; cycle <= 2; cycle++ {
		before := readPublisherJournal(t, state)
		next := c
		next.End = now.Add(time.Duration(cycle) * 30 * 24 * time.Hour)
		next.CleanupBy = next.End.Add(time.Minute)
		nextPath := writePublisherConfig(t, config+".next", next)
		at := c.End.Add(time.Second) // The previous operational window expired.
		if q, err := openOwned(config, state, "resume", at, nil); err == nil {
			closePublisher(t, q)
			t.Fatal("expired ordinary resume accepted")
		}
		if err = advanceWindowAt(config, nextPath, state, at, systemFiles()); err != nil {
			t.Fatal(err)
		}
		after := readPublisherJournal(t, state)
		if before.Binding == after.Binding {
			t.Fatal("binding did not advance")
		}
		after.Binding = before.Binding
		if !reflect.DeepEqual(before, after) {
			t.Fatal("transition changed candidate state")
		}
		for i, stream := range c.Streams {
			for _, target := range stream.Targets {
				raw, _ := os.ReadFile(target)
				if !bytes.Equal(raw, before.Streams[i].Current) {
					t.Fatal("transition changed target")
				}
			}
		}
		if q, err := openOwned(config, state, "advance-window", at, []string{nextPath}); err == nil {
			closePublisher(t, q)
			t.Fatal("old config accepted after transition")
		}
		p, err = openOwned(nextPath, state, "resume", at, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Step(at); err != nil {
			t.Fatal(err)
		}
		for i, saved := range p.state.Streams {
			v, start, end, err := p.verify(i, saved.Current)
			if err != nil || v != before.Streams[i].Version+1 || end.Sub(start) > peerauth.MaxMembershipLifetime {
				t.Fatal("renewal reset revision or extended signed lifetime")
			}
		}
		if !errors.Is(p.Step(next.End), ErrBudget) {
			t.Fatal("maintenance horizon did not stop issuance")
		}
		closePublisher(t, p)
		config, c = nextPath, next
	}
}

func TestWindowKeepsExpiredPartialCandidate(t *testing.T) {
	config, state, c, now := publisherFixture(t)
	p, err := Open(config, state, "init")
	if err != nil {
		t.Fatal(err)
	}
	base := p.files.rename
	p.files.rename = func(a, b string) error {
		if b == c.Streams[0].Targets[1] {
			return errInjected
		}
		return base(a, b)
	}
	if !errors.Is(p.Step(now), ErrPublication) {
		t.Fatal("partial candidate not retained")
	}
	closePublisher(t, p)
	before := readPublisherJournal(t, state)
	next := c
	next.End, next.CleanupBy = c.End.Add(15*time.Minute), c.CleanupBy.Add(15*time.Minute)
	nextPath := writePublisherConfig(t, config+".next", next)
	at := c.CleanupBy.Add(time.Minute)
	if err = advanceWindowAt(config, nextPath, state, at, systemFiles()); err != nil {
		t.Fatal(err)
	}
	p, err = openOwned(nextPath, state, "resume", at, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closePublisher(t, p)
	if err = p.Step(at); err != nil {
		t.Fatal(err)
	}
	if p.state.Streams[0].Pending || !bytes.Equal(p.state.Streams[0].Current, before.Streams[0].Current) || p.state.Streams[1].Version != before.Streams[1].Version+1 {
		t.Fatal("rebind re-signed expired pending bytes or blocked the other stream")
	}
	for _, target := range c.Streams[0].Targets {
		raw, _ := os.ReadFile(target)
		if !bytes.Equal(raw, before.Streams[0].Current) {
			t.Fatal("wrong replay")
		}
	}
	if err = p.Step(at.Add(time.Second)); err != nil || p.state.Streams[0].Version != before.Streams[0].Version+1 {
		t.Fatal("exact completed replay did not precede successor", err)
	}
}

func TestWindowRejectsOtherConfigChanges(t *testing.T) {
	cases := map[string]func(*Config){
		"version":      func(c *Config) { c.Version++ },
		"interval":     func(c *Config) { c.Interval++ },
		"lifetime":     func(c *Config) { c.Lifetime++ },
		"stream order": func(c *Config) { c.Streams[0], c.Streams[1] = c.Streams[1], c.Streams[0] },
		"key":          func(c *Config) { c.Streams[0].KeyFile += ".changed" },
		"roots":        func(c *Config) { c.Streams[0].RootsFile += ".changed" },
		"certificates": func(c *Config) { c.Streams[0].Certificates = c.Streams[0].Certificates[:2] },
		"targets":      func(c *Config) { c.Streams[0].Targets = c.Streams[0].Targets[:1] },
		"initial":      func(c *Config) { c.Streams[0].Initial = append(c.Streams[0].Initial, ' ') },
		"end":          func(c *Config) { c.End = c.End.Add(-20 * time.Minute) },
		"cleanup":      func(c *Config) { c.CleanupBy = c.CleanupBy.Add(-20 * time.Minute) },
		"allowance":    func(c *Config) { c.CleanupBy = c.End.Add(time.Second) },
		"capacity":     func(c *Config) { c.End = c.End.Add(2 * time.Hour); c.CleanupBy = c.End.Add(time.Minute) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			config, state, c, now := publisherFixture(t)
			p, err := Open(config, state, "init")
			if err != nil {
				t.Fatal(err)
			}
			closePublisher(t, p)
			before, _ := os.ReadFile(state)
			c.End, c.CleanupBy = c.End.Add(10*time.Minute), c.CleanupBy.Add(10*time.Minute)
			change(&c)
			next := writePublisherConfig(t, config+".next", c)
			if advanceWindowAt(config, next, state, now, systemFiles()) == nil {
				t.Fatal("unexpected transition")
			}
			after, _ := os.ReadFile(state)
			if !bytes.Equal(before, after) {
				t.Fatal("rejection changed journal")
			}
		})
	}
}

func TestWindowAllPersistenceBoundaries(t *testing.T) {
	for _, fail := range []string{"retained", "temp", "write", "short", "sync", "close", "rename", "directory", "response"} {
		t.Run(fail, func(t *testing.T) {
			config, state, c, now := publisherFixture(t)
			p, err := Open(config, state, "init")
			if err != nil {
				t.Fatal(err)
			}
			closePublisher(t, p)
			before := readPublisherJournal(t, state)
			next := c
			next.End, next.CleanupBy = c.End.Add(15*time.Minute), c.CleanupBy.Add(15*time.Minute)
			nextPath := writePublisherConfig(t, config+".next", next)
			base := systemFiles()
			ops := base
			var events []string
			ops.temp = func(dir, prefix string) (durableFile, error) {
				if fail == "temp" {
					return nil, errInjected
				}
				f, e := base.temp(dir, prefix)
				if e != nil {
					return nil, e
				}
				return failingFile{f, fail, &events}, nil
			}
			ops.retained = func(path string) error {
				if fail == "retained" {
					return errInjected
				}
				return base.retained(path)
			}
			ops.rename = func(a, b string) error {
				if b != state {
					t.Fatal("transition wrote a target")
				}
				if fail == "rename" {
					return errInjected
				}
				return base.rename(a, b)
			}
			ops.directory = func(path string) error {
				if fail == "directory" {
					return errInjected
				}
				e := base.directory(path)
				if fail == "response" && e == nil {
					return errInjected
				}
				return e
			}
			at := c.CleanupBy.Add(time.Second)
			if advanceWindowAt(config, nextPath, state, at, ops) == nil {
				t.Fatal("uncertain transition reported success")
			}
			after := readPublisherJournal(t, state)
			if !reflect.DeepEqual(before.Streams, after.Streams) {
				t.Fatal("failure changed candidate")
			}
			if fail == "directory" || fail == "response" {
				if before.Binding == after.Binding {
					t.Fatal("new record not retained")
				}
				if q, e := openOwned(config, state, "resume", now, nil); e == nil {
					closePublisher(t, q)
					t.Fatal("old config accepted new record")
				}
			} else {
				if before.Binding != after.Binding {
					t.Fatal("premature binding change")
				}
				if q, e := openOwned(config, state, "resume", at, nil); e == nil {
					closePublisher(t, q)
					t.Fatal("expired resume accepted")
				}
				if e := advanceWindowAt(config, nextPath, state, at, base); e != nil {
					t.Fatal("explicit advance retry", e)
				}
			}
			q, e := openOwned(nextPath, state, "resume", at, nil)
			if e != nil {
				t.Fatal("intact new resume", e)
			}
			closePublisher(t, q)
		})
	}
}

func TestWindowAliasesOwnershipAndRetainedDrift(t *testing.T) {
	for _, name := range []string{"old-config", "target", "state", "state-lease", "target-lease", "hardlink", "owner", "missing-lease", "drift", "advanced-target", "old-end"} {
		t.Run(name, func(t *testing.T) {
			config, state, c, now := publisherFixture(t)
			p, e := Open(config, state, "init")
			if e != nil {
				t.Fatal(e)
			}
			if e = p.Step(now); e != nil {
				t.Fatal(e)
			}
			if name != "owner" {
				closePublisher(t, p)
			} else {
				defer closePublisher(t, p)
			}
			next := c
			next.End = c.End.Add(10 * time.Minute)
			next.CleanupBy = c.CleanupBy.Add(10 * time.Minute)
			nextPath := writePublisherConfig(t, config+".next", next)
			switch name {
			case "old-config":
				nextPath = config
			case "target":
				nextPath = c.Streams[0].Targets[0]
			case "state":
				nextPath = state
			case "state-lease":
				nextPath = state + ".lease"
			case "target-lease":
				nextPath = c.Streams[0].Targets[0] + ".publisher.lease"
			case "hardlink":
				nextPath = config + ".link"
				if e = os.Link(config, nextPath); e != nil {
					t.Fatal(e)
				}
			case "missing-lease":
				if e = os.Remove(state + ".lease"); e != nil {
					t.Fatal(e)
				}
			case "advanced-target":
				if e = os.WriteFile(c.Streams[0].Targets[0], []byte("foreign"), 0600); e != nil {
					t.Fatal(e)
				}
			case "old-end":
				// A valid later signed candidate cannot be rescued using next.End.
				q, e := openOwned(config, state, "resume", now, nil)
				if e != nil {
					t.Fatal(e)
				}
				q.config.End = next.End
				if e = q.Step(c.End.Add(time.Second)); e != nil {
					t.Fatal(e)
				}
				closePublisher(t, q)
			}
			ops := systemFiles()
			if name == "drift" {
				base := ops.retained
				ops.retained = func(path string) error {
					if e := base(path); e != nil {
						return e
					}
					raw, e := os.ReadFile(path)
					if e != nil {
						return e
					}
					return os.WriteFile(path, append(raw, '\n'), 0600)
				}
			}
			before, _ := os.ReadFile(state)
			if e = advanceWindowAt(config, nextPath, state, now, ops); e == nil {
				t.Fatal("unsafe transition accepted")
			}
			after, _ := os.ReadFile(state)
			if name != "drift" && !bytes.Equal(before, after) {
				t.Fatal("rejection mutated journal")
			}
		})
	}
}

func TestWindowOldAuthenticityIsSeparateFromNewLeafCapacity(t *testing.T) {
	config, state, c, now, renew := publisherTrustFixture(t, time.Hour)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Step(now); e != nil {
		t.Fatal(e)
	}
	closePublisher(t, p)
	at := now.Add(2 * time.Hour)
	renew(at.Add(-time.Minute), at.Add(60*24*time.Hour))
	next := c
	next.End = at.Add(30 * 24 * time.Hour)
	next.CleanupBy = next.End.Add(time.Minute)
	nextPath := writePublisherConfig(t, config+".next", next)
	before := readPublisherJournal(t, state)
	if e = advanceWindowAt(config, nextPath, state, at, systemFiles()); e != nil {
		t.Fatal("renewed leaf checked at obsolete cleanup", e)
	}
	after := readPublisherJournal(t, state)
	canonical, _ := json.Marshal(next)
	if after.Binding != sha256.Sum256(canonical) || !reflect.DeepEqual(before.Streams, after.Streams) {
		t.Fatal("incorrect cutover")
	}
	q, e := openOwned(nextPath, state, "resume", at, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer closePublisher(t, q)
	if e = q.Step(at); e != nil {
		t.Fatal(e)
	}
}
