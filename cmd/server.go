package main

import (
	"context"
	v1 "github.com/anaregdesign/lantern-proto/go/graph/v1"
	"github.com/anaregdesign/lantern-server/service"
	"github.com/anaregdesign/papaya/cache/graph"
	"google.golang.org/grpc"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
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

	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal(err)
	}

	s := grpc.NewServer()
	c := graph.NewGraphCache[string, *v1.Vertex](ctx, 1*time.Minute)
	svc := service.NewLanternService(c)
	server := service.NewLanternServer(svc, s, listener)

	if err := server.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
