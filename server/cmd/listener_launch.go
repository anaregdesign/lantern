package main

import (
	"bufio"
	"context"
	"os"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
)

// listenerLaunch is a local composition-root capability. The production child
// itself allocates the sockets; no OS descriptor or poller changes ownership.
// The supervisor supplies generated configuration over its private stdin pipe.
// Pipe EOF/cancellation closes the launch, including before certification.
type listenerLaunch struct {
	owner  *listenerlaunch.Owner
	nonce  string
	stopIO func() bool
}

func prepareListenerLaunch(ctx context.Context, cancel context.CancelFunc) (*listenerLaunch, error) {
	launch := &listenerLaunch{}
	if len(os.Args) == 1 {
		return launch, nil
	}
	if len(os.Args) != 3 || os.Args[1] != listenerlaunch.Argument || !listenerlaunch.ValidNonce(os.Args[2]) {
		return nil, listenerlaunch.ErrLaunch
	}
	launch.nonce = os.Args[2]
	launch.stopIO = context.AfterFunc(ctx, func() { _ = os.Stdin.Close() })
	// A dead/unresponsive local supervisor cannot strand pre-certification
	// reservations. Its own configured readiness deadline can be shorter.
	timer := time.AfterFunc(time.Minute, func() { _ = os.Stdin.Close(); cancel() })
	defer timer.Stop()
	input := bufio.NewReaderSize(os.Stdin, listenerlaunch.MaxFrame)
	request, err := listenerlaunch.Read(input)
	if err != nil || !request.Control("reserve", launch.nonce) {
		launch.Close()
		return nil, listenerlaunch.ErrLaunch
	}
	launch.owner, err = listenerlaunch.Reserve(ctx, request.Private)
	if err != nil {
		launch.Close()
		return nil, err
	}
	public, peer := launch.owner.Addresses()
	if err = listenerlaunch.Write(os.Stdout, listenerlaunch.Message{Phase: "reserved", Nonce: launch.nonce, PID: os.Getpid(), Public: public, Peer: peer}); err != nil {
		launch.Close()
		return nil, err
	}
	configuration, err := listenerlaunch.Read(input)
	if err != nil || ctx.Err() != nil || !configuration.Configuration(launch.nonce) {
		launch.Close()
		return nil, listenerlaunch.ErrLaunch
	}
	// The complete launch environment replaces inherited Lantern settings;
	// application env validation and every existing certification still run.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "LANTERN_") {
			_ = os.Unsetenv(key)
		}
	}
	for _, entry := range configuration.Environment {
		key, value, _ := strings.Cut(entry, "=")
		if err := os.Setenv(key, value); err != nil {
			launch.Close()
			return nil, listenerlaunch.ErrLaunch
		}
	}
	go func() {
		// There are no further commands. EOF, excess input or a broken pipe
		// cancels the child; the parent remains its lifetime owner.
		_, _ = input.ReadByte()
		cancel()
	}()
	return launch, nil
}

func (l *listenerLaunch) acknowledge() error {
	if l.owner == nil {
		return nil
	}
	if err := l.owner.Adopted(); err != nil {
		return err
	}
	return listenerlaunch.Write(os.Stdout, listenerlaunch.Message{Phase: "adopted", Nonce: l.nonce, PID: os.Getpid()})
}

func (l *listenerLaunch) Close() {
	if l == nil {
		return
	}
	if l.stopIO != nil {
		l.stopIO()
		_ = os.Stdin.Close()
	}
	l.owner.Close()
}
