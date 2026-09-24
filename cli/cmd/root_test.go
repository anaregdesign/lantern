package cmd

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
)

func TestBuildHTTPClient_HTTP2Wire(t *testing.T) {
	oldTLS, oldCA, oldCert, oldKey, oldServer, oldInsecure := flagTLS, flagTLSCA, flagTLSCert, flagTLSKey, flagTLSServer, flagInsecureTLS
	t.Cleanup(func() {
		flagTLS, flagTLSCA, flagTLSCert, flagTLSKey, flagTLSServer, flagInsecureTLS = oldTLS, oldCA, oldCert, oldKey, oldServer, oldInsecure
	})
	flagTLS, flagTLSCA, flagTLSCert, flagTLSKey, flagTLSServer, flagInsecureTLS = false, "", "", "", "", false

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			http.Error(w, "HTTP/2 required", http.StatusHTTPVersionNotSupported)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	t.Run("h2c", func(t *testing.T) {
		srv := httptest.NewUnstartedServer(handler)
		protocols := new(http.Protocols)
		protocols.SetUnencryptedHTTP2(true)
		srv.Config.Protocols = protocols
		srv.Start()
		defer srv.Close()

		client, err := buildHTTPClient()
		if err != nil {
			t.Fatalf("buildHTTPClient: %v", err)
		}
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("h2c GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusNoContent || resp.ProtoMajor != 2 {
			t.Fatalf("h2c response: status=%d protocol=%s", resp.StatusCode, resp.Proto)
		}
	})

	t.Run("TLS", func(t *testing.T) {
		srv := httptest.NewUnstartedServer(handler)
		srv.EnableHTTP2 = true
		srv.StartTLS()
		defer srv.Close()

		caPath := filepath.Join(t.TempDir(), "test-ca.pem")
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
		if err := os.WriteFile(caPath, certPEM, 0600); err != nil {
			t.Fatalf("write test CA: %v", err)
		}
		flagTLS, flagTLSCA = true, caPath

		client, err := buildHTTPClient()
		if err != nil {
			t.Fatalf("buildHTTPClient: %v", err)
		}
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("TLS GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusNoContent || resp.ProtoMajor != 2 {
			t.Fatalf("TLS response: status=%d protocol=%s", resp.StatusCode, resp.Proto)
		}
	})
}

func TestRunCommand_ExitCodesAndStderr(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode int
		wantText string
	}{
		{
			name:     "ConnectErrorIsRPCFailureEvenWithoutRPCText",
			err:      connect.NewError(connect.CodeInvalidArgument, errors.New("server rejected the request")),
			wantCode: 2,
			wantText: "server rejected the request",
		},
		{
			name:     "WrappedConnectErrorIsRPCFailure",
			err:      fmt.Errorf("family traversal: %w", connect.NewError(connect.CodeResourceExhausted, errors.New("work budget exhausted"))),
			wantCode: 2,
			wantText: "work budget exhausted",
		},
		{
			name:     "LocalErrorMentioningRPCIsNotReclassified",
			err:      errors.New("rpc error text from a local parser"),
			wantCode: 1,
			wantText: "rpc error text from a local parser",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			command := &cobra.Command{
				Use:           "test",
				SilenceErrors: true,
				SilenceUsage:  true,
				RunE: func(*cobra.Command, []string) error {
					return tc.err
				},
			}
			command.SetOut(&stdout)
			command.SetErr(&stderr)

			if got := runCommand(context.Background(), command); got != tc.wantCode {
				t.Errorf("runCommand() = %d, want %d", got, tc.wantCode)
			}
			if got := stdout.String(); got != "" {
				t.Errorf("stdout = %q, want empty", got)
			}
			if got := stderr.String(); !strings.Contains(got, tc.wantText) {
				t.Errorf("stderr = %q, want original error detail %q", got, tc.wantText)
			}
		})
	}
}
