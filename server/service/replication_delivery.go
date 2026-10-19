package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/replicationstatus"
)

// replicationDelivery keeps terminal observation independent of synchronous
// network delivery. The HTTP owner supplies Interrupt and owns its actual I/O
// cancellation; this layer never renews a peer admission or runs Send elsewhere.
type replicationDelivery struct {
	ctx          context.Context
	sender       Sender[pb.SubscribeResponse]
	subscription *mutationlog.Subscription
	fault        <-chan struct{}
	sending      atomic.Bool
	stop         chan struct{}
	stopped      chan struct{}
	closeOnce    sync.Once
}

func newReplicationDelivery(ctx context.Context, sender Sender[pb.SubscribeResponse], subscription *mutationlog.Subscription, fault <-chan struct{}) *replicationDelivery {
	d := &replicationDelivery{ctx: ctx, sender: sender, subscription: subscription, fault: fault}
	interrupt, ok := sender.(interface{ Interrupt() })
	if !ok {
		return d
	}
	d.stop, d.stopped = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(d.stopped)
		abort := false
		select {
		case <-subscription.Done():
			abort = subscription.Err() != nil
		case <-fault:
			abort = true
		case <-ctx.Done():
			abort = true
		case <-d.stop:
		}
		if abort && d.sending.Load() {
			interrupt.Interrupt()
		}
	}()
	return d
}

func replicationStreamClosedError() error {
	return replicationstatus.TransientGap(replicationstatus.ReasonSubscriberStreamClosed,
		errors.New("gapped: subscriber stream closed; reconnect from committed cursor"))
}

func (d *replicationDelivery) ready() error {
	select {
	case <-d.fault:
		return publicationGapError()
	default:
	}
	if err := d.subscription.Err(); err != nil {
		if errors.Is(err, context.Canceled) && d.ctx.Err() != nil {
			return ctxToConnect(d.ctx.Err())
		}
		return replicationStreamClosedError()
	}
	if err := d.ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	return nil
}

func (d *replicationDelivery) send(send func() error) error {
	d.sending.Store(true)
	defer d.sending.Store(false)
	// Setting sending before checking terminal state closes the race where
	// the watcher observes loss just before this goroutine begins a Send.
	if err := d.ready(); err != nil {
		return err
	}
	err := send()
	if err != nil {
		if terminal := d.ready(); terminal != nil {
			return terminal
		}
	}
	return err
}

func (d *replicationDelivery) Send(frame *pb.SubscribeResponse) error {
	return d.send(func() error { return d.sender.Send(frame) })
}

func (d *replicationDelivery) FlushHeaders() error {
	if headers, ok := d.sender.(interface{ FlushHeaders() error }); ok {
		return d.send(headers.FlushHeaders)
	}
	return nil
}

// Close runs after the handler's final synchronous Send returns. Joining the
// watcher here prevents a late interrupt after the HTTP owner resets deadlines.
func (d *replicationDelivery) Close() {
	if d.stop != nil {
		d.closeOnce.Do(func() {
			close(d.stop)
			<-d.stopped
		})
	}
}
