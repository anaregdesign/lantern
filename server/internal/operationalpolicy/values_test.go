package operationalpolicy

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestValuesDefaultsAndClosedUnion(t *testing.T) {
	groups := Groups()
	if len(groups) != 8 {
		t.Fatalf("groups = %v", groups)
	}
	groups[0] = "unknown"
	if Groups()[0] != GroupSlowRPC {
		t.Fatal("Groups leaked its ordering")
	}
	fields := 0
	for _, group := range Groups() {
		t.Run(string(group), func(t *testing.T) {
			value, err := Defaults(group)
			if err != nil || value.Validate() != nil || !group.Valid() {
				t.Fatalf("default %s: %+v, %v", group, value, err)
			}
			actual, err := value.Group()
			if err != nil || actual != group {
				t.Fatalf("selected %s, %v", actual, err)
			}
			selected := reflect.ValueOf(value)
			for i := range selected.NumField() {
				if !selected.Field(i).IsNil() {
					fields += selected.Field(i).Elem().NumField()
				}
			}
			copyValue := value.Clone()
			if !reflect.DeepEqual(value, copyValue) {
				t.Fatal("clone changed values")
			}
			original, _ := json.Marshal(value)
			cloned := reflect.ValueOf(copyValue)
			for i := range cloned.NumField() {
				if !cloned.Field(i).IsNil() && cloned.Field(i).Pointer() == selected.Field(i).Pointer() {
					t.Fatal("clone shares an arm")
				}
			}
			roundtrip := Values{}
			if err := json.Unmarshal(original, &roundtrip); err != nil || !reflect.DeepEqual(roundtrip, value) {
				t.Fatalf("roundtrip: %+v, %v", roundtrip, err)
			}
		})
	}
	if fields != 43 {
		t.Fatalf("selected setting count = %d", fields)
	}
	for _, bad := range []Values{{}, {SlowRPC: &SlowRPC{}, Rate: &Rate{}}} {
		if _, err := bad.Group(); err == nil || bad.Validate() == nil || bad.Clone().Validate() == nil {
			t.Fatal("empty or multiple arms accepted")
		}
	}
	if Group("unknown").Valid() {
		t.Fatal("unknown group accepted")
	}
	if _, err := Defaults("unknown"); err == nil {
		t.Fatal("unknown default accepted")
	}
}

func TestValuesValidation(t *testing.T) {
	tests := []struct {
		name  string
		group Group
		edit  func(*Values)
	}{
		{"slow negative", GroupSlowRPC, func(v *Values) { v.SlowRPC.ThresholdMS = -1 }},
		{"slow overflow", GroupSlowRPC, func(v *Values) { v.SlowRPC.ThresholdMS = math.MaxInt64/int64(time.Millisecond) + 1 }},
		{"rate negative", GroupRate, func(v *Values) { v.Rate.RPS = -1 }},
		{"rate negative zero", GroupRate, func(v *Values) { v.Rate.RPS = math.Copysign(0, -1) }},
		{"rate nan", GroupRate, func(v *Values) { v.Rate.RPS = math.NaN() }},
		{"rate infinite", GroupRate, func(v *Values) { v.Rate.RPS = math.Inf(1) }},
		{"rate disabled burst", GroupRate, func(v *Values) { v.Rate.Burst = 1 }},
		{"rate active zero burst", GroupRate, func(v *Values) { v.Rate.RPS = 2.5 }},
		{"rate active negative burst", GroupRate, func(v *Values) { *v.Rate = Rate{RPS: 1, Burst: -1} }},
		{"delete default zero", GroupRequest, func(v *Values) { v.Request.DeleteByPrefixDefaultLimit = 0 }},
		{"delete inverted", GroupRequest, func(v *Values) { v.Request.DeleteByPrefixMaxLimit = 1 }},
		{"delete overflow", GroupRequest, func(v *Values) { v.Request.DeleteByPrefixMaxLimit = math.MaxUint32 + 1 }},
		{"illuminate k", GroupRequest, func(v *Values) { v.Request.IlluminateMaxK = -1 }},
		{"illuminate step", GroupRequest, func(v *Values) { v.Request.IlluminateMaxStep = -1 }},
		{"batch", GroupRequest, func(v *Values) { v.Request.MaxBatchSize = -1 }},
		{"key", GroupRequest, func(v *Values) { v.Request.MaxKeyLen = -1 }},
		{"scan default zero", GroupRequest, func(v *Values) { v.Request.ScanDefaultLimit = 0 }},
		{"scan inverted", GroupRequest, func(v *Values) { v.Request.ScanMaxLimit = 1 }},
		{"scan overflow", GroupRequest, func(v *Values) { v.Request.ScanMaxLimit = math.MaxUint32 + 1 }},
		{"traversal pushes", GroupRequest, func(v *Values) { v.Request.TraversalMaxPushes = 0 }},
		{"traversal results", GroupRequest, func(v *Values) { v.Request.TraversalMaxResults = 0 }},
		{"traversal edges", GroupRequest, func(v *Values) { v.Request.TraversalMaxTouchedEdges = 0 }},
		{"traversal timeout", GroupRequest, func(v *Values) { v.Request.TraversalTimeoutMS = -1 }},
		{"traversal overflow", GroupRequest, func(v *Values) { v.Request.TraversalTimeoutMS = math.MaxInt64/int64(time.Millisecond) + 1 }},
		{"backup interval zero", GroupBackup, func(v *Values) { v.Backup.IntervalNanos = 0 }},
		{"backup retain", GroupBackup, func(v *Values) { v.Backup.Retain = -1 }},
		{"gc budget", GroupCapacity, func(v *Values) { v.Capacity.GCEdgeBudget = -1 }},
		{"gc interval zero", GroupCapacity, func(v *Values) { v.Capacity.GCIntervalSeconds = 0 }},
		{"gc interval overflow", GroupCapacity, func(v *Values) { v.Capacity.GCIntervalSeconds = math.MaxInt64/int64(time.Second) + 1 }},
		{"capacity edges", GroupCapacity, func(v *Values) { v.Capacity.MaxEdges = -1 }},
		{"capacity edge causal", GroupCapacity, func(v *Values) { v.Capacity.MaxEdgeCausalEntries = -1 }},
		{"capacity vertex causal", GroupCapacity, func(v *Values) { v.Capacity.MaxVertexCausalEntries = -1 }},
		{"capacity vertices", GroupCapacity, func(v *Values) { v.Capacity.MaxVertices = -1 }},
		{"lifecycle drain", GroupLifecycle, func(v *Values) { v.Lifecycle.DrainDelaySeconds = -1 }},
		{"lifecycle drain overflow", GroupLifecycle, func(v *Values) { v.Lifecycle.DrainDelaySeconds = math.MaxInt64/int64(time.Second) + 1 }},
		{"lifecycle shutdown", GroupLifecycle, func(v *Values) { v.Lifecycle.ShutdownTimeoutSeconds = -1 }},
		{"lifecycle shutdown overflow", GroupLifecycle, func(v *Values) { v.Lifecycle.ShutdownTimeoutSeconds = math.MaxInt64/int64(time.Second) + 1 }},
		{"search default", GroupSearch, func(v *Values) { v.Search.DefaultLimit = 0 }},
		{"search inverted", GroupSearch, func(v *Values) { v.Search.MaxLimit = 1 }},
		{"search limit overflow", GroupSearch, func(v *Values) { v.Search.MaxLimit = math.MaxUint32 + 1 }},
		{"search mode", GroupSearch, func(v *Values) { v.Search.DefaultMode = "min_should" }},
		{"search minimum", GroupSearch, func(v *Values) { v.Search.DefaultMinShould = 0 }},
		{"search minimum overflow", GroupSearch, func(v *Values) { v.Search.DefaultMinShould = math.MaxUint32 + 1 }},
		{"search timeout", GroupSearch, func(v *Values) { v.Search.TimeoutMS = 0 }},
		{"search timeout overflow", GroupSearch, func(v *Values) { v.Search.TimeoutMS = math.MaxInt64/int64(time.Millisecond) + 1 }},
		{"search bytes", GroupSearch, func(v *Values) { v.Search.MaxQueryBytes = 0 }},
		{"search terms", GroupSearch, func(v *Values) { v.Search.MaxQueryTerms = 0 }},
		{"search terms exceed bytes", GroupSearch, func(v *Values) { v.Search.MaxQueryTerms = v.Search.MaxQueryBytes + 1 }},
		{"search dictionary", GroupSearch, func(v *Values) { v.Search.MaxDictionaryVisits = 0 }},
		{"search postings", GroupSearch, func(v *Values) { v.Search.MaxPostingVisits = 0 }},
		{"search positions", GroupSearch, func(v *Values) { v.Search.MaxPositionVisits = 0 }},
		{"search expiration", GroupSearch, func(v *Values) { v.Search.MaxExpirationVisits = 0 }},
		{"replication warning", GroupReplication, func(v *Values) { v.Replication.AntiEntropyGapWarnThreshold = -1 }},
		{"replication interval", GroupReplication, func(v *Values) { v.Replication.AntiEntropyIntervalMS = -1 }},
		{"replication interval overflow", GroupReplication, func(v *Values) { v.Replication.AntiEntropyIntervalMS = math.MaxInt64/int64(time.Millisecond) + 1 }},
		{"replication subscribe", GroupReplication, func(v *Values) { v.Replication.AntiEntropySubscribeTimeoutMS = 0 }},
		{"replication subscribe overflow", GroupReplication, func(v *Values) {
			v.Replication.AntiEntropySubscribeTimeoutMS = math.MaxInt64/int64(time.Millisecond) + 1
		}},
		{"replication backoff min", GroupReplication, func(v *Values) { v.Replication.PumpBackoffMinMS = 0 }},
		{"replication backoff max", GroupReplication, func(v *Values) { v.Replication.PumpBackoffMaxMS = 0 }},
		{"replication backoff inverted", GroupReplication, func(v *Values) { v.Replication.PumpBackoffMaxMS = 1 }},
		{"replication backoff overflow", GroupReplication, func(v *Values) { v.Replication.PumpBackoffMaxMS = math.MaxInt64/int64(time.Millisecond) + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, _ := Defaults(test.group)
			test.edit(&value)
			if value.Validate() == nil {
				t.Fatalf("invalid value accepted: %+v", value)
			}
		})
	}
	t.Run("explicit boundary semantics", func(t *testing.T) {
		values := []Values{
			{SlowRPC: &SlowRPC{ThresholdMS: math.MaxInt64 / int64(time.Millisecond)}},
			{Rate: &Rate{RPS: 2.5, Burst: 5}},
			{Backup: &Backup{Enabled: true, IntervalNanos: 1, Retain: 0}},
			{Capacity: &Capacity{GCIntervalSeconds: math.MaxInt64 / int64(time.Second)}},
			{Lifecycle: &Lifecycle{MaxReplicationLag: math.MaxUint64}},
			{Replication: &Replication{AntiEntropySubscribeTimeoutMS: 1, PumpBackoffMinMS: 1, PumpBackoffMaxMS: 1}},
		}
		request, _ := Defaults(GroupRequest)
		request.Request.IlluminateMaxK, request.Request.IlluminateMaxStep = 0, 0
		request.Request.MaxBatchSize, request.Request.MaxKeyLen, request.Request.TraversalTimeoutMS = 0, 0, 0
		values = append(values, request)
		for _, value := range values {
			if value.Validate() != nil {
				t.Fatalf("valid boundary rejected: %+v", value)
			}
		}
		for _, mode := range []MatchMode{MatchAny, MatchAll, MatchMinShould} {
			search, _ := Defaults(GroupSearch)
			search.Search.DefaultMode = mode
			if search.Validate() != nil {
				t.Fatalf("mode %s rejected", mode)
			}
		}
	})
}
