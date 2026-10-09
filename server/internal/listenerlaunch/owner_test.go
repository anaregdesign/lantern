package listenerlaunch

import (
	"net"
	"strconv"
	"testing"
)

func TestReservedListenerOwnership(t *testing.T) {
	o, err := Reserve(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	public, peer := o.Addresses()
	for _, address := range []string{public, peer} {
		competitor, err := net.Listen("tcp", address)
		if err == nil {
			_ = competitor.Close()
			t.Fatal("competitor took reserved socket", address)
		}
	}
	publicAddr := o.public.Addr().(*net.TCPAddr)
	if _, err := o.Public(publicAddr.Port + 1); err == nil {
		t.Fatal("wrong public endpoint accepted")
	}
	if _, err := o.Peer(public); err == nil {
		t.Fatal("swapped private endpoint accepted")
	}
	if o.NoPeer() == nil || o.Adopted() == nil {
		t.Fatal("unconsumed capability accepted")
	}
	listener, err := o.Public(publicAddr.Port)
	if err != nil || listener != o.public {
		t.Fatal("public socket replaced", err)
	}
	if _, err := o.Public(publicAddr.Port); err == nil {
		t.Fatal("duplicate public adoption")
	}
	private, err := o.Peer(peer)
	if err != nil || private != o.peer || o.Adopted() != nil {
		t.Fatal("private socket replaced or incomplete adoption", err)
	}
	for _, address := range []string{public, peer} {
		competitor, err := net.Listen("tcp", address)
		if err == nil {
			_ = competitor.Close()
			t.Fatal("competitor took adopted socket", address)
		}
	}
	o.Close()
	if _, err := o.Public(publicAddr.Port); err == nil || o.Adopted() == nil {
		t.Fatal("closed socket adopted")
	}
	for _, address := range []string{public, peer} {
		replacement, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal("closed owner retained endpoint", err)
		}
		_ = replacement.Close()
	}
	// The earlier close/rebind strategy admits a competing owner. This negative
	// control establishes that the conflict checks above exercise real binding.
	released, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := released.Addr().String()
	_ = released.Close()
	competitor, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = competitor.Close() }()
	if victim, err := net.Listen("tcp", address); err == nil {
		_ = victim.Close()
		t.Fatal("negative control did not conflict")
	}
}

func TestReservedListenerRoleGuards(t *testing.T) {
	o, err := Reserve(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	o.public, o.peer = o.peer, o.public
	if _, err := o.Public(o.public.Addr().(*net.TCPAddr).Port); err == nil {
		t.Fatal("loopback private socket adopted as public wildcard")
	}
	if _, err := o.Peer(net.JoinHostPort("127.0.0.1", strconv.Itoa(o.peer.Addr().(*net.TCPAddr).Port))); err == nil {
		t.Fatal("wildcard public socket adopted as private")
	}
	empty := &Owner{}
	if _, err := empty.Public(1); err == nil {
		t.Fatal("missing socket accepted")
	}
	if _, err := empty.Peer("127.0.0.1:1"); err == nil {
		t.Fatal("missing private socket accepted")
	}
}
