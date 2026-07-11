package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
)

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
