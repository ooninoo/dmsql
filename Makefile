.DEFAULT_GOAL := build

GO ?= go
DOCKER ?= docker
BINARY ?= bin
IMAGE ?= dmsql:latest
COMMIT_ID ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS ?= -s -w -X main.commitID=$(COMMIT_ID)

.PHONY: build test vet clean docker-build

build: build-x86 build-aarch64

build-x86:
	mkdir -p "$(BINARY)"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -buildvcs=false -ldflags="$(LDFLAGS)" -o "$(BINARY)/dmsql-linux-x86" ./cmd

build-aarch64:
	mkdir -p "$(BINARY)"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -buildvcs=false -ldflags="$(LDFLAGS)" -o "$(BINARY)/dmsql-linux-aarch64" ./cmd

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -f "$(BINARY)"

docker-build:
	$(DOCKER) build -t "$(IMAGE)" .
