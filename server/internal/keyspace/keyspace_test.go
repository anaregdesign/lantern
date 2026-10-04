package keyspace

import (
	"errors"
	"strings"
	"testing"
)

func TestDataBoundary(t *testing.T) {
	for _, logical := range []string{"users:user1", "sys:roles:admin", "data:users:user1", "Data:x", "日本語:é", "e\u0301", "*", "a\x00b"} {
		t.Run(logical, func(t *testing.T) {
			physical, err := DataKey(logical)
			if err != nil || physical != "data:"+logical {
				t.Fatalf("encode = %q, %v", physical, err)
			}
			decoded, err := LogicalKey(physical)
			if err != nil || decoded != logical {
				t.Fatalf("decode = %q, %v", decoded, err)
			}
			prefix, err := DataKeyPrefix(logical)
			if err != nil || !strings.HasPrefix(physical, prefix) {
				t.Fatalf("prefix = %q, %v", prefix, err)
			}
		})
	}
	if got, err := DataKeyPrefix(""); err != nil || got != DataPrefix {
		t.Fatalf("empty prefix = %q, %v", got, err)
	}
	for _, logical := range []string{"", string([]byte{0xff})} {
		if _, err := DataKey(logical); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("invalid logical %q admitted", logical)
		}
	}
	for _, physical := range []string{"sys:roles:admin", "data:", "users:user1", "Data:x", "data:\xff"} {
		if _, err := LogicalKey(physical); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("invalid physical %q admitted", physical)
		}
	}
}

func TestDataEdges(t *testing.T) {
	for _, endpoints := range [][2]string{{"data:a", "data:b"}, {"data:sys:a", "data:data:b"}} {
		if err := ValidateDataEdge(endpoints[0], endpoints[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, endpoints := range [][2]string{{"sys:a", "data:b"}, {"data:a", "sys:b"}, {"sys:a", "sys:b"}, {"data:", "data:b"}} {
		if err := ValidateDataEdge(endpoints[0], endpoints[1]); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("cross-domain/invalid edge admitted: %v", endpoints)
		}
	}
}
