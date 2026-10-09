package provider

import (
	"context"
	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestPeerPlaneDisabledStillRequiresCertifiedServiceAndJoins(t *testing.T) {
	_, data, _ := securityRuntimeFixture(t)
	svc := data.NewLanternService(nil)
	rep, err := data.NewLanternReplicationService(svc)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewPeerPlaneServer(PeerPlaneConfig{}, nil, nil, rep, NetConfig{}, nil, runtimeCertified{}, nil); err == nil {
		t.Fatal("private surface ignored runtime identity")
	}
	restored, err := NewRuntimeRestored(data, svc)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := NewRuntimeCertified(data, svc, rep, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	server, cleanup, err := NewPeerPlaneServer(PeerPlaneConfig{}, nil, nil, rep, NetConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)), cert, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("disabled private lifecycle did not join")
	}
}

func TestPeerListenerAdoptionRequiresExactRuntime(t *testing.T) {
	_, data, _ := securityRuntimeFixture(t)
	svc := data.NewLanternService(nil)
	rep, err := data.NewLanternReplicationService(svc)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(data, svc)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := NewRuntimeCertified(data, svc, rep, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"foreign_replication", "frame", "missing_workload", "foreign_policy", "unexpected_private"} {
		t.Run(phase, func(t *testing.T) {
			owner, err := listenerlaunch.Reserve(t.Context(), true)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			_, address := owner.Addresses()
			config := PeerPlaneConfig{ListenAddress: address}
			certified := cert
			var policy *SecurityPeerRuntime
			switch phase {
			case "foreign_replication":
				certified.replication = nil
			case "frame":
				certified.replicationSendMaxBytes = 1
			case "foreign_policy":
				policy = &SecurityPeerRuntime{}
			case "unexpected_private":
				config.ListenAddress = ""
			}
			if _, cleanup, err := NewPeerPlaneServer(config, nil, policy, rep, NetConfig{}, nil, certified, owner); err == nil {
				cleanup()
				t.Fatal("private guard bypassed")
			}
			// A failing guard cannot consume the role-bound capability.
			if listener, err := owner.Peer(address); err != nil {
				t.Fatal("failed guard consumed private listener", err)
			} else {
				_ = listener.Close()
			}
		})
	}
}
