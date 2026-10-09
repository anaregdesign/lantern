package security

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/net/http/httpguts"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

type currentOutputWriter struct {
	mu               sync.Mutex
	request          *currentOutputRequest
	destination      http.ResponseWriter
	ctx              context.Context
	headers, initial http.Header
	trailers         map[string]bool
	status           int
	sent, finished   bool
	frame            uint64
	err              error
	hooks            *authorityOutputHooks
}

func (w *currentOutputWriter) Header() http.Header { return w.headers }
func (w *currentOutputWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sent || w.err != nil {
		return
	}
	if status < 200 || status > 599 {
		w.err = errCurrentOutput
		return
	}
	w.status = status
}

func freezeCurrentHeaders(h http.Header) (http.Header, error) {
	if len(h) > 256 {
		return nil, ErrControlReserve
	}
	bytes := 0
	for key, values := range h {
		name := strings.TrimPrefix(key, http.TrailerPrefix)
		if !httpguts.ValidHeaderFieldName(name) || len(values) > 256 {
			return nil, errCurrentOutput
		}
		bytes += len(key) + 16
		for _, value := range values {
			bytes += len(key) + len(value) + 16
			if !httpguts.ValidHeaderFieldValue(value) {
				return nil, errCurrentOutput
			}
		}
		if bytes > currentOutputHeaderBytes {
			return nil, ErrControlReserve
		}
	}
	return h.Clone(), nil
}

func (w *currentOutputWriter) inventory(h http.Header) (http.Header, http.Header, error) {
	initial, trailers := make(http.Header), make(http.Header)
	declared := w.trailers
	if !w.sent {
		declared = make(map[string]bool)
		for _, value := range h.Values("Trailer") {
			for _, key := range strings.Split(value, ",") {
				name := http.CanonicalHeaderKey(strings.TrimSpace(key))
				if !httpguts.ValidHeaderFieldName(name) || name == "Trailer" || name == "Transfer-Encoding" || name == "Content-Length" {
					return nil, nil, errCurrentOutput
				}
				declared[name] = true
			}
		}
		w.trailers = declared
	}
	for key, values := range h {
		name := strings.TrimPrefix(key, http.TrailerPrefix)
		if strings.HasPrefix(key, http.TrailerPrefix) || declared[http.CanonicalHeaderKey(name)] {
			trailers[name] = values
		} else {
			initial[key] = values
		}
	}
	if w.sent && !reflect.DeepEqual(initial, w.initial) {
		return nil, nil, errCurrentOutput
	}
	return initial, trailers, nil
}

func (w *currentOutputWriter) unit(raw []byte, terminal bool) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	refuse := func(err error) (int, error) { w.err = err; return 0, err }
	if w.finished || len(raw) > w.request.owner.wireBytes || w.frame == ^uint64(0) {
		return refuse(ErrControlReserve)
	}
	headers, err := freezeCurrentHeaders(w.headers)
	if err != nil {
		return refuse(err)
	}
	initial, trailers, err := w.inventory(headers)
	if err != nil {
		return refuse(err)
	}
	// All mutable bytes and protected metadata, exact response stream/socket,
	// sequence, credential/cut and typed resource are fixed before the sample.
	payload := bytes.Clone(raw)
	status := w.status
	r := w.request
	r.mu.Lock()
	grant := r.grant
	r.started = true
	r.mu.Unlock()
	if w.hooks != nil && w.hooks.beforeSample != nil {
		w.hooks.beforeSample()
	}
	now, err := r.authorize(w.ctx, grant)
	if err != nil {
		return refuse(err)
	}
	w.frame++
	if w.hooks != nil && w.hooks.record != nil {
		w.hooks.record(now, w.frame)
	}
	if w.hooks != nil && w.hooks.afterAuthorize != nil {
		w.hooks.afterAuthorize()
	}
	if !w.sent {
		for key, values := range initial {
			w.destination.Header()[key] = values
		}
		w.destination.WriteHeader(status)
		w.initial, w.sent = initial, true
	}
	if terminal {
		for key, values := range trailers {
			name := http.CanonicalHeaderKey(key)
			if !w.trailers[name] {
				name = http.TrailerPrefix + name
			}
			w.destination.Header()[name] = values
		}
		w.finished = true
		return 0, nil
	}
	n, err := w.destination.Write(payload)
	if err != nil || n != len(payload) {
		return refuse(errors.Join(errCurrentOutput, err))
	}
	return n, nil
}

func (w *currentOutputWriter) Write(raw []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.unit(raw, false)
}
func (w *currentOutputWriter) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.finished {
		return nil
	}
	if !w.sent {
		_, err := w.unit(nil, true)
		return err
	}
	headers, err := freezeCurrentHeaders(w.headers)
	if err != nil {
		return err
	}
	_, trailers, err := w.inventory(headers)
	if err != nil {
		return err
	}
	if len(trailers) == 0 {
		w.finished = true
		return nil
	}
	_, err = w.unit(nil, true)
	return err
}

func (w *currentOutputWriter) Flush() { _ = w.FlushError() }
func (w *currentOutputWriter) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if !w.sent {
		if _, err := w.unit(nil, false); err != nil {
			return err
		}
	}
	return http.NewResponseController(w.destination).Flush()
}
func (w *currentOutputWriter) SetWriteDeadline(deadline time.Time) error {
	return http.NewResponseController(w.destination).SetWriteDeadline(deadline)
}
func (w *currentOutputWriter) SetReadDeadline(deadline time.Time) error {
	return http.NewResponseController(w.destination).SetReadDeadline(deadline)
}
func (w *currentOutputWriter) EnableFullDuplex() error {
	return http.NewResponseController(w.destination).EnableFullDuplex()
}

// No Unwrap/Hijack/ReaderFrom exposes an unguarded data-writing destination.
// Only the finite transport's own controller operations are forwarded.
