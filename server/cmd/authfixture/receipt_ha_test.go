package main

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestReceiptHAControlPartitionsOnlyData(t *testing.T) {
	control := &receiptHAControl{}
	data := "/graph.v1.LanternReplicationService/Subscribe"
	policy := "/graph.v1.LanternSecurityPeerService/GetPolicySnapshot"
	for _, tc := range []struct {
		caller, target int
		path           string
		allow          bool
	}{
		{1, 2, data, true}, {2, 1, data, true}, {1, 3, data, false}, {3, 2, data, false},
		{0, 1, data, false}, {1, 0, data, false}, {3, 0, policy, false}, {1, 3, policy, false},
		{1, 2, "/graph.v1.LanternService/GetVertex", false},
	} {
		if got := control.allowed(tc.caller, tc.target, tc.path); got != tc.allow {
			t.Fatalf("%+v got %t", tc, got)
		}
	}
	control.connected.Store(true)
	if !control.allowed(3, 2, data) || control.allowed(1, 0, data) || control.allowed(0, 3, data) {
		t.Fatal("relay broadened the separate control participant's data plane")
	}
}

func TestReceiptHAShutdownDoesNotWaitForCallerPipeEOF(t *testing.T) {
	input, caller := io.Pipe()
	defer input.Close()
	defer caller.Close()
	commands := make(chan string, 1)
	done := make(chan struct{})
	go func() { defer close(done); readReceiptHACommands(context.Background(), input, commands) }()
	if _, err := io.WriteString(caller, "shutdown\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for a second read or caller EOF")
	}
	if command := <-commands; command != "shutdown" {
		t.Fatal("terminal shutdown frame lost")
	}
	if _, open := <-commands; open {
		t.Fatal("command reader remained active after shutdown")
	}
}
