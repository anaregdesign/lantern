.DEFAULT_GOAL := all
SHELL := /bin/bash

.PHONY: all generate wire proto proto-lint proto-format proto-breaking proto-deps build test test-race fmt vet lint tidy vuln clean

all: generate

generate: wire proto

wire: ./server/cmd/wire_gen.go

./server/cmd/wire_gen.go: ./server/cmd/wire.go
	@echo "Generating wire_gen.go"
	wire ./server/cmd

# Proto codegen: buf v2 with managed mode + remote BSR plugins.
# Config lives in ./buf.yaml (v2 workspace) and ./buf.gen.yaml (v2 plugins).
# The output target depends on every .proto file so make skips work when
# nothing changed; --clean wipes orphaned generated files inside the outputs.
PROTO_SRCS := $(shell find proto -name '*.proto')
PROTO_OUT  := gen/go/graph/v1/graph.pb.go

proto: $(PROTO_OUT)

$(PROTO_OUT): $(PROTO_SRCS) buf.yaml buf.gen.yaml buf.lock
	@echo "Generating protobuf code"
	buf generate --clean

proto-lint:
	buf lint

proto-format:
	buf format -d --exit-code

proto-breaking:
	buf breaking --against '.git#branch=main'

proto-deps:
	buf dep update

build:
	go build -v ./...

test:
	go test -v ./...

test-race:
	go test -race -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...

fmt:
	gofmt -s -w .

vet:
	go vet ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

clean:
	rm -rf bin dist coverage.out
