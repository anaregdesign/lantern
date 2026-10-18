package decisiontrace

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type startupErrorFixture struct{ state StartupSnapshot }

func (e startupErrorFixture) Error() string                                 { return "private-token-endpoint-body" }
func (e startupErrorFixture) AuthorityTimeStartupSnapshot() StartupSnapshot { return e.state }

func TestStartupFailureRecordIsBoundedAndContentFree(t *testing.T) {
	state := StartupSnapshot{Count: 4, PublishedAttempt: 4, StartedAttempt: 4, CompletedAttempt: 4,
		AnchoringContext: ContextDeadlineExceeded, IncomingContext: ContextLive, OwnerContext: ContextLive}
	for i := range state.Attempts {
		state.Attempts[i] = StartupAttempt{Attempt: uint64(i + 1), Stage: "read", Reason: "read_timeout", HasReadBudget: true,
			ReadRemainingNS: 3_000_000_000, ExchangeContext: ContextDeadlineExceeded, IncomingContext: ContextLive,
			ReadCount: 2, FirstReadError: "aborted", FinalReadError: "timeout"}
	}
	var output bytes.Buffer
	WriteStartupFailure(&output, fmt.Errorf("private wrapper: %w", startupErrorFixture{state}))
	if output.Len() == 0 || output.Len() > MaxStartupRecordBytes || bytes.Contains(output.Bytes(), []byte("private")) {
		t.Fatal("record missing, unbounded or disclosed private text")
	}
	decoded, ok := DecodeStartupRecord(bytes.TrimSpace(output.Bytes()))
	if !ok || decoded != state {
		t.Fatal("lost accepted startup/read history")
	}
	for _, invalid := range [][]byte{
		bytes.Replace(output.Bytes(), []byte(`"aborted"`), []byte(`"private-token"`), 1),
		bytes.Replace(output.Bytes(), []byte(`"count":4`), []byte(`"count":5`), 1),
		bytes.Replace(output.Bytes(), []byte(`"read_count":2`), []byte(`"read_count":3`), 1),
		bytes.Replace(output.Bytes(), []byte(`"version":1`), []byte(`"version":2`), 1),
		append(bytes.TrimSpace(output.Bytes()), []byte(` {}`)...),
		[]byte(`{"msg":"authority_time_startup","body":"private-token"}`),
		bytes.Repeat([]byte("x"), MaxStartupRecordBytes),
	} {
		if _, ok := DecodeStartupRecord(bytes.TrimSpace(invalid)); ok {
			t.Fatal("accepted invalid record")
		}
	}
	for _, err := range []error{errors.New("private-token"), startupErrorFixture{StartupSnapshot{}}, startupErrorFixture{func() StartupSnapshot {
		invalid := state
		invalid.Attempts[0].Reason = "private-token"
		return invalid
	}()}} {
		output.Reset()
		WriteStartupFailure(&output, err)
		if output.Len() != 0 {
			t.Fatal("forwarded unavailable or invalid snapshot")
		}
	}
}

func TestStartupSnapshotHasNoMutableReferences(t *testing.T) {
	var check func(reflect.Type)
	check = func(value reflect.Type) {
		switch value.Kind() {
		case reflect.Array:
			check(value.Elem())
		case reflect.Struct:
			for i := range value.NumField() {
				check(value.Field(i).Type)
			}
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
			t.Fatalf("startup snapshot shares mutable state through %v", value)
		}
	}
	check(reflect.TypeFor[StartupSnapshot]())
}
