package integration_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
)

type lifecycleWireListener struct {
	server   *http.Server
	listener net.Listener
}

func (l lifecycleWireListener) Server() *http.Server   { return l.server }
func (l lifecycleWireListener) Listener() net.Listener { return l.listener }
func (l lifecycleWireListener) TLSEnabled() bool       { return false }

func TestLifecyclePolicyRealConnectShutdownUsesSharedFrozenGeneration(t *testing.T) {
	for _, bounded := range []bool{true, false} {
		t.Run(map[bool]string{true: "frozen deadline", false: "zero has no forced deadline"}[bounded], func(t *testing.T) {
			ctx, cancelTest := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancelTest()
			owner, err := service.NewLifecyclePolicyOwner(service.LifecyclePolicySnapshot{Generation: 1})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := service.NewShutdownPlan(owner)
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			releaseRequest := sync.OnceFunc(func() { close(release) })
			defer releaseRequest()
			block := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
				return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
					}
					return next(ctx, req)
				}
			})
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
			svc := service.NewLanternService(cache)
			_, handler := graphv1connect.NewLanternServiceHandler(service.NewLanternServiceConnectHandler(svc), connect.WithInterceptors(block))
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			httpServer := &http.Server{Handler: handler}
			t.Cleanup(func() { _ = httpServer.Close() })
			shutdownStarted := make(chan struct{})
			httpServer.RegisterOnShutdown(func() { close(shutdownStarted) })
			server := service.NewLanternServer(lifecycleWireListener{httpServer, listener}, slog.New(slog.NewTextHandler(io.Discard, nil)),
				service.LifecycleConfig{ShutdownTimeout: time.Millisecond}, nil, nil, svc, nil).WithShutdownPlan(plan)
			runCtx, stop := context.WithCancel(ctx)
			defer stop()
			done := make(chan error, 1)
			go func() { done <- server.Run(runCtx) }()
			client := graphv1connect.NewLanternServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
			request := make(chan error, 1)
			go func() {
				_, err := client.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
				request <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			selected := service.LifecyclePolicySnapshot{Generation: 2}
			if bounded {
				selected.Policy.ShutdownTimeout = time.Second
			}
			if err := owner.Apply(selected); err != nil {
				t.Fatal(err)
			}
			// App's first shutdown action captures this exact shared object.
			if plan.Capture() != selected {
				t.Fatal("server construction captured lifecycle policy too early")
			}
			if err := owner.Apply(service.LifecyclePolicySnapshot{Generation: 3}); err != nil {
				t.Fatal(err)
			}
			stop()
			select {
			case <-shutdownStarted:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if !bounded {
				select {
				case err := <-done:
					t.Fatal("zero timeout inherited forced bootstrap deadline", err)
				case <-time.After(20 * time.Millisecond):
				}
				// A completed request lets the unbounded HTTP drain finish.
				releaseRequest()
			}
			select {
			case err := <-done:
				if bounded && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("frozen HTTP deadline not enforced", err)
				}
				if !bounded && err != nil {
					t.Fatal("zero-timeout shutdown failed", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP shutdown did not finish")
			}
			if err := <-request; !bounded && err != nil {
				t.Fatal("unbounded drain did not finish its admitted Connect request", err)
			}
			if plan.Capture() != selected {
				t.Fatal("shutdown plan changed during HTTP draining")
			}
		})
	}
}
