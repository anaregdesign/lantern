package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

// receiptHALaunch owns the complete fixture cohort and its stable relay
// frontends. Child capabilities never imply runtime readiness or authority.
type receiptHALaunch struct {
	launches []*fixtureLaunch
	peers    []net.Listener
	public   *receiptHAFrontend
	relays   *receiptHADataRelays
	deadline time.Time
	once     sync.Once
}

func reserveReceiptHALaunch(ctx context.Context, binary, directory string, timeout time.Duration) (_ *receiptHALaunch, public, peer []int, err error) {
	h := &receiptHALaunch{}
	defer func() {
		if err != nil {
			h.close()
		}
	}()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, nil, err
	}
	h.public = newReceiptHAFrontend(listener)
	for i := 0; i < 4; i++ {
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return nil, nil, nil, err
		}
		h.peers = append(h.peers, listener)
		peer = append(peer, listener.Addr().(*net.TCPAddr).Port)
	}
	h.launches, public, _, err = reserveFixtureCohort(ctx, binary, directory, 4, true, timeout)
	if err != nil {
		return nil, nil, nil, err
	}
	h.deadline = h.launches[0].deadline
	public[3] = h.public.listener.Addr().(*net.TCPAddr).Port
	return h, public, peer, nil
}

func (h *receiptHALaunch) bindNodes(f *fixture) error {
	if h == nil || len(h.launches) != 4 || len(h.peers) != 4 || f == nil || len(f.Nodes) != 4 {
		return errors.New("exact owned HA cohort required")
	}
	for i := range f.Nodes {
		f.Nodes[i].Environment["LANTERN_PORT"] = strconv.Itoa(h.launches[i].public)
		f.Nodes[i].Environment["LANTERN_PEER_LISTEN_ADDR"] = net.JoinHostPort("127.0.0.1", strconv.Itoa(h.launches[i].peer))
	}
	return nil
}

func (h *receiptHALaunch) children() []*fixtureProcess {
	children := make([]*fixtureProcess, len(h.launches))
	for i, l := range h.launches {
		children[i] = l.process
	}
	return children
}

func (h *receiptHALaunch) configure(ctx context.Context, f fixture) error {
	if len(f.Nodes) != 4 || len(h.launches) != 4 {
		return errors.New("exact owned HA cohort required")
	}
	ctx, cancel := context.WithDeadline(ctx, h.deadline)
	defer cancel()
	var failure error
	var first sync.Once
	var workers sync.WaitGroup
	for i, node := range f.Nodes {
		workers.Add(1)
		go func() {
			defer workers.Done()
			launch := h.launches[i]
			err := checkFixtureLaunchNode(launch, node)
			var env []string
			if err == nil {
				env, err = fixtureEnvironment(node, nil)
			}
			started := time.Now()
			if err == nil {
				err = launch.configure(ctx, env)
			}
			if err != nil {
				first.Do(func() {
					failure = fixtureAdoptionFailure(err, launch.process, i, launch.public, time.Since(started))
					cancel()
				})
			}
		}()
	}
	workers.Wait()
	return failure
}

// orderlyStop is distinct from best-effort cleanup. A killed or failed child
// cannot supply the normal shutdown/custody premise needed for a resume.
func (l *fixtureLaunch) orderlyStop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_ = l.input.Close()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.process.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		if l.process.command.ProcessState == nil || !l.process.command.ProcessState.Success() {
			return errors.New("HA child did not shut down successfully")
		}
		_ = l.output.Close()
		_ = l.process.log.Close()
		return nil
	}
}

func (h *receiptHALaunch) close() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		if h.public != nil {
			h.public.close()
		}
		if h.relays != nil {
			h.relays.close()
		}
		closeFixtureLaunches(h.launches)
		for i := len(h.peers) - 1; i >= 0; i-- {
			_ = h.peers[i].Close()
		}
	})
}
