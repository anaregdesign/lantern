package security

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
)

// A Write call is the finite unit, including the first protected HTTP headers.
// Stream envelope prefix/body writes may be separate units; even the next part
// requires a new authorization. Authorization never promises a complete frame.
type authorityOutputWriter struct {
	mu                   sync.Mutex
	owner                *authorityOriginOwner
	recipient            *authorityOutputConnection
	credential           authorityCredential
	requirement          authorityOutputRequirement
	destination          http.ResponseWriter
	ctx                  context.Context
	headers, sentHeaders http.Header
	status               int
	sent                 bool
	frame                uint64
	err                  error
	hooks                *authorityOutputHooks
}
type authorityOutputHooks struct {
	beforeSample, afterAuthorize func()
	record                       func(authorityCurrentTime, uint64)
}

func (w *authorityOutputWriter) Header() http.Header { return w.headers }
func (w *authorityOutputWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sent || w.err != nil {
		return
	}
	if status < 200 || status > 599 {
		w.err = ErrPermissionDenied
		return
	}
	w.status = status
}
func authorityFreezeHeaders(h http.Header) (http.Header, error) {
	size := 0
	if len(h) > 128 {
		return nil, errS3ACredit
	}
	for key, values := range h {
		if key == "" || strings.ContainsAny(key, "\r\n:") || strings.EqualFold(key, "Trailer") || strings.HasPrefix(key, http.TrailerPrefix) || len(values) > 128 {
			return nil, ErrPermissionDenied
		}
		// Even a name with no values occupies retained map/key storage.
		size += len(key) + 4
		if size > authorityOutputHeaderBytes {
			return nil, errS3ACredit
		}
		for _, value := range values {
			size += len(key) + len(value) + 4
			if strings.ContainsAny(value, "\r\n") || size > authorityOutputHeaderBytes {
				return nil, errS3ACredit
			}
		}
	}
	return h.Clone(), nil
}
func (w *authorityOutputWriter) Write(raw []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	refuse := func(err error) (int, error) { w.err = err; _ = w.recipient.conn.Close(); return 0, err }
	if len(raw) > authorityOutputUnitBytes || w.frame == ^uint64(0) {
		return refuse(errS3ACredit)
	}
	headers, err := authorityFreezeHeaders(w.headers)
	if err != nil {
		return refuse(err)
	}
	if w.sent && !reflect.DeepEqual(headers, w.sentHeaders) {
		return refuse(ErrPermissionDenied)
	}
	// Detach all mutable caller storage before the event. The destination and
	// socket are immutable request-owned references; there is no send callback.
	payload := bytes.Clone(raw)
	if w.hooks != nil && w.hooks.beforeSample != nil {
		w.hooks.beforeSample()
	}
	status := w.status
	stamp, err := w.owner.authorizeOutput(w.ctx, w.credential, w.requirement)
	if err != nil {
		return refuse(err)
	}
	w.frame++
	if w.hooks != nil && w.hooks.record != nil {
		w.hooks.record(stamp, w.frame)
	}
	if w.hooks != nil && w.hooks.afterAuthorize != nil {
		w.hooks.afterAuthorize()
	}
	if !w.sent {
		for key, values := range headers {
			w.destination.Header()[key] = values
		}
		w.destination.WriteHeader(status)
		w.sent, w.sentHeaders = true, headers
	}
	n, err := w.destination.Write(payload)
	if n != len(payload) || err != nil {
		return refuse(errors.Join(ErrAuthorityUnavailable, err))
	}
	// No application-owned authorized bytes remain usable after this return.
	// TLS/kernel copies are irreversible handoff, with no arrival deadline.
	return n, nil
}
func (w *authorityOutputWriter) Flush() {
	w.mu.Lock()
	sent := w.sent
	w.mu.Unlock()
	if !sent {
		if _, err := w.Write(nil); err != nil {
			return
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil {
		if f, ok := w.destination.(http.Flusher); ok {
			f.Flush()
		}
	}
}
