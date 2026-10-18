package main

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// receiptHAFrontend keeps C's public endpoint bound across an OS-process
// restart. It copies opaque TCP bytes: TLS and public admission remain in Server.
type receiptHAFrontend struct {
	listener net.Listener
	mu       sync.Mutex
	target   *receiptHATunnelGeneration
	closed   bool
	done     chan struct{}
}

type receiptHATunnelGeneration struct {
	ctx         context.Context
	cancel      context.CancelFunc
	address     string
	mu          sync.Mutex
	closed      bool
	connections map[net.Conn]struct{}
	workers     sync.WaitGroup
	active      int
}

func newReceiptHAFrontend(listener net.Listener) *receiptHAFrontend {
	f := &receiptHAFrontend{listener: listener, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			g := f.target
			if g != nil {
				g.mu.Lock()
				if g.closed || g.active >= 32 {
					g.mu.Unlock()
					g = nil
				} else {
					g.active++
					g.connections[conn] = struct{}{}
					g.workers.Add(1)
					g.mu.Unlock()
				}
			}
			f.mu.Unlock()
			if g == nil {
				_ = conn.Close()
				continue
			}
			go g.forward(conn)
		}
	}()
	return f
}

func (f *receiptHAFrontend) resume(launch *fixtureLaunch) error {
	if launch == nil || launch.public < 1 || launch.public > 65535 || launch.peer == 0 {
		return errors.New("owned HA child listeners required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.target != nil {
		return errors.New("public forwarding generation still active")
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.target = &receiptHATunnelGeneration{ctx: ctx, cancel: cancel, address: net.JoinHostPort("127.0.0.1", strconv.Itoa(launch.public)), connections: map[net.Conn]struct{}{}}
	return nil
}

func (f *receiptHAFrontend) pause() {
	f.mu.Lock()
	g := f.target
	f.target = nil
	f.mu.Unlock()
	if g == nil {
		return
	}
	g.cancel()
	g.mu.Lock()
	g.closed = true
	for conn := range g.connections {
		_ = conn.Close()
	}
	g.mu.Unlock()
	g.workers.Wait()
}

func (f *receiptHAFrontend) close() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	_ = f.listener.Close()
	<-f.done
	f.pause()
}

func (g *receiptHATunnelGeneration) forward(down net.Conn) {
	var up net.Conn
	defer func() {
		_ = down.Close()
		if up != nil {
			_ = up.Close()
		}
		g.mu.Lock()
		delete(g.connections, down)
		delete(g.connections, up)
		g.active--
		g.mu.Unlock()
		g.workers.Done()
	}()
	var err error
	up, err = (&net.Dialer{Timeout: 5 * time.Second}).DialContext(g.ctx, "tcp", g.address)
	if err != nil {
		return
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.connections[up] = struct{}{}
	g.mu.Unlock()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, down); done <- struct{}{} }()
	go func() { _, _ = io.Copy(down, up); done <- struct{}{} }()
	<-done
	_ = down.Close()
	_ = up.Close()
	<-done
}
