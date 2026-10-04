package provider

import (
	"context"
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
	if _, _, err := NewPeerPlaneServer(PeerPlaneConfig{}, nil, nil, rep, NetConfig{}, nil, runtimeCertified{}); err == nil {
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
	server, cleanup, err := NewPeerPlaneServer(PeerPlaneConfig{}, nil, nil, rep, NetConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)), cert)
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
