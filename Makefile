.DEFAULT_GOAL := all
SHELL := /bin/bash

# Pinned buf version used by the `go run` fallback. Keep in sync with the
# directive in generate.go so local and CI behaviour match.
BUF_VERSION := v1.70.0
BUF        ?= $(shell command -v buf 2>/dev/null)
ifeq ($(BUF),)
BUF := go run github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
endif

.PHONY: all generate wire proto proto-lint proto-format proto-breaking proto-deps \
        build test test-race fmt vet lint tidy vuln clean

# `go generate ./...` is the single source of truth: it runs `go tool wire`
# (registered via the tool directive in go.mod) and `buf generate`. No
# pre-installation required — all tools resolve through the Go toolchain.
all: generate

generate:
	go generate ./...

# Backwards-compatible aliases for muscle memory.
wire:
	cd server && go tool wire ./cmd

proto:
	$(BUF) generate

proto-lint:
	$(BUF) lint

proto-format:
	$(BUF) format -d --exit-code

proto-breaking:
	$(BUF) breaking --against '.git#branch=main'

proto-deps:
	$(BUF) dep update

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
