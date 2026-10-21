package main

import (
	"strings"
	"testing"
)

func TestEdgeSetupCommandRejectsInvalidPlansBeforeTransport(t *testing.T) {
	valid := []string{
		"-endpoints", "https://127.0.0.1:6380", "-ca-cert", "unused.pem", "-output", "unused.json",
		"-warmup-duration", "10s", "-warmup-pair-rps", "100",
		"-steady-duration", "45s", "-steady-pair-rps", "100",
	}
	for _, tc := range []struct {
		name  string
		args  []string
		token string
		want  string
	}{
		{"missing credential", valid, "", "LANTERN_BENCH_AUTH_TOKEN"},
		{"unknown flag", []string{"-unknown"}, "private", "flag provided but not defined"},
		{"zero duration", append(append([]string{}, valid...), "-warmup-duration", "0s"), "private", "positive ticker"},
		{"zero RPS", append(append([]string{}, valid...), "-steady-pair-rps", "0"), "private", "positive ticker"},
		{"zero timeout", append(append([]string{}, valid...), "-request-timeout", "0s"), "private", "positive request timeout"},
		{"extra argument", append(append([]string{}, valid...), "unexpected"), "private", "requires output"},
		{"plaintext", append(append([]string{}, valid...), "-endpoints", "http://127.0.0.1:6380"), "private", "https://"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := executeEdgeSetupCommand(tc.args, tc.token)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid preparation returned %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("error included credential")
			}
		})
	}
}
