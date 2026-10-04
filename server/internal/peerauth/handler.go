package peerauth

import (
	"context"
	"encoding/hex"
	"net/http"
	"time"

	"connectrpc.com/connect"
)

const DomainHeader = "Lantern-Peer-Domain"
const membershipCheckInterval = 250 * time.Millisecond

// ProtectHandler wraps only a private peer mux. Every request, including one
// over a reused TLS connection, needs current membership and domain agreement.
// Transport deadlines also stop a blocked write after admission is withdrawn.
func (s *Store) ProtectHandler(next http.Handler, options ...connect.HandlerOption) http.Handler {
	errors := connect.NewErrorWriter(options...)
	refuse := func(w http.ResponseWriter, req *http.Request) {
		_ = errors.Write(w, req, connect.NewError(connect.CodeUnavailable, ErrMembership))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if s == nil || len(req.Header.Values(DomainHeader)) != 1 || len(req.Header.Values("Authorization")) != 0 || len(req.Header.Values("Cookie")) != 0 {
			refuse(w, req)
			return
		}
		digest := s.Domain().Digest()
		if req.Header.Get(DomainHeader) != hex.EncodeToString(digest[:]) {
			refuse(w, req)
			return
		}
		admission, err := s.Admit(req.TLS, "")
		if err != nil {
			refuse(w, req)
			return
		}
		remaining := admission.expiresAt.Sub(s.options.Now())
		if remaining <= 0 {
			refuse(w, req)
			return
		}
		controller := http.NewResponseController(w)
		if controller.SetWriteDeadline(time.Now().Add(remaining)) != nil {
			refuse(w, req)
			return
		}
		ctx, cancel := context.WithTimeout(WithAdmission(req.Context(), admission), remaining)
		stop, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			ticker := time.NewTicker(membershipCheckInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ctx.Done():
					_ = controller.SetWriteDeadline(time.Now())
					return
				case <-ticker.C:
					if admission.Check(ctx) != nil {
						cancel()
						_ = controller.SetWriteDeadline(time.Now())
						return
					}
				}
			}
		}()
		defer func() {
			close(stop)
			<-stopped
			cancel()
			_ = controller.SetWriteDeadline(time.Time{})
		}()
		if admission.Check(ctx) != nil {
			refuse(w, req)
			return
		}
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}
