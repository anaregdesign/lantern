package operationalpolicy

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func validPolicy(t *testing.T, group Group) *Policy {
	t.Helper()
	values, err := Defaults(group)
	if err != nil {
		t.Fatal(err)
	}
	policy := &Policy{Version: Version, Generation: 1, Values: values}
	if group == GroupSearch {
		policy.CompatibilityFingerprint = strings.Repeat("ab", 32)
	}
	return policy
}

func TestPolicyValidationAndDetachedCollection(t *testing.T) {
	var policies Policies
	if err := policies.ValidateNilAllowed(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(policies)
	if string(raw) != "{}" {
		t.Fatalf("absent policies encoding = %s", raw)
	}
	for _, group := range Groups() {
		t.Run(string(group), func(t *testing.T) {
			policy := validPolicy(t, group)
			if err := policy.Validate(); err != nil {
				t.Fatal(err)
			}
			if err := policies.Set(group, policy); err != nil {
				t.Fatal(err)
			}
			policy.Generation = 100
			got := policies.Get(group)
			if got.Generation != 1 {
				t.Fatal("Set retained input pointer")
			}
			got.Generation = 200
			if policies.Get(group).Generation != 1 {
				t.Fatal("Get leaked installed pointer")
			}
			clone := policies.Clone()
			if !reflect.DeepEqual(clone, policies) {
				t.Fatal("clone changed collection")
			}
			if err := clone.Set(group, nil); err != nil || policies.Get(group) == nil {
				t.Fatal("clone mutation changed original")
			}
			encoded, err := json.Marshal(policies)
			var restored Policies
			if err != nil || json.Unmarshal(encoded, &restored) != nil || !reflect.DeepEqual(restored, policies) {
				t.Fatalf("roundtrip collection: %s, %v", encoded, err)
			}
		})
	}
	if policies.ValidateNilAllowed() != nil {
		t.Fatal("valid complete collection rejected")
	}
	before := policies.Clone()
	if policies.Set("unknown", validPolicy(t, GroupSlowRPC)) == nil || !reflect.DeepEqual(before, policies) {
		t.Fatal("unknown Set mutated collection")
	}
	if policies.Get("unknown") != nil {
		t.Fatal("unknown Get returned policy")
	}
	if policies.Set(GroupRate, validPolicy(t, GroupSlowRPC)) == nil || !reflect.DeepEqual(before, policies) {
		t.Fatal("mismatched Set mutated collection")
	}
	policies.Rate = validPolicy(t, GroupSlowRPC)
	if policies.ValidateNilAllowed() == nil {
		t.Fatal("mismatched collection group accepted")
	}
	var nilPolicies *Policies
	if nilPolicies.Set(GroupRate, nil) == nil {
		t.Fatal("nil collection accepted Set")
	}
}

func TestPolicyAndChangeMetadata(t *testing.T) {
	t.Run("policy schema and generation", func(t *testing.T) {
		for _, edit := range []func(*Policy){
			func(p *Policy) { p.Version = 0 },
			func(p *Policy) { p.Version = Version + 1 },
			func(p *Policy) { p.Generation = 0 },
			func(p *Policy) { p.Values = Values{} },
			func(p *Policy) { p.CompatibilityFingerprint = strings.Repeat("a", 64) },
		} {
			policy := validPolicy(t, GroupSlowRPC)
			edit(policy)
			if policy.Validate() == nil {
				t.Fatal("invalid policy accepted")
			}
		}
		var nilPolicy *Policy
		if nilPolicy.Validate() == nil || nilPolicy.Clone() != nil {
			t.Fatal("nil policy invalid semantics")
		}
		policy := validPolicy(t, GroupSlowRPC)
		clone := policy.Clone()
		clone.Values.SlowRPC.ThresholdMS = 999
		if policy.Values.SlowRPC.ThresholdMS != 500 {
			t.Fatal("policy clone leaked values")
		}
	})
	t.Run("closed search fingerprint", func(t *testing.T) {
		for _, fingerprint := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
			policy := validPolicy(t, GroupSearch)
			policy.CompatibilityFingerprint = fingerprint
			if policy.Validate() == nil {
				t.Fatalf("invalid fingerprint accepted: %q", fingerprint)
			}
		}
	})
	t.Run("reviewed change metadata", func(t *testing.T) {
		for _, group := range Groups() {
			policy := validPolicy(t, group)
			change := Change{Values: policy.Values, CompatibilityFingerprint: policy.CompatibilityFingerprint}
			if change.Validate() != nil {
				t.Fatalf("adoption rejected: %s", group)
			}
			change.ExpectedGeneration = 1
			if change.Validate() != nil {
				t.Fatalf("update rejected: %s", group)
			}
			change.RetentionReductionAcknowledged = true
			if (change.Validate() == nil) != (group == GroupBackup) {
				t.Fatalf("ack group validation: %s", group)
			}
			change.RetentionReductionAcknowledged = false
			change.ExpectedGeneration = math.MaxUint64
			if change.Validate() == nil {
				t.Fatal("generation without successor accepted")
			}
		}
		policy := validPolicy(t, GroupSlowRPC)
		change := Change{Values: policy.Values}
		clone := change.Clone()
		clone.Values.SlowRPC.ThresholdMS = 1
		if change.Values.SlowRPC.ThresholdMS != 500 {
			t.Fatal("change clone leaked values")
		}
	})
}
