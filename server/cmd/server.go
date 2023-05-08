package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	log.Println("Environment Variables:")
	for _, pair := range os.Environ() {
		log.Println(pair)
	}

	signalCh := make(chan os.Signal)
	defer close(signalCh)

	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-signalCh
		cancel()
	}()

	svr, err := initializeLanternServer()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Starting Lantern Server")
	if err := svr.Run(ctx); err != nil {
		log.Fatal(err)
	}

}
