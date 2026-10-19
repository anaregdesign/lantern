package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/replicationstatus"
)

type deliveryTestSender struct {
	send      func() error
	interrupt func()
}

func (s deliveryTestSender) Send(*pb.SubscribeResponse) error { return s.send() }
func (s deliveryTestSender) Interrupt()                       { s.interrupt() }

func TestReplicationDeliveryInterruptsBlockedSend(t *testing.T) {
	for _, cause := range []string{"overflow", "publication fault", "request cancellation"} {
		t.Run(cause, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				log := mutationlog.New(mutationlog.Options{Capacity: 10000, SubscriberBuffer: 512})
				defer log.Close()
				subscription, err := log.SubscribeStream(1, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer subscription.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				entered, interrupted := make(chan struct{}), make(chan struct{})
				var once sync.Once
				sender := deliveryTestSender{
					send:      func() error { close(entered); <-interrupted; return errors.New("transport interrupted") },
					interrupt: func() { once.Do(func() { close(interrupted) }) },
				}
				fault := make(chan struct{})
				delivery := newReplicationDelivery(ctx, sender, subscription, fault)
				defer delivery.Close()
				done := make(chan error, 1)
				go func() { done <- delivery.Send(&pb.SubscribeResponse{}) }()
				<-entered
				switch cause {
				case "overflow":
					for i := 1; i <= 514; i++ {
						if _, err := log.Append(i, hlc.Timestamp{}); err != nil {
							t.Fatal(err)
						}
						if i == 1 {
							synctest.Wait()
						}
					}
				case "publication fault":
					close(fault)
				case "request cancellation":
					cancel()
				}
				synctest.Wait()
				select {
				case <-interrupted:
				default:
					t.Fatal("terminal signal did not interrupt the in-flight Send")
				}
				err = <-done
				if cause == "request cancellation" {
					if connect.CodeOf(err) != connect.CodeCanceled {
						t.Fatalf("cancel error = %v", err)
					}
				} else if transient, detailErr := replicationstatus.IsTransientGap(err); detailErr != nil || !transient {
					t.Fatalf("terminal error = %v; transient=%t detail=%v", err, transient, detailErr)
				}
				delivery.Close()
				select {
				case <-delivery.stopped:
				default:
					t.Fatal("delivery watcher survived Close")
				}
			})
		})
	}
}

func TestReplicationDeliveryChecksLossBeforeStartingSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := mutationlog.New(mutationlog.Options{Capacity: 10000, SubscriberBuffer: 512})
		defer log.Close()
		subscription, err := log.SubscribeStream(1, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer subscription.Close()
		for i := 1; i <= 514; i++ {
			if _, err := log.Append(i, hlc.Timestamp{}); err != nil {
				t.Fatal(err)
			}
			if i == 1 {
				synctest.Wait()
			}
		}
		synctest.Wait()
		var sends atomic.Int32
		sender := deliveryTestSender{send: func() error { sends.Add(1); return nil }, interrupt: func() {}}
		delivery := newReplicationDelivery(context.Background(), sender, subscription, nil)
		defer delivery.Close()
		if err := delivery.Send(&pb.SubscribeResponse{}); err == nil || sends.Load() != 0 {
			t.Fatalf("loss allowed a new Send: sends=%d error=%v", sends.Load(), err)
		}
	})
}

func TestReplicationDeliveryStopsWatcherBeforeNormalCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := mutationlog.New(mutationlog.Options{})
		defer log.Close()
		subscription, err := log.SubscribeStream(1, nil)
		if err != nil {
			t.Fatal(err)
		}
		var interrupted atomic.Int32
		sender := deliveryTestSender{send: func() error { return nil }, interrupt: func() { interrupted.Add(1) }}
		delivery := newReplicationDelivery(context.Background(), sender, subscription, nil)
		if err := delivery.Send(&pb.SubscribeResponse{}); err != nil {
			t.Fatal(err)
		}
		delivery.Close()
		subscription.Close()
		synctest.Wait()
		if interrupted.Load() != 0 {
			t.Fatal("normal cleanup interrupted an already completed HTTP request")
		}
	})
}
