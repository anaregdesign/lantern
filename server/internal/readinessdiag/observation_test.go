package readinessdiag

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestObserverRetainsSeparateProgressAndBoundsOutput(t *testing.T) {
	var output bytes.Buffer
	now := time.Now()
	o := New(&output)
	o.now = func() time.Time { return now }
	if o.Snapshot().WorkerState != "never_started" {
		t.Fatal("invented worker progress")
	}
	o.WorkerStart()
	if s := o.Snapshot(); s.WorkerStarted != 1 || s.WorkerDone != 0 || s.WorkerState != "in_progress" {
		t.Fatal(s)
	}
	o.WorkerDone("restart_wait")
	if s := o.Snapshot(); s.WorkerDone != 1 || s.WorkerPublish != 0 || s.WorkerState != "completed" {
		t.Fatal(s)
	}
	o.WorkerPublished()
	now = now.Add(3 * time.Second)
	o.Health("static_rejection", "not_evaluated")
	var record Record
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record); err != nil {
		t.Fatal(err)
	}
	if s := record.State; !s.Valid() || s.WorkerPublish != 1 || s.WorkerAgeMS != 3000 || s.WorkerReason != "restart_wait" || s.HealthReason != "not_evaluated" {
		t.Fatal(s)
	}
	o.WorkerStart()
	if s := o.Snapshot(); s.WorkerDone != 1 || s.WorkerPublish != 1 || s.WorkerReason != "restart_wait" {
		t.Fatal("prior completion overwritten by unevaluated attempt", s)
	}
	o.WorkerStopped(true)
	if s := o.Snapshot(); s.WorkerState != "stopped_canceled" {
		t.Fatal(s)
	}
	for range 1000 {
		o.Health("static_rejection", "not_evaluated")
		now = now.Add(Interval)
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'})
	if len(lines) != MaxRecords || output.Len() > 64<<10 {
		t.Fatal("unbounded diagnostic", len(lines), output.Len())
	}
	for i, line := range lines {
		if len(line)+1 > MaxRecordBytes || json.Unmarshal(line, &record) != nil || !record.State.Valid() || record.State.Sequence != uint64(i+1) {
			t.Fatal("invalid bounded record")
		}
	}
	if !record.State.Exhausted {
		t.Fatal("exhaustion hidden")
	}
}

func TestObserverRejectsArbitraryPrivateReasons(t *testing.T) {
	var output bytes.Buffer
	o := New(&output)
	o.WorkerStart()
	o.WorkerDone("private-token-key-body")
	o.Health("runtime_rejection", "private-token-key-body")
	if output.Len() != 0 || strings.Contains(output.String(), "private") {
		t.Fatal("private reason emitted")
	}
}
