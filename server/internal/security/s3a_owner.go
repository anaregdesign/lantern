package security

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

var (
	errS3AConfig = errors.New("invalid S3-A fixed owner configuration")
	errS3AClosed = errors.New("S3-A network owner closed")
	errS3ACredit = errors.New("S3-A delivery credit unavailable before transition")
	errS3AWire   = errors.New("invalid S3-A private wire request")
)

type s3aLimits struct {
	QueueBytes                                         uint64
	PerPeerQueue, TransitionCount                      int
	RangeSlots, RangeBytes                             uint64
	MaxConnections, Attempts, Rounds                   int
	RPCTimeout, Backoff, BallotBackoff, ScheduleWindow time.Duration
}

func (l s3aLimits) valid(c s2cParticipantConfig) bool {
	if c.Trust == nil || len(c.Trust.members) < 3 || len(c.Trust.members) > 31 || len(c.Trust.members)%2 == 0 || !c.Trust.bounds.valid() ||
		c.OutboxBytes == 0 || c.OutboxBytes > 256<<20 ||
		l.QueueBytes < c.OutboxBytes+uint64(len(c.Trust.members))*s3aQueueOverhead || l.QueueBytes > 1<<30 ||
		l.PerPeerQueue < 1 || l.PerPeerQueue > 32 || l.TransitionCount < len(c.Trust.members)+1 || l.TransitionCount > 128 ||
		l.RangeSlots == 0 || l.RangeSlots > 128 || l.RangeSlots > c.PPolicy.Records ||
		l.RangeBytes < c.Trust.bounds.PayloadBytes || l.RangeBytes > c.OutboxBytes ||
		l.MaxConnections < len(c.Trust.members) || l.MaxConnections > 128 ||
		l.Attempts < 1 || l.Attempts > 16 || l.Rounds < 1 || l.Rounds > 64 ||
		l.RPCTimeout <= 0 || l.RPCTimeout > peerauth.MaxRPCLifetime || l.Backoff < time.Millisecond || l.Backoff > time.Second ||
		l.BallotBackoff < l.RPCTimeout || l.BallotBackoff > l.ScheduleWindow ||
		l.ScheduleWindow < l.RPCTimeout || l.ScheduleWindow > 5*time.Minute {
		return false
	}
	maxFrame := min(c.Trust.bounds.PayloadBytes, uint64(s2cMessageOverhead)+c.Trust.bounds.ProofBytes+c.Trust.bounds.HistoricalBytes)
	if maxFrame > c.OutboxBytes/uint64(len(c.Trust.members)) {
		return false // Every representable valid broadcast must fit one reservation.
	}
	return true
}

type s3aConfig struct {
	Participant s2cParticipantConfig
	Membership  peerauth.ControlStoreOptions
	Manifest    []byte
	Identity    s3aIdentityFiles
	Limits      s3aLimits
	hooks       *s3aHooks
}

// The three floors deliberately remain different types and independent inputs.
type s3aFloors struct {
	M peerauth.ControlFloor
	P s2cJournalFloor
	B s2LocalReceipt
}

type s3aOwner struct {
	kernel      *s2cParticipant
	membership  *peerauth.ControlStore
	identity    *s3aIdentity
	limits      s3aLimits
	now         func() time.Time
	peers       map[uint32]peerauth.ControlVoter
	byIdentity  map[string]uint32
	binding     [32]byte
	client      *http.Client
	ctx         context.Context
	cancel      context.CancelFunc
	closed      atomic.Bool
	closeOnce   sync.Once
	closeErr    error
	workers     sync.WaitGroup
	callMu      sync.Mutex
	calls       sync.WaitGroup
	requests    chan s3aTransition
	ballotReady time.Time // Owned only by the serialized transition worker.
	ballotSlot  uint64
	queueMu     sync.Mutex
	queues      map[uint32][]s3aDelivery
	queued      uint64
	staged      uint64 // One reserved maximum outbox, separate from delivery caches.
	dropped     uint64 // Transport-cache drops, never durable obligation release.
	queueWake   map[uint32]chan struct{}
	inbound     map[uint32]chan struct{}
	rangeSlot   chan struct{}
	localSlot   chan struct{}
	driveSlot   chan struct{}
	serverMu    sync.Mutex
	server      *http.Server
	listener    net.Listener
	hooks       *s3aHooks
}

// Hooks alter only delivery/fault timing in native tests, never signatures,
// authenticated admission, durable certificates, or protocol verification.
type s3aHooks struct {
	beforeSend     func(context.Context, uint32, []byte) error
	beforeResponse func(context.Context)
}

func s3aPaths(c *s3aConfig) error {
	p, b, err := s2cCanonicalFamilies(c.Participant.PPath, c.Participant.BPath)
	if err != nil || !filepath.IsAbs(c.Membership.Path) {
		return errS3AConfig
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(c.Membership.Path))
	if err != nil {
		return err
	}
	m := filepath.Join(dir, filepath.Base(c.Membership.Path))
	paths := []string{p, p + ".tip", p + ".lease", b, b + ".tip", b + ".lease", m, m + ".lease"}
	seen, infos := map[string]bool{}, []os.FileInfo{}
	for _, path := range paths {
		if seen[path] {
			return errS3AConfig
		}
		seen[path] = true
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return errS3AConfig
		}
		for _, old := range infos {
			if os.SameFile(info, old) {
				return errS3AConfig
			}
		}
		infos = append(infos, info)
	}
	c.Participant.PPath, c.Participant.BPath, c.Membership.Path = p, b, m
	return nil
}

func createS3AOwner(c s3aConfig) (*s3aOwner, error) {
	return openS3AOwner(c, true, s3aFloors{})
}

func resumeS3AOwner(c s3aConfig, floors s3aFloors) (*s3aOwner, error) {
	return openS3AOwner(c, false, floors)
}

func openS3AOwner(c s3aConfig, fresh bool, floors s3aFloors) (_ *s3aOwner, err error) {
	if c.Membership.Now == nil {
		c.Membership.Now = time.Now
	}
	if !c.Limits.valid(c.Participant) || s3aPaths(&c) != nil {
		return nil, errS3AConfig
	}
	identity, err := s3aLoadIdentity(c)
	if err != nil {
		return nil, err
	}
	o := &s3aOwner{identity: identity, limits: c.Limits, now: c.Membership.Now, hooks: c.hooks, rangeSlot: make(chan struct{}, 1), binding: c.Membership.Profile.Digest(),
		localSlot: make(chan struct{}, 1), driveSlot: make(chan struct{}, 1),
		peers: map[uint32]peerauth.ControlVoter{}, byIdentity: map[string]uint32{},
		queues: map[uint32][]s3aDelivery{}, queueWake: map[uint32]chan struct{}{}, inbound: map[uint32]chan struct{}{}}
	o.ctx, o.cancel = context.WithCancel(context.Background())
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, o.Close())
		}
	}()
	if fresh {
		// Detect every existing family before creating M. A partial creation is
		// deliberately not repaired by either constructor.
		for _, path := range []string{c.Membership.Path, c.Participant.PPath, c.Participant.PPath + ".tip", c.Participant.BPath, c.Participant.BPath + ".tip"} {
			if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
				return nil, errS3AConfig
			}
		}
		o.membership, err = peerauth.CreateControlStore(c.Membership, c.Manifest)
	} else {
		o.membership, err = peerauth.ResumeControlStore(c.Membership, floors.M)
		if err == nil {
			err = o.membership.Apply(c.Manifest)
		}
	}
	if err != nil {
		return nil, err
	}
	c.Participant.Key = identity.votingKey
	if fresh {
		o.kernel, err = createS2CParticipant(c.Participant)
	} else {
		o.kernel, err = resumeS2CParticipant(c.Participant, floors.P, floors.B)
	}
	clear(identity.votingKey)
	identity.votingKey = nil
	if err != nil {
		return nil, err
	}
	if err = o.check(o.ctx); err != nil {
		return nil, err
	}
	for _, v := range c.Membership.Profile.Voters {
		o.peers[v.Voter], o.byIdentity[v.Workload.Identity] = v, v.Voter
		o.queueWake[v.Voter], o.inbound[v.Voter] = make(chan struct{}, 1), make(chan struct{}, 1)
	}
	o.client = o.membership.NewHTTPClient(identity.certificate, identity.roots)
	o.requests = make(chan s3aTransition, c.Limits.TransitionCount)
	o.workers.Add(1)
	go o.transitions()
	for id := range o.peers {
		o.workers.Add(1)
		go o.deliveries(id)
	}
	transferred = true
	return o, nil
}

func (o *s3aOwner) check(ctx context.Context) error {
	if o == nil || o.closed.Load() {
		return errS3AClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !o.now().Before(o.identity.expires) {
		return peerauth.ErrMembership
	}
	err := o.membership.Check(ctx)
	if err != nil && o.membership.FaultReason() != "none" {
		o.fail()
	}
	return err
}

func (o *s3aOwner) fail() {
	o.callMu.Lock()
	o.closed.Store(true)
	o.callMu.Unlock()
	o.cancel()
}

// Register admitted HTTP handlers and externally driven network operations
// before shutdown begins. The mutex makes Add versus terminal Wait ordered.
func (o *s3aOwner) enterCall() bool {
	if o == nil {
		return false
	}
	o.callMu.Lock()
	defer o.callMu.Unlock()
	if o.closed.Load() {
		return false
	}
	o.calls.Add(1)
	return true
}

func (o *s3aOwner) Refresh(raw []byte) error {
	if o == nil || o.closed.Load() {
		return errS3AClosed
	}
	err := o.membership.Apply(raw)
	if o.membership.FaultReason() != "none" {
		o.fail()
	}
	return err
}

func (o *s3aOwner) Floors() (s3aFloors, error) {
	if o == nil || o.closed.Load() {
		return s3aFloors{}, errS3AClosed
	}
	m, err := o.membership.Floor()
	if err != nil {
		return s3aFloors{}, err
	}
	p, b, err := o.kernel.Floors()
	if err != nil {
		o.fail()
	}
	return s3aFloors{m, p, b}, err
}

// MembershipFloor may report an acknowledged terminal M fence for independent
// retention even when that fence has stopped P/B/network admission. Uncertain
// checkpoint I/O returns no receipt. This is never a serving/freshness token.
func (o *s3aOwner) MembershipFloor() (peerauth.ControlFloor, error) {
	if o == nil || o.membership == nil {
		return peerauth.ControlFloor{}, errS3AClosed
	}
	return o.membership.Floor()
}

func (o *s3aOwner) Close() error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		o.fail()
		o.serverMu.Lock()
		if o.server != nil {
			_ = o.server.Close()
		}
		if o.listener != nil {
			_ = o.listener.Close()
		}
		o.serverMu.Unlock()
		o.workers.Wait()
		o.calls.Wait()
		if o.client != nil {
			o.client.CloseIdleConnections()
		}
		if o.kernel != nil {
			o.closeErr = errors.Join(o.closeErr, o.kernel.Close())
		}
		if o.membership != nil {
			o.closeErr = errors.Join(o.closeErr, o.membership.Close())
		}
		o.identity.clear()
		o.queueMu.Lock()
		clear(o.queues)
		o.queued = 0
		o.queueMu.Unlock()
	})
	return o.closeErr
}

func (o *s3aOwner) tlsConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{o.identity.certificate},
		ClientCAs: o.identity.roots.Clone(), ClientAuth: tls.RequireAndVerifyClientCert,
		NextProtos: []string{"http/1.1"}, SessionTicketsDisabled: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if err := o.check(o.ctx); err != nil {
				return err
			}
			_, err := o.membership.Admit(&state, "")
			return err
		}}
}
