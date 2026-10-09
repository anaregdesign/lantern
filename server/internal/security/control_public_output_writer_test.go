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

func currentPublicTestWriter(t *testing.T, o *CurrentAuthority, p *CurrentOutput) (*currentOutputWriter, *httptest.ResponseRecorder, *Admission) {
	t.Helper()
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close(); p.connState(left, http.StateClosed) })
	ctx := p.connContext(context.Background(), left)
	c := ctx.Value(currentOutputConnectionKey{}).(*currentOutputConnection)
	r, err := p.reserve(c, httptest.NewRequest(http.MethodGet, "/auth/session", nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.release(r) })
	ctx = context.WithValue(ctx, currentOutputRequestKey{}, r)
	ctx, a, err := o.WithRequestCredential(ctx, authorityFakeCredentialProducer{expiry: 5 * time.Second})
	if err != nil || a.BindCurrentSelfOutput(ctx) != nil {
		t.Fatal("bind current output", err)
	}
	recorder := httptest.NewRecorder()
	w := &currentOutputWriter{request: r, destination: recorder, ctx: ctx, headers: make(http.Header), status: 200, hooks: &authorityOutputHooks{}}
	r.writer = w
	return w, recorder, a
}

func TestCurrentPublicOutputWriterImmutableUnit(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before", false: "after"}[before], func(t *testing.T) {
			o, p, _, ticks := currentPublicOutputFixture(t)
			w, rec, _ := currentPublicTestWriter(t, o, p)
			payload := []byte("original payload")
			w.Header().Set("Set-Cookie", "original-cookie")
			pause := func() {
				ticks.Add(uint64(20 * time.Second))
				copy(payload, []byte("mutated! payload"))
				w.Header().Set("Set-Cookie", "mutated-cookie")
			}
			if before {
				w.hooks.beforeSample = pause
			} else {
				w.hooks.afterAuthorize = pause
			}
			_, err := w.Write(payload)
			if before {
				if err == nil || rec.Body.Len() != 0 || rec.Header().Get("Set-Cookie") != "" {
					t.Fatal("expiry before final sample disclosed output", err)
				}
				return
			}
			if err != nil || rec.Body.String() != "original payload" || rec.Header().Get("Set-Cookie") != "original-cookie" {
				t.Fatal("authorized immutable unit changed", err)
			}
			w.hooks = nil
			if _, err = w.Write([]byte("next")); err == nil {
				t.Fatal("old final sample reused for next unit")
			}
		})
	}
}

func TestCurrentPublicOutputTrailersNeedTheirOwnEvent(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{true: "expired", false: "live"}[expired], func(t *testing.T) {
			o, p, _, ticks := currentPublicOutputFixture(t)
			w, rec, _ := currentPublicTestWriter(t, o, p)
			w.Header().Set("Trailer", "X-Original-Result")
			w.Header().Set("X-Original-Result", "must-be-trailer")
			if _, err := w.Write([]byte("first")); err != nil {
				t.Fatal(err)
			}
			if rec.Result().Header.Get("X-Original-Result") != "" {
				t.Fatal("trailer escaped into initial headers")
			}
			if expired {
				ticks.Add(uint64(20 * time.Second))
			}
			err := w.finish()
			if expired && (err == nil || rec.Header().Get("X-Original-Result") != "") {
				t.Fatal("stale terminal details disclosed")
			}
			if !expired && (err != nil || rec.Header().Get("X-Original-Result") != "must-be-trailer" || w.frame != 2) {
				t.Fatal("current trailer did not have a separate event", err)
			}
		})
	}
}

func TestCurrentPublicOutputMetadataAndFailure(t *testing.T) {
	o, p, _, ticks := currentPublicOutputFixture(t)
	w, rec, _ := currentPublicTestWriter(t, o, p)
	w.Header().Set("Set-Cookie", "protected")
	ticks.Add(uint64(20 * time.Second))
	BindCurrentFailure(w)
	w.WriteHeader(http.StatusServiceUnavailable)
	if _, err := w.Write([]byte("fixed refusal")); err != nil || rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("fixed refusal retained protected metadata", err)
	}
	for _, headers := range []http.Header{{"Bad(": {"value"}}, {"X-Result": {"bad\x00value"}}, {"X-Result": {"a\r\nb"}}, {"X-Result": {strings.Repeat("x", currentOutputHeaderBytes)}}} {
		if _, err := freezeCurrentHeaders(headers); err == nil {
			t.Fatal("unbounded/invalid metadata")
		}
	}
}
