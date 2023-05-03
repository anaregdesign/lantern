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

	if err := svr.Run(ctx); err != nil {
		log.Fatal(err)
	}

}
