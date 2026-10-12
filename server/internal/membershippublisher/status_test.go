package membershippublisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestStatusSeparatesDurabilityExpiryAndAcceptance(t *testing.T) {
	config, state, _, now := publisherFixture(t)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	defer closePublisher(t, p)
	p.clock = func() time.Time { return now }
	initial := p.Status(now, time.Minute)
	if initial.Streams[0].Targets[0].LastDurableAt != nil {
		t.Fatal("invented initial delivery date")
	}
	if e = p.Step(now); e != nil {
		t.Fatal(e)
	}
	s := p.Status(now, 7*24*time.Hour)
	if s.NodeAcceptanceObserved || !s.WallClockOnly || len(s.Streams) != 2 || s.Failure != "none" {
		t.Fatal("status authority semantics")
	}
	for _, stream := range s.Streams {
		if stream.JournalVersion != 2 || stream.Pending || stream.Remaining != 30 || !stream.NextIssueAt.Equal(now.Add(5*time.Second)) {
			t.Fatal("incorrect stream observation")
		}
		for _, target := range stream.Targets {
			if target.LastDurableVersion != 2 || target.LastDurableAt == nil || !target.LastDurableAt.Equal(now) || !target.CandidateMatches {
				t.Fatal("missing confirmed delivery")
			}
		}
	}
	*s.Streams[0].Targets[0].ExpiresAt = now
	*s.Streams[0].Targets[0].LastDurableAt = now.Add(-time.Hour)
	if snapshot := p.Status(now, 0); snapshot.Streams[0].Targets[0].ExpiresAt.Equal(now) || !snapshot.Streams[0].Targets[0].LastDurableAt.Equal(now) {
		t.Fatal("caller could mutate retained observations")
	}
	raw, e := json.Marshal(s)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range [][]byte{[]byte(config), []byte("spiffe://"), []byte("PRIVATE KEY"), []byte("Targets"), p.state.Streams[0].Current} {
		if bytes.Contains(raw, secret) {
			t.Fatal("private material in status")
		}
	}
	// With I=5s and M=2s, warning/urgent leads are 14s/9s.
	for _, test := range []struct {
		elapsed         time.Duration
		warning, urgent bool
	}{{15 * time.Second, false, false}, {16 * time.Second, true, false}, {21 * time.Second, true, true}} {
		target := p.Status(now.Add(test.elapsed), time.Second).Streams[0].Targets[0]
		if target.Warning != test.warning || target.Urgent != test.urgent {
			t.Fatal("warning boundary", test, target)
		}
	}
	closePublisher(t, p)
	q, e := Open(config, state, "resume")
	if e != nil {
		t.Fatal(e)
	}
	defer closePublisher(t, q)
	for _, stream := range q.Status(now, 0).Streams {
		for _, target := range stream.Targets {
			if target.ObservedVersion != 2 || target.LastDurableAt != nil || target.LastDurableVersion != 0 {
				t.Fatal("restart invented historical delivery")
			}
		}
	}
}

func TestStatusDoesNotCountUncertainSyncOrMemoryCandidate(t *testing.T) {
	for _, fault := range []string{"journal", "target"} {
		t.Run(fault, func(t *testing.T) {
			config, state, c, now := publisherFixture(t)
			p, e := Open(config, state, "init")
			if e != nil {
				t.Fatal(e)
			}
			defer closePublisher(t, p)
			p.clock = func() time.Time { return now }
			base := p.files
			last := ""
			p.files.rename = func(a, b string) error { last = b; return base.rename(a, b) }
			p.files.directory = func(path string) error {
				if fault == "journal" && last == state || fault == "target" && last == c.Streams[0].Targets[0] {
					return errInjected
				}
				return base.directory(path)
			}
			if p.Step(now) == nil {
				t.Fatal("errInjected persistence error missing")
			}
			s := p.Status(now, 0)
			if fault == "journal" {
				if s.Failure != "journal_uncertain" || s.Streams[0].JournalVersion != 1 {
					t.Fatal("unconfirmed successor counted durable")
				}
				if !errors.Is(p.Step(now.Add(time.Second)), ErrState) {
					t.Fatal("uncertain owner continued")
				}
				return
			}
			target := s.Streams[0].Targets[0]
			if s.Failure != "delivery_retry" || !s.Streams[0].Pending || target.LastDurableAt != nil || target.ObservedVersion != 1 || target.Remaining == nil || *target.Remaining >= 0 || !target.Warning {
				t.Fatal("lag or durability hidden", target)
			}
			p.files = base
			if e = p.Step(now.Add(time.Second)); e != nil {
				t.Fatal(e)
			}
			target = p.Status(now.Add(time.Second), 0).Streams[0].Targets[0]
			if target.LastDurableAt == nil || target.LastDurableVersion != 2 || target.Failure != "" {
				t.Fatal("exact retained flush not observed")
			}
		})
	}
}

func TestStatusReportingFailureAndNormalCancellation(t *testing.T) {
	config, state, _, now := publisherFixture(t)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	defer closePublisher(t, p)
	p.clock = func() time.Time { return now }
	if !errors.Is(p.RunWithStatus(t.Context(), func(Status) error { return errInjected }, time.Hour), ErrReporting) {
		t.Fatal("lost monitoring failure")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var final Status
	if !errors.Is(p.RunWithStatus(ctx, func(s Status) error { final = s; return nil }, time.Hour), context.Canceled) || final.Failure != "stopped" {
		t.Fatal("stop not reported")
	}
	for _, test := range []struct {
		err  error
		want string
	}{{nil, "none"}, {ErrCapacity, "trust_capacity"}, {ErrBudget, "budget_exhausted"}, {ErrJournal, "journal_uncertain"}, {ErrReporting, "reporting_failure"}, {ErrState, "operator_recovery_required"}} {
		if FailureCategory(test.err) != test.want {
			t.Fatal("unsafe error classification")
		}
	}
}
