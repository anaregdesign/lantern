package security

import (
	"context"
	"net"
	"net/http"
	"testing"
)

func TestAuthorityOutputPoolOwnsFiniteConnectionsAndEncoders(t *testing.T) {
	_, owners, _ := authorityTestComposite(t)
	o := owners[1]
	var connections []*authorityOutputConnection
	for range authorityOutputConnections {
		a, b := net.Pipe()
		t.Cleanup(func() { a.Close(); b.Close() })
		ctx := o.outputConnContext(context.Background(), a)
		c, _ := ctx.Value(authorityOutputContextKey{}).(*authorityOutputConnection)
		if c == nil {
			t.Fatal("bounded connection refused")
		}
		connections = append(connections, c)
	}
	a, b := net.Pipe()
	defer b.Close()
	defer a.Close()
	if c := o.outputConnContext(context.Background(), a).Value(authorityOutputContextKey{}); c != nil {
		t.Fatal("connection bound bypass")
	}
	for _, c := range connections[:authorityOutputActive] {
		if !o.reserveOutput(c) {
			t.Fatal("encoder reserve")
		}
	}
	if o.reserveOutput(connections[0]) || o.reserveOutput(connections[authorityOutputActive]) {
		t.Fatal("credit spent twice or unlimited writers")
	}
	if o.outputs.bytes > authorityOutputPoolBytes {
		t.Fatal("global byte bound")
	}
	o.outputConnState(connections[0].conn, http.StateClosed)
	if o.outputs.connections[connections[0].conn] == nil {
		t.Fatal("entered writer credit released on early socket close")
	}
	for _, c := range connections[:authorityOutputActive] {
		o.releaseOutput(c)
	}
	if o.outputs.active != 0 {
		t.Fatal("encoder leak")
	}
	o.stopOutputs()
	if o.reserveOutput(connections[authorityOutputActive]) {
		t.Fatal("closed output pool")
	}
}

func TestAuthorityOutputRequirementRejectsUnboundedOrUnknownScope(t *testing.T) {
	for _, r := range []authorityOutputRequirement{{action: SecurityManage, keys: []string{"x"}}, {action: VertexRead}, {action: "sys.put"}, {action: VertexRead, keys: make([]string, 1025)}} {
		if _, err := r.freeze(); err == nil {
			t.Fatal("unbounded or invalid output scope")
		}
	}
	keys := []string{"orders:1"}
	f, err := (authorityOutputRequirement{VertexRead, keys}).freeze()
	if err != nil {
		t.Fatal(err)
	}
	keys[0] = "secret:2"
	if f.keys[0] != "orders:1" {
		t.Fatal("scope was mutable")
	}
}
