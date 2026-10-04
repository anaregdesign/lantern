package main

import (
	"context"
	client "github.com/anaregdesign/lantern/sdks/go"
	"reflect"
	"testing"
	"time"
)

func TestPercentilesAndBoundedWorkload(t *testing.T) {
	values := []float64{9, 1, 5, 3, 7}
	if percentile(values, .5) != 5 || percentile(values, 1) != 9 || percentile(nil, .99) != 0 {
		t.Fatal("percentiles")
	}
	if !reflect.DeepEqual(values, []float64{9, 1, 5, 3, 7}) {
		t.Fatal("percentile mutated caller")
	}
	for _, duration := range []time.Duration{0, -1, 6 * time.Minute} {
		if _, err := run(context.Background(), nil, client.ReceiptCapability{}, false, duration, 100); err == nil {
			t.Fatal("unbounded duration accepted")
		}
	}
}
