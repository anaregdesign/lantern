package provider

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// PeerPlaneServer owns only the dedicated workload listener. Public API and
// public credentials are absent from its mux in both authentication modes.
type PeerPlaneServer struct {
	server   *http.Server
	listener net.Listener
}

func NewPeerPlaneServer(config PeerPlaneConfig, peer *PeerIdentityRuntime, policy *SecurityPeerRuntime, replication *service.LanternReplicationService, limits NetConfig, logger *slog.Logger, certified runtimeCertified) (*PeerPlaneServer, func(), error) {
	if !certified.valid || certified.replication != replication || certified.replicationSendMaxBytes != limits.MaxSendMsgBytes {
		return nil, nil, errors.New("private listener requires the exact certified replication service and frame limit")
	}
	if config.ListenAddress == "" {
		if peer != nil || policy != nil {
			return nil, nil, errors.New("private runtime without a configured listener")
		}
		return &PeerPlaneServer{}, func() {}, nil
	}
	if policy != nil && (policy.peer != peer || policy.runtime == nil || policy.runtime.peer != peer || policy.runtime.data != certified.runtime) {
		return nil, nil, errors.New("private policy service must own the certified workload and data runtime")
	}
	if peer == nil || replication == nil || peer.CheckWorkload(context.Background()) != nil {
		return nil, nil, errors.New("private listener requires certified workload and replication runtime")
	}
	options := []connect.HandlerOption{connect.WithReadMaxBytes(limits.MaxRecvMsgBytes), connect.WithSendMaxBytes(limits.MaxSendMsgBytes), service.StrictJSONHandlerOption()}
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(service.NewLanternReplicationServiceConnectHandler(replication), options...))
	if policy != nil {
		mux.Handle(policy.PrivateHTTPHandler())
	}
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return nil, nil, err
	}
	server := &http.Server{Handler: peer.ProtectPeerHTTPHandler(mux, options...), TLSConfig: peer.TLSConfig(), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	logger.Info("private workload listener configured", slog.String("addr", listener.Addr().String()))
	return &PeerPlaneServer{server: server, listener: listener}, func() { _ = listener.Close() }, nil
}
func (p *PeerPlaneServer) Run(ctx context.Context) error {
	if p == nil || p.server == nil {
		<-ctx.Done()
		return nil
	}
	owned, cancelOwned := context.WithCancel(ctx)
	defer cancelOwned()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-owned.Done()
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if p.server.Shutdown(stopCtx) != nil {
			_ = p.server.Close()
		}
	}()
	err := p.server.ServeTLS(p.listener, "", "")
	cancelOwned()
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
