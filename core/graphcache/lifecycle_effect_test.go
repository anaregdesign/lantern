package graphcache

import (
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestLifecycleEffectVertexAtomicApplicationTime(t *testing.T) {
	for _, mode := range []string{"local", "hlc", "conditional", "conditional hlc", "staged"} {
		t.Run(mode, func(t *testing.T) {
			c := NewGraphCacheWithStaging[string, string](time.Hour)
			now := time.Now()
			live := now.Add(time.Hour)
			c.applicationClock = func() time.Time { return now }
			if err := c.PutVertexWithExpiration("existing", "original", live); err != nil {
				t.Fatal(err)
			}
			items := []VertexItem[string, string]{{Key: "new", Value: "safe", Expiration: live, DenyLifecycleReduction: true}, {Key: "crossed", Value: "expired", Expiration: now, DenyLifecycleReduction: true}}
			var err error
			switch mode {
			case "local":
				_, err = c.PutVerticesWithExpirationOutcomesChecked(items)
			case "hlc":
				_, err = c.PutVerticesWithExpirationHLCOutcomesChecked(items, hlc.Timestamp{WallNs: 10})
			case "conditional":
				_, err = c.PutVerticesWithExpirationIfAbsentOutcomesChecked(items)
			case "conditional hlc":
				_, _, err = c.PutVerticesWithExpirationIfAbsentHLCOutcomesChecked(items, hlc.Timestamp{WallNs: 10})
			case "staged":
				var tx *VertexPutTransaction[string, string]
				tx, err = c.BeginVertexPut(items, hlc.Timestamp{WallNs: 10}, false)
				if tx != nil {
					tx.Abort()
				}
			}
			if !errors.Is(err, ErrLifecycleEffectDenied) {
				t.Fatal("application-time expiry admitted", err)
			}
			if _, live := c.GetVertex("new"); live {
				t.Fatal("denied batch partially wrote")
			}
			if value, live := c.GetVertex("existing"); !live || value != "original" {
				t.Fatal("unrelated value changed")
			}
		})
	}
}
func TestLifecycleEffectVertexShorteningDuplicatesAndCondition(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	now := time.Now()
	c.applicationClock = func() time.Time { return now }
	put := func(items []VertexItem[string, string]) error {
		_, err := c.PutVerticesWithExpirationOutcomesChecked(items)
		return err
	}
	if err := put([]VertexItem[string, string]{{Key: "permanent", Value: "original"}}); err != nil {
		t.Fatal(err)
	}
	if err := put([]VertexItem[string, string]{{Key: "permanent", Value: "replace", Expiration: now.Add(time.Hour), DenyLifecycleReduction: true}}); !errors.Is(err, ErrLifecycleEffectDenied) {
		t.Fatal("permanent lifetime shortened", err)
	}
	if value, _ := c.GetVertex("permanent"); value != "original" {
		t.Fatal("denied effect changed value")
	}
	items := []VertexItem[string, string]{{Key: "duplicate", Value: "long", Expiration: now.Add(time.Hour), DenyLifecycleReduction: true}, {Key: "duplicate", Value: "short", Expiration: now.Add(time.Minute), DenyLifecycleReduction: true}}
	if err := put(items); !errors.Is(err, ErrLifecycleEffectDenied) {
		t.Fatal("request-order shortening ignored", err)
	}
	if _, live := c.GetVertex("duplicate"); live {
		t.Fatal("duplicate rejection partially wrote")
	}
	if err := put([]VertexItem[string, string]{{Key: "growing", Value: "one", Expiration: now.Add(time.Minute), DenyLifecycleReduction: true}, {Key: "growing", Value: "two", Expiration: now.Add(time.Hour), DenyLifecycleReduction: true}}); err != nil {
		t.Fatal("extension requires Delete", err)
	}
	outcomes, err := c.PutVerticesWithExpirationIfAbsentOutcomesChecked([]VertexItem[string, string]{{Key: "permanent", Value: "skipped", Expiration: now.Add(-time.Hour), DenyLifecycleReduction: true}})
	if err != nil || len(outcomes) != 1 || outcomes[0] != PutOutcomeConditionNotMet {
		t.Fatal("condition miss invented delete effect", err)
	}
}
func TestLifecycleEffectEdgeAtomicAndPermanentContributions(t *testing.T) {
	for _, hlcMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "hlc"}[hlcMode], func(t *testing.T) {
			c := NewGraphCache[string, string](time.Hour)
			now := time.Now()
			c.applicationClock = func() time.Time { return now }
			c.AddEdgeWithExpiration("tail", "head", 3, time.Time{})
			items := []EdgeItem[string]{{Tail: "new", Head: "other", Weight: 1, Expiration: now.Add(time.Hour), DenyLifecycleReduction: true}, {Tail: "tail", Head: "head", Weight: 2, Expiration: now.Add(time.Hour), DenyLifecycleReduction: true}}
			var err error
			if hlcMode {
				_, err = c.PutEdgesWithExpirationHLCOutcomesChecked(items, hlc.Timestamp{WallNs: 10})
			} else {
				_, err = c.PutEdgesWithExpirationOutcomesChecked(items)
			}
			if !errors.Is(err, ErrLifecycleEffectDenied) {
				t.Fatal("permanent edge lifetime shortened", err)
			}
			if _, live := c.GetVertex("new"); live {
				t.Fatal("denied edge batch auto-created an endpoint")
			}
			if weight, _, live := c.GetEdgeDetail("tail", "head"); !live || weight != 3 {
				t.Fatal("denied replacement changed edge")
			}
		})
	}
	c := NewGraphCache[string, string](time.Hour)
	now := time.Now()
	c.applicationClock = func() time.Time { return now }
	duplicate := []EdgeItem[string]{{Tail: "a", Head: "b", Weight: 1, Expiration: now.Add(time.Hour), DenyLifecycleReduction: true}, {Tail: "a", Head: "b", Weight: 2, Expiration: now.Add(time.Minute), DenyLifecycleReduction: true}}
	if _, err := c.PutEdgesWithExpirationOutcomesChecked(duplicate); !errors.Is(err, ErrLifecycleEffectDenied) {
		t.Fatal("duplicate edge shortening ignored", err)
	}
	if _, live := c.GetVertex("a"); live {
		t.Fatal("duplicate denial created endpoint")
	}
	if _, err := c.PutEdgesWithExpirationOutcomesChecked([]EdgeItem[string]{{Tail: "expired", Head: "head", Weight: 1, Expiration: now, DenyLifecycleReduction: true}}); !errors.Is(err, ErrLifecycleEffectDenied) {
		t.Fatal("born-expired edge admitted", err)
	}
}
