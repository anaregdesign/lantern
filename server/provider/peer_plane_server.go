package provider

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
	"github.com/anaregdesign/lantern/server/service"
)

// PeerPlaneServer owns only the dedicated workload listener. Public API and
// public credentials are absent from its mux in both authentication modes.
type PeerPlaneServer struct {
	server             *http.Server
	listener           net.Listener
	closeSnapshotTails func()
}

func NewPeerPlaneServer(config PeerPlaneConfig, peer *PeerIdentityRuntime, replication *service.LanternReplicationService, limits NetConfig, logger *slog.Logger, certified runtimeCertified, launch *listenerlaunch.Owner) (*PeerPlaneServer, func(), error) {
	if !certified.valid || certified.replication != replication || certified.replicationSendMaxBytes != limits.MaxSendMsgBytes {
		return nil, nil, errors.New("private listener requires the exact certified replication service and frame limit")
	}
	closeTails := func() {
		if replication != nil {
			replication.CloseSnapshotTails()
		}
	}
	if config.ListenAddress == "" {
		if err := launch.NoPeer(); err != nil {
			return nil, nil, err
		}
		if peer != nil {
			return nil, nil, errors.New("private runtime without a configured listener")
		}
		return &PeerPlaneServer{closeSnapshotTails: closeTails}, closeTails, nil
	}
	if peer == nil || replication == nil || peer.CheckWorkload(context.Background()) != nil {
		return nil, nil, errors.New("private listener requires certified workload and replication runtime")
	}
	options := []connect.HandlerOption{connect.WithReadMaxBytes(limits.MaxRecvMsgBytes), connect.WithSendMaxBytes(limits.MaxSendMsgBytes), service.StrictJSONHandlerOption()}
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(service.NewLanternReplicationServiceConnectHandler(replication), options...))
	var listener net.Listener
	var err error
	if launch != nil {
		listener, err = launch.Peer(config.ListenAddress)
	} else {
		listener, err = net.Listen("tcp", config.ListenAddress)
	}
	if err != nil {
		return nil, nil, err
	}
	server := &http.Server{Handler: peer.ProtectPeerHTTPHandler(mux, options...), TLSConfig: peer.TLSConfig(), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	logger.Info("private workload listener configured", slog.String("addr", listener.Addr().String()))
	return &PeerPlaneServer{server: server, listener: listener, closeSnapshotTails: closeTails}, func() {
		closeTails()
		_ = listener.Close()
	}, nil
}
func (p *PeerPlaneServer) Run(ctx context.Context) error {
	if p == nil || p.server == nil {
		<-ctx.Done()
		if p != nil && p.closeSnapshotTails != nil {
			p.closeSnapshotTails()
		}
		return nil
	}
	owned, cancelOwned := context.WithCancel(ctx)
	defer cancelOwned()
	// App cancels this owner after its readiness drain. Wake long-lived
	// subscriptions, then let Shutdown join their handlers before returning.
	p.server.BaseContext = func(net.Listener) context.Context { return owned }
	stopped := make(chan error, 1)
	go func() {
		<-owned.Done()
		if p.closeSnapshotTails != nil {
			p.closeSnapshotTails()
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := p.server.Shutdown(stopCtx)
		if err != nil {
			err = errors.Join(err, p.server.Close())
		}
		stopped <- err
	}()
	err := p.server.ServeTLS(p.listener, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		if ctx.Err() == nil {
			err = errors.New("private listener stopped without shutdown request")
		} else {
			err = nil
		}
	}
	cancelOwned()
	return errors.Join(err, <-stopped)
}
