.PHONY: all generate wire proto build test

all: generate

generate: wire proto

wire: ./server/cmd/wire_gen.go

./server/cmd/wire_gen.go: ./server/cmd/wire.go
	@echo "Generating wire_gen.go"
	wire ./server/cmd

proto:
	@echo "Generating protobuf code"
	rm -rf ./gen/go ./gen/openapiv2
	buf generate proto

build:
	go build -v ./...

test:
	go test -v ./...
