package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/privatefile"
	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
)

// fixtureLaunch retains the pipe capability and reaps its one production child.
// The child owns both listeners continuously; the supervisor never accepts or
// proxies. The pipe remains open after adoption as a parent lifetime signal.
type fixtureLaunch struct {
	process      *fixtureProcess
	input        io.WriteCloser
	output       io.ReadCloser
	reader       *bufio.Reader
	nonce        string
	public, peer int
	deadline     time.Time
}

func (l *fixtureLaunch) close() {
	_ = l.input.Close()
	_ = l.output.Close()
	l.process.stop()
}

func (l *fixtureLaunch) exchange(ctx context.Context, request listenerlaunch.Message, phase string) (listenerlaunch.Message, error) {
	ctx, cancel := context.WithDeadline(ctx, l.deadline)
	defer cancel()
	if ctx.Err() != nil {
		return listenerlaunch.Message{}, ctx.Err()
	}
	done := make(chan struct{})
	interrupt := context.AfterFunc(ctx, func() {
		_ = l.input.Close()
		_ = l.output.Close()
		close(done)
	})
	defer func() {
		if !interrupt() {
			<-done
		}
	}()
	if err := listenerlaunch.Write(l.input, request); err != nil {
		return listenerlaunch.Message{}, err
	}
	response, err := listenerlaunch.Read(l.reader)
	if err != nil || ctx.Err() != nil || response.Nonce != l.nonce || response.Phase != phase || response.PID != l.process.command.Process.Pid || response.Private || len(response.Environment) != 0 {
		return listenerlaunch.Message{}, listenerlaunch.ErrLaunch
	}
	return response, nil
}

func (l *fixtureLaunch) configure(ctx context.Context, env []string) error {
	var native []string
	for _, entry := range env {
		if strings.HasPrefix(entry, "LANTERN_") {
			native = append(native, entry)
		}
	}
	response, err := l.exchange(ctx, listenerlaunch.Message{Phase: "configure", Nonce: l.nonce, Environment: native}, "adopted")
	if err != nil || response.Public != "" || response.Peer != "" {
		return listenerlaunch.ErrLaunch
	}
	return nil
}

func startFixtureLaunch(ctx context.Context, binary, dir string, private bool, deadline time.Time) (*fixtureLaunch, error) {
	if !filepath.IsAbs(binary) || !filepath.IsAbs(dir) {
		return nil, errors.New("absolute Server binary required")
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	log, err := privatefile.CreateTemp(filepath.Dir(dir), "listener-launch-")
	if err != nil {
		return nil, err
	}
	command := exec.Command(binary, listenerlaunch.Argument, hex.EncodeToString(token[:]))
	command.Env, err = fixtureEnvironment(fixtureNode{}, nil)
	if err != nil {
		_ = log.Close()
		return nil, err
	}
	command.Stderr = log
	input, err := command.StdinPipe()
	if err != nil {
		_ = log.Close()
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		_ = log.Close()
		return nil, err
	}
	if err = command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		_ = log.Close()
		return nil, err
	}
	process := &fixtureProcess{command: command, log: log, done: make(chan struct{})}
	go func() { _ = command.Wait(); close(process.done) }()
	launch := &fixtureLaunch{process: process, input: input, output: output, reader: bufio.NewReaderSize(output, listenerlaunch.MaxFrame), nonce: hex.EncodeToString(token[:]), deadline: deadline}
	response, err := launch.exchange(ctx, listenerlaunch.Message{Phase: "reserve", Nonce: launch.nonce, Private: private}, "reserved")
	if err == nil {
		launch.public, err = fixtureReservedPort(response.Public, false)
		if err == nil && private {
			launch.peer, err = fixtureReservedPort(response.Peer, true)
		}
		if err == nil && ((!private && response.Peer != "") || launch.peer == launch.public) {
			err = listenerlaunch.ErrLaunch
		}
	}
	if err != nil {
		launch.close()
		return nil, err
	}
	return launch, nil
}

func fixtureReservedPort(raw string, private bool) (int, error) {
	addr, err := netip.ParseAddrPort(raw)
	if err != nil || addr.Port() == 0 || addr.Addr().Zone() != "" || addr.String() != raw {
		return 0, listenerlaunch.ErrLaunch
	}
	if private && addr.Addr() != netip.AddrFrom4([4]byte{127, 0, 0, 1}) || !private && !addr.Addr().IsUnspecified() {
		return 0, listenerlaunch.ErrLaunch
	}
	return int(addr.Port()), nil
}

func reserveFixtureCohort(ctx context.Context, binary, dir string, count int, private bool, timeout time.Duration) ([]*fixtureLaunch, []int, []int, error) {
	if count < 1 || count > 8 || count > 1 && !private || timeout < time.Second || timeout > 5*time.Minute {
		return nil, nil, nil, listenerlaunch.ErrLaunch
	}
	var launches []*fixtureLaunch
	var public, peer []int
	deadline := time.Now().Add(timeout)
	for i := 0; i < count; i++ {
		launch, err := startFixtureLaunch(ctx, binary, dir, private, deadline)
		if err != nil {
			closeFixtureLaunches(launches)
			return nil, nil, nil, err
		}
		launches = append(launches, launch)
		public = append(public, launch.public)
		if private {
			peer = append(peer, launch.peer)
		}
	}
	return launches, public, peer, nil
}

func closeFixtureLaunches(launches []*fixtureLaunch) {
	for i := len(launches) - 1; i >= 0; i-- {
		launches[i].close()
	}
}

func checkFixtureLaunchNode(l *fixtureLaunch, node fixtureNode) error {
	if node.Environment["LANTERN_PORT"] != strconv.Itoa(l.public) {
		return listenerlaunch.ErrLaunch
	}
	wantPeer := ""
	if l.peer != 0 {
		wantPeer = net.JoinHostPort("127.0.0.1", strconv.Itoa(l.peer))
	}
	if node.Environment["LANTERN_PEER_LISTEN_ADDR"] != wantPeer {
		return listenerlaunch.ErrLaunch
	}
	return nil
}
