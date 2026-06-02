package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.Println("Environment Variables:")
	for _, pair := range os.Environ() {
		log.Println(pair)
	}

	// NotifyContext cancels ctx on SIGINT/SIGTERM and avoids the
	// close-while-Notify-may-send race of an explicit signal channel.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	svr, err := initializeLanternServer()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Starting Lantern Server")
	if err := svr.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
