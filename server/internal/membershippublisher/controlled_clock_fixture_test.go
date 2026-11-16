//go:build lantern_test_time && (linux || darwin)

package membershippublisher

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestControlledFixtureClockOwnsAdmissionPublicationAndObservations(t *testing.T) {
	config, state, c, wall := publisherFixture(t)
	at := wall.Add(time.Minute)
	clock := func() time.Time { return at }
	p, err := OpenControlledFixture(config, state, "init", clock)
	if err != nil {
		t.Fatal(err)
	}
	defer closePublisher(t, p)
	initial := p.Status(clock(), time.Minute)
	if !initial.StartedAt.Equal(at) || !initial.ObservedAt.Equal(at) {
		t.Fatal("fixture initial admission sampled another clock")
	}
	for _, stream := range initial.Streams {
		for _, target := range stream.Targets {
			if !target.ObservedAt.Equal(at) || target.LastDurableAt != nil {
				t.Fatal("initial target observation sampled another clock or invented delivery")
			}
		}
	}
	if err := p.Step(clock()); err != nil {
		t.Fatal(err)
	}
	at = at.Add(c.Interval)
	observed := false
	if err := p.RunWithStatus(t.Context(), func(s Status) error {
		observed = true
		if !s.ObservedAt.Equal(at) || !p.last.Equal(at) || s.Failure != "none" {
			t.Error("fixture run or status sampled another clock")
		}
		for _, stream := range s.Streams {
			if stream.JournalVersion != 3 || !stream.ExpiresAt.Equal(at.Add(c.Lifetime)) {
				t.Error("fixture publication sampled another clock")
			}
			for _, target := range stream.Targets {
				if !target.ObservedAt.Equal(at) || target.LastDurableAt == nil || !target.LastDurableAt.Equal(at) {
					t.Error("fixture delivery observation sampled another clock")
				}
			}
		}
		return errInjected
	}, time.Minute); !errors.Is(err, ErrReporting) || !observed {
		t.Fatal("fixture report failure did not stop publication", err)
	}
	at = at.Add(time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.RunWithStatus(ctx, func(s Status) error {
		if !s.ObservedAt.Equal(at) || s.Failure != "stopped" {
			t.Error("fixture cancellation sampled another clock")
		}
		return nil
	}, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatal("fixture cancellation did not join", err)
	}
	before := append([]byte(nil), p.raw...)
	if err := p.Step(at.Add(-2 * time.Second)); !errors.Is(err, ErrState) || p.failure != "clock_rollback" || !bytes.Equal(before, p.raw) {
		t.Fatal("fixture clock seam bypassed rollback refusal", err)
	}
}

func TestControlledFixtureClockPreservesFiniteBudgetAndModes(t *testing.T) {
	for _, mode := range []string{"", "advance-window"} {
		if _, err := OpenControlledFixture("", "", mode, time.Now); !errors.Is(err, ErrState) {
			t.Fatal("fixture constructor admitted another ownership mode", mode, err)
		}
	}
	if _, err := OpenControlledFixture("", "", "init", nil); !errors.Is(err, ErrState) {
		t.Fatal("fixture constructor admitted a missing clock", err)
	}
	config, state, c, now := publisherFixture(t)
	p, err := OpenControlledFixture(config, state, "init", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer closePublisher(t, p)
	before := append([]byte(nil), p.raw...)
	if err := p.Step(c.End); !errors.Is(err, ErrBudget) || !bytes.Equal(before, p.raw) {
		t.Fatal("fixture clock seam extended publication budget", err)
	}
}
