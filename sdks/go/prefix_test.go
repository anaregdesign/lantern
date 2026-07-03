package client

import (
	"testing"
)

func TestScanOptions_Defaults(t *testing.T) {
	o := scanOptions{}
	if o.limit != 0 {
		t.Errorf("default limit = %d, want 0", o.limit)
	}
	if o.cursor != nil {
		t.Errorf("default cursor = %v, want nil", o.cursor)
	}
	if o.order != ScanOrderUnspecified {
		t.Errorf("default order = %v, want unspecified", o.order)
	}
}

func TestScanOptions_Apply(t *testing.T) {
	o := scanOptions{}
	for _, apply := range []ScanOption{
		WithScanLimit(42),
		WithScanCursor([]byte("opaque")),
		WithScanOrder(ScanOrderDesc),
	} {
		apply(&o)
	}
	if o.limit != 42 {
		t.Errorf("limit = %d, want 42", o.limit)
	}
	if string(o.cursor) != "opaque" {
		t.Errorf("cursor = %q, want %q", o.cursor, "opaque")
	}
	if o.order != ScanOrderDesc {
		t.Errorf("order = %v, want descending", o.order)
	}
}

func TestDeleteByPrefixOptions_Defaults(t *testing.T) {
	o := deleteByPrefixOptions{}
	if o.limit != 0 || o.dryRun {
		t.Errorf("defaults = %+v, want zero-value", o)
	}
}

func TestDeleteByPrefixOptions_Apply(t *testing.T) {
	o := deleteByPrefixOptions{}
	for _, apply := range []DeleteByPrefixOption{
		WithDeleteByPrefixLimit(7),
		WithDryRun(),
	} {
		apply(&o)
	}
	if o.limit != 7 {
		t.Errorf("limit = %d, want 7", o.limit)
	}
	if !o.dryRun {
		t.Errorf("dryRun = false, want true")
	}
}
