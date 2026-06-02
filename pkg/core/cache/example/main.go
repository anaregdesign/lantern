package main

import (
	"context"
	"github.com/anaregdesign/lantern/pkg/core/cache"
	"log"
	"time"
)

func main() {
	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
	c := cache.NewCache[string, string](1 * time.Second)

	// Watch the cache, this will remove expired items
	go c.Watch(ctx, 1*time.Second)

	// Put a value
	c.Put("key", "value")

	// Get a value
	if value, ok := c.Get("key"); ok {
		log.Printf("value: %v", value)
	}

	// Wait for the cache to expire
	time.Sleep(1 * time.Second)

	// Get a value
	if _, ok := c.Get("key"); !ok {
		log.Printf("Key expired")
	}

	<-ctx.Done()

}
