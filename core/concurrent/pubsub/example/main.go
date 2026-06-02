package main

import (
	"context"
	"github.com/anaregdesign/lantern/core/concurrent/pubsub"
	"log"
	"runtime"
	"time"
)

var (
	topic = pubsub.NewTopic[int]("int_topic")
	sub   = topic.NewSubscription("printer", runtime.NumCPU(), time.Minute, time.Minute)
)

func main() {
	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)

	// Publish 100 messages
	go func() {
		for i := 0; i < 100; i++ {
			topic.Publish(i)
		}
	}()

	// Subscribe to the topic
	go sub.Subscribe(ctx, func(x *pubsub.Message[int]) {
		// Simulate some work
		time.Sleep(1 * time.Second)
		log.Printf("value: %v", x.Body())

		// Acknowledge the message
		x.Ack()
	})

	// Wait for the context to be done
	<-ctx.Done()

}
