package security

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthorityOutputWriterImmutableUnitAndNextWrite(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before final sample", false: "after final sample"}[before], func(t *testing.T) {
			_, owners, ticks := authorityTestComposite(t)
			o := owners[1]
			ctx := s3aTestContext(t)
			if err := o.network.renewAuthority(ctx); err != nil {
				t.Fatal(err)
			}
			c, err := o.authenticate(ctx, authorityFakeCredentialProducer{expiry: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			conn := o.outputConnContext(context.Background(), left).Value(authorityOutputContextKey{}).(*authorityOutputConnection)
			if !o.reserveOutput(conn) {
				t.Fatal("reserve")
			}
			defer o.releaseOutput(conn)
			rec := httptest.NewRecorder()
			w := &authorityOutputWriter{owner: o, recipient: conn, credential: c, requirement: authorityOutputRequirement{action: SecurityManage}, destination: rec, ctx: ctx, headers: make(http.Header), status: 200, hooks: &authorityOutputHooks{}}
			original := []byte("approved content")
			w.Header().Set("Set-Cookie", "approved-token")
			pause := func() {
				ticks[1].Add(uint64(20 * time.Second))
				copy(original, []byte("changed contents"))
				w.Header().Set("Set-Cookie", "substituted-token")
			}
			if before {
				w.hooks.beforeSample = pause
			} else {
				w.hooks.afterAuthorize = pause
			}
			_, err = w.Write(original)
			if before {
				if err == nil || rec.Body.Len() != 0 || rec.Header().Get("Set-Cookie") != "" {
					t.Fatal("pre-event expiry leaked", err)
				}
				return
			}
			if err != nil || rec.Body.String() != "approved content" || rec.Header().Get("Set-Cookie") != "approved-token" {
				t.Fatal("late immutable unit changed", err)
			}
			w.hooks = nil
			if _, err := w.Write([]byte("another unit")); err == nil || rec.Body.String() != "approved content" {
				t.Fatal("reused stale output authorization")
			}
		})
	}
}
func TestAuthorityOutputHeaderInventoryBounds(t *testing.T) {
	for _, h := range []http.Header{{"Trailer": {"Private-Token"}}, {"X-Private": {"one\r\ntwo"}}, {"X-Private": {string(make([]byte, authorityOutputHeaderBytes+1))}}, {strings.Repeat("X", authorityOutputHeaderBytes+1): nil}} {
		if _, err := authorityFreezeHeaders(h); err == nil {
			t.Fatal("header inventory escape")
		}
	}
}
