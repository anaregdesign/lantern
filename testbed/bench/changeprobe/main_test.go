package main

import (
	"context"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	client "github.com/anaregdesign/lantern/sdks/go"
)

type scriptedChanges struct {
	requests []client.WatchChangesOptions
	failures []error
	cursor   client.ScopedChangeCursor
}

func (s *scriptedChanges) WatchChanges(ctx context.Context, options client.WatchChangesOptions) iter.Seq2[*client.ChangeFrame, error] {
	return func(yield func(*client.ChangeFrame, error) bool) {
		s.requests = append(s.requests, options)
		if !yield(&client.ChangeFrame{Bootstrap: options.Bootstrap, Cursor: s.cursor}, nil) {
			return
		}
		if !yield(&client.ChangeFrame{Invalidations: []client.ChangeInvalidation{{Key: "private-test-data"}}}, nil) {
			return
		}
		if len(s.failures) > 0 {
			err := s.failures[0]
			s.failures = s.failures[1:]
			yield(nil, err)
			return
		}
		<-ctx.Done()
		yield(nil, ctx.Err())
	}
}
func TestConsumerResumesCommittedCursorAndSeparatesRebootstrap(t *testing.T) {
	cursor, err := client.NewScopedChangeCursor([]byte("opaque"))
	if err != nil {
		t.Fatal(err)
	}
	source := &scriptedChanges{cursor: cursor, failures: []error{connect.NewError(connect.CodeDeadlineExceeded, nil), connect.NewError(connect.CodeFailedPrecondition, nil)}}
	ctx, cancel := context.WithTimeout(t.Context(), 350*time.Millisecond)
	defer cancel()
	report, err := consume(ctx, source, client.WatchChangesOptions{Bootstrap: true, Prefix: "allowed:"})
	if err != nil || report.Reopens != 2 || report.Gaps != 1 || report.CursorCommits != 3 || report.Invalidations != 3 {
		t.Fatalf("consumer progress: %+v %v", report, err)
	}
	if len(source.requests) != 3 || source.requests[1].Bootstrap || string(source.requests[1].Cursor.Bytes()) != "opaque" || !source.requests[2].Bootstrap || len(source.requests[2].Cursor.Bytes()) != 0 {
		t.Fatal("cursor advanced on an incomplete frame or gap was resumed")
	}
}
func TestConsumerRejectsPermissionLossAndUnknownProtocolFailure(t *testing.T) {
	cursor, _ := client.NewScopedChangeCursor([]byte("opaque"))
	for _, code := range []connect.Code{connect.CodePermissionDenied, connect.CodeUnauthenticated, connect.CodeUnavailable, connect.CodeDataLoss} {
		source := &scriptedChanges{cursor: cursor, failures: []error{connect.NewError(code, nil)}}
		if _, err := consume(t.Context(), source, client.WatchChangesOptions{Bootstrap: true}); err == nil || len(source.requests) != 1 {
			t.Fatal("unproven failure was retried", code)
		}
	}
}

func TestFailureReportsRetainCountersWithoutPrivateContext(t *testing.T) {
	cursor, _ := client.NewScopedChangeCursor([]byte("private-cursor"))
	original := connect.NewError(connect.CodeUnavailable, errors.New("private-endpoint-context"))
	source := &scriptedChanges{cursor: cursor, failures: []error{errors.Join(client.ErrUnavailable, original)}}
	report, failure := consume(t.Context(), source, client.WatchChangesOptions{Bootstrap: true})
	if report.Failure != "rpc_unavailable" || report.CursorCommits != 1 || report.Invalidations != 1 || !errors.Is(failure, original) || len(source.requests) != 1 {
		t.Fatalf("failed consumer lost counters/cause or retried: %+v %v", report, failure)
	}
	output := filepath.Join(t.TempDir(), "partial.json")
	if err := writeReport(output, report); err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "private-") || !strings.Contains(string(public), "\"failure_category\":\"rpc_unavailable\"") {
		t.Fatal("public report exposed context or lost category")
	}
	directory, err := privateFailure(failure)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	private, err := os.ReadFile(filepath.Join(directory, "failure.log"))
	if err != nil || !strings.Contains(string(private), "private-endpoint-context") {
		t.Fatal("private cause was not preserved", err)
	}
	for path, mode := range map[string]os.FileMode{directory: 0700, filepath.Join(directory, "failure.log"): 0600, output: 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatal("failure evidence permissions", err)
		}
	}
}

func TestProtocolGapHasStableCategoryAndBoundedPrivateDetail(t *testing.T) {
	cursor, _ := client.NewScopedChangeCursor([]byte("opaque"))
	source := &scriptedChanges{cursor: cursor, failures: []error{errors.Join(client.ErrChangeGap, errors.New("private-frame"))}}
	report, failure := consume(t.Context(), source, client.WatchChangesOptions{Bootstrap: true})
	if report.Failure != "protocol_gap" || !errors.Is(failure, client.ErrChangeGap) || len(source.requests) != 1 {
		t.Fatal("protocol failure was hidden or retried")
	}
	directory, err := privateFailure(errors.New(strings.Repeat("x", 128<<10)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	info, err := os.Stat(filepath.Join(directory, "failure.log"))
	if err != nil || info.Size() != (64<<10)+1 {
		t.Fatal("private failure detail is not bounded", err)
	}
}
