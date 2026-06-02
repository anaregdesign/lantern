.DEFAULT_GOAL := all
SHELL := /bin/bash

.PHONY: all generate wire proto build test test-race fmt vet lint tidy vuln clean

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
