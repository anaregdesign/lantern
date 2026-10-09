// Package listenerlaunch owns sockets reserved by a production child for one
// local fixture launch. Reservation is not runtime certification or readiness.
package listenerlaunch

import (
	"context"
	"errors"
	"net"
	"strconv"
)

var ErrLaunch = errors.New("invalid native listener launch")

// Owner never accepts. Its concrete TCP sockets remain in the same process and
// Go poller from port allocation through certified provider adoption on every
// platform, including Windows. It accepts no caller-supplied handles.
// The composition root serializes adoption and closes Owner after app cleanup.
type Owner struct {
	public, peer           *net.TCPListener
	publicTaken, peerTaken bool
	closed                 bool
}

func Reserve(ctx context.Context, private bool) (*Owner, error) {
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "tcp", ":0")
	if err != nil {
		return nil, err
	}
	o := &Owner{public: listener.(*net.TCPListener)}
	if private {
		listener, err = config.Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			o.Close()
			return nil, err
		}
		o.peer = listener.(*net.TCPListener)
	}
	return o, nil
}

func (o *Owner) Addresses() (string, string) {
	public, peer := "", ""
	if o != nil && o.public != nil {
		public = o.public.Addr().String()
	}
	if o != nil && o.peer != nil {
		peer = o.peer.Addr().String()
	}
	return public, peer
}

// Public is called only after the ordinary runtime, public receipt and frame
// guards. A requested launch never falls back to a new bind on any error.
func (o *Owner) Public(port int) (net.Listener, error) {
	if o == nil || o.public == nil || o.closed || o.publicTaken {
		return nil, ErrLaunch
	}
	addr, ok := o.public.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsUnspecified() || addr.Zone != "" || addr.Port != port || port < 1 {
		return nil, ErrLaunch
	}
	o.publicTaken = true
	return o.public, nil
}

// Peer is called only after the ordinary exact runtime, replication, policy
// identity and live workload guards. The only fixture scope is IPv4 loopback.
func (o *Owner) Peer(address string) (net.Listener, error) {
	if o == nil || o.peer == nil || o.closed || o.peerTaken {
		return nil, ErrLaunch
	}
	addr, ok := o.peer.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) || addr.Zone != "" || addr.Port < 1 || address != net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.Port)) {
		return nil, ErrLaunch
	}
	o.peerTaken = true
	return o.peer, nil
}

func (o *Owner) NoPeer() error {
	if o != nil && (o.closed || o.peer != nil) {
		return ErrLaunch
	}
	return nil
}

func (o *Owner) Adopted() error {
	if o == nil || o.public == nil || o.closed || !o.publicTaken || (o.peer != nil) != o.peerTaken {
		return ErrLaunch
	}
	return nil
}

func (o *Owner) Close() {
	if o == nil {
		return
	}
	o.closed = true
	if o.public != nil {
		_ = o.public.Close()
	}
	if o.peer != nil {
		_ = o.peer.Close()
	}
}
