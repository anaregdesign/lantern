package security

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"sync"
	"time"
)

const authorityRenewalPath = "/lantern-private/control/current-v1/renewal"
const authorityRenewalRequestLimit = len(authorityRenewalRequestDomain) + authorityRenewalMaxStatement + 64

type authorityRenewalResult struct {
	raw []byte
	err error
}
type authorityRenewalTask struct {
	ctx     context.Context
	sender  uint32
	raw     []byte
	check   func() error
	release func()
	done    chan authorityRenewalResult
}

// The composite owner supplies its native clock. Existing S3-A construction
// remains available separately; it cannot produce current renewal authority.
func bindAuthorityNetworkTime(c *s3aConfig, clock *authorityTimeOwner) error {
	if c == nil || clock == nil || c.Participant.Trust == nil || c.Limits.MaxConnections < 2*len(c.Participant.Trust.members)+1 {
		return errS3AConfig
	}
	c.timeOwner = clock
	bounds := func() (time.Time, time.Time, error) {
		now, err := clock.current()
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		return time.Unix(0, int64(now.utc.low)).UTC(), time.Unix(0, int64(now.utc.high)).UTC(), nil
	}
	c.Membership.TimeBounds = bounds
	c.Membership.Now = func() time.Time {
		_, high, err := bounds()
		if err != nil {
			return time.Unix(0, math.MaxInt64).UTC()
		}
		return high
	}
	return nil
}

func (o *s3aOwner) renewalVote(ctx context.Context, sender uint32, raw []byte, check func() error) ([]byte, error) {
	if o.receiver == nil || len(raw) > authorityRenewalRequestLimit || !o.enterCall() {
		return nil, errAuthorityRenewal
	}
	defer o.calls.Done()
	slot, ok := o.renewalInbound[sender]
	if !ok {
		return nil, errAuthorityRenewal
	}
	select {
	case slot <- struct{}{}:
	default:
		return nil, errS3ACredit
	}
	transferred := false
	defer func() {
		if !transferred {
			<-slot
		}
	}()
	r := authorityRenewalTask{ctx, sender, bytes.Clone(raw), check, func() { <-slot }, make(chan authorityRenewalResult, 1)}
	select {
	case <-o.ctx.Done():
		return nil, errS3AClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	case o.renewals <- r:
		transferred = true
	default:
		return nil, errS3ACredit
	}
	select {
	case result := <-r.done:
		return result.raw, result.err
	case <-o.ctx.Done():
		return nil, errS3AClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (o *s3aOwner) applyRenewal(r authorityRenewalTask) (result authorityRenewalResult) {
	defer r.release()
	defer func() {
		if recover() != nil {
			o.fail()
			result = authorityRenewalResult{err: errS2CUnknown}
		}
	}()
	if err := o.check(r.ctx); err != nil {
		return authorityRenewalResult{err: err}
	}
	request, err := parseAuthorityRenewalRequest(r.raw, o.kernel.trust, o.binding)
	if err != nil || request.statement.Receiver != r.sender {
		return authorityRenewalResult{err: errAuthorityRenewal}
	}
	eligible := func() error {
		if err := o.check(r.ctx); err != nil {
			return err
		}
		if r.check == nil {
			return errAuthorityRenewal
		}
		return r.check()
	}
	raw, err := o.kernel.signAuthorityRenewal(r.raw, o.binding, eligible)
	return authorityRenewalResult{raw, err}
}

// One receiver cycle fans out to bounded distinct peers. A missing peer cannot
// consume another peer's slot or delay it serially. Every entered call joins
// before this cycle releases its pending challenge or starts another cycle.
func (o *s3aOwner) renewAuthority(ctx context.Context) error {
	if o.receiver == nil || !o.enterCall() {
		return errAuthorityRenewal
	}
	defer o.calls.Done()
	if err := o.check(ctx); err != nil {
		return err
	}
	raw, err := o.receiver.challenge()
	if err != nil {
		return err
	}
	defer o.receiver.abandon(raw)
	ctx, cancel := context.WithTimeout(ctx, o.limits.RPCTimeout)
	defer cancel()
	var workers sync.WaitGroup
	for id := range o.peers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var vote []byte
			var err error
			if id == o.kernel.config.Member {
				vote, err = o.renewalVote(ctx, id, raw, func() error { return o.check(ctx) })
			} else {
				var status int
				vote, status, err = o.request(ctx, id, authorityRenewalPath, raw, 68)
				if status != http.StatusOK {
					err = errAuthorityRenewal
				}
			}
			if err == nil {
				_ = o.receiver.receive(raw, vote)
			}
		}()
	}
	workers.Wait()
	o.kernel.gate.Lock()
	defer o.kernel.gate.Unlock()
	_, _, err = o.receiver.currentLocked()
	return err
}

func (o *s3aOwner) renewalsLoop() {
	defer o.workers.Done()
	for {
		if o.hooks != nil && o.hooks.beforeRenewal != nil {
			o.hooks.beforeRenewal(o.ctx)
		}
		if o.ctx.Err() != nil {
			return
		}
		_ = o.renewAuthority(o.ctx)
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-o.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
