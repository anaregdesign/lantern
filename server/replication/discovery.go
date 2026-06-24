package replication

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
)

// PeerSource resolves the current set of peer addresses the pump
// should subscribe to. Implementations may be static (read once at
// construction) or dynamic (e.g. periodic DNS lookups for headless
// k8s services / Compose service names / Nomad+Consul DNS / plain
// A-record fleets — see #190).
//
// Each Resolve call MUST return an absolute set: the supervisor
// reconciles by diffing the returned set against the currently-active
// peer goroutines and adding / cancelling as needed. Returning the
// same set every call is correct and cheap — the supervisor only
// acts on differences.
//
// Returned addresses are passed to the Connect peer client verbatim,
// so each entry MUST already be in "host:port" form. Implementations
// are responsible for any self-filtering (so the local node does not
// subscribe to itself); the pump's mutation-level self-echo guard
// is a defence-in-depth backstop, not a substitute.
type PeerSource interface {
	Resolve(ctx context.Context) ([]string, error)
}

// StaticSource is a trivial PeerSource that returns the same address
// list on every call. It models the historical
// LANTERN_PEERS=host1:port,host2:port behaviour and is what
// NewPump.Config.Peers boils down to internally when no explicit
// Source is supplied.
type StaticSource struct {
	Peers []string
}

// Resolve returns a defensive copy of the configured peers.
func (s StaticSource) Resolve(context.Context) ([]string, error) {
	out := make([]string, len(s.Peers))
	copy(out, s.Peers)
	return out, nil
}

// HostLookup is the minimal surface DNSSource needs to resolve a
// hostname to a set of A/AAAA addresses. *net.Resolver satisfies it
// directly; tests inject fakes that return a controlled slice and
// optionally error.
type HostLookup interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// DNSSource resolves a single DNS name to its current A/AAAA
// records, applies an optional self-IP filter (so the local node
// does not subscribe to its own listener), and emits the surviving
// addresses as "ip:port" strings ready for the Connect peer client.
//
// The DNS source is platform-neutral: it works against k8s headless
// Services (`lantern-headless.<ns>.svc.cluster.local`), Docker
// Compose service names (Compose's embedded DNS returns one A per
// replica), HashiCorp Nomad service discovery via Consul DNS, or any
// plain DNS round-robin A-record list.
//
// Platforms that do not expose per-instance DNS remain explicitly
// unsupported as multi-instance topologies — see RFC #175 §D7. Those
// deployments keep using single-instance mode (empty peer list).
type DNSSource struct {
	// Name is the DNS hostname to resolve (no port). Example:
	// "lantern-headless.default.svc.cluster.local".
	Name string

	// Port is the wire port appended to every resolved IP. Must
	// be non-empty (the wire protocol has no default port).
	Port string

	// Lookup performs the actual hostname resolution. nil means
	// net.DefaultResolver, which honours /etc/resolv.conf and the
	// platform DNS stack.
	Lookup HostLookup

	// SelfIPs is the set of IP literals that identify the local
	// node's own listener interfaces. Resolved addresses present
	// in this set are filtered out before emission. nil disables
	// self-filtering (acceptable in tests; production wiring
	// passes LocalIPSet()).
	SelfIPs map[string]struct{}
}

// Resolve returns the current peer set: every resolved address from
// Name, with self-IPs removed, each formatted as "ip:port". A
// resolution error is propagated verbatim — the supervisor logs and
// keeps the previous set live, so a transient DNS failure does NOT
// tear down established peer streams.
func (d *DNSSource) Resolve(ctx context.Context) ([]string, error) {
	if d.Name == "" {
		return nil, errors.New("dns source: empty Name")
	}
	if d.Port == "" {
		return nil, errors.New("dns source: empty Port")
	}
	lookup := d.Lookup
	if lookup == nil {
		lookup = net.DefaultResolver
	}
	ips, err := lookup.LookupHost(ctx, d.Name)
	if err != nil {
		return nil, fmt.Errorf("dns source: lookup %q: %w", d.Name, err)
	}
	out := make([]string, 0, len(ips))
	seen := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if _, self := d.SelfIPs[ip]; self {
			continue
		}
		addr := net.JoinHostPort(ip, d.Port)
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out, nil
}

// LocalIPSet enumerates the IPs assigned to every up, non-loopback
// network interface on this host. The returned set is intended for
// DNSSource.SelfIPs so the pump never subscribes to its own
// listener when a headless-Service A-record list happens to include
// the local pod IP.
//
// Loopback addresses are excluded — they cannot appear in a
// headless-Service or Compose-network resolution anyway, and
// dropping them keeps the set minimal. Both IPv4 and IPv6 literals
// are emitted in their canonical String() form so they match the
// strings net.Resolver.LookupHost returns.
func LocalIPSet() (map[string]struct{}, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{})
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		if ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			out[ip.String()] = struct{}{}
		}
	}
	return out, nil
}

// peerSupervisor manages the dynamic set of per-peer pump
// goroutines. One supervisor instance is created per Pump.Run call.
type peerSupervisor struct {
	run func(ctx context.Context, addr string)

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	wg      sync.WaitGroup
}

func newPeerSupervisor(run func(ctx context.Context, addr string)) *peerSupervisor {
	return &peerSupervisor{run: run, cancels: make(map[string]context.CancelFunc)}
}

// reconcile starts goroutines for any address in want that is not
// currently active and cancels goroutines for any active address
// missing from want. The parent ctx scopes all spawned per-peer
// contexts.
func (s *peerSupervisor) reconcile(parent context.Context, want []string) {
	wantSet := make(map[string]struct{}, len(want))
	for _, a := range want {
		wantSet[a] = struct{}{}
	}

	s.mu.Lock()
	// stop departed peers
	for addr, cancel := range s.cancels {
		if _, keep := wantSet[addr]; !keep {
			cancel()
			delete(s.cancels, addr)
		}
	}
	// start new peers
	for _, addr := range want {
		if _, running := s.cancels[addr]; running {
			continue
		}
		peerCtx, cancel := context.WithCancel(parent)
		s.cancels[addr] = cancel
		s.wg.Add(1)
		go func(addr string) {
			defer s.wg.Done()
			s.run(peerCtx, addr)
		}(addr)
	}
	s.mu.Unlock()
}

// active returns a snapshot of the addresses with live goroutines.
// Used by tests; not part of any production hot path.
func (s *peerSupervisor) active() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.cancels))
	for a := range s.cancels {
		out = append(out, a)
	}
	return out
}

// shutdown cancels every active peer goroutine and waits for them
// to exit. Safe to call once after the supervisor loop has returned.
func (s *peerSupervisor) shutdown() {
	s.mu.Lock()
	for addr, cancel := range s.cancels {
		cancel()
		delete(s.cancels, addr)
	}
	s.mu.Unlock()
	s.wg.Wait()
}
