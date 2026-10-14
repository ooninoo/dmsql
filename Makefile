.DEFAULT_GOAL := build

GO ?= go
DOCKER ?= docker
BINARY ?= bin
IMAGE ?= dmsql:latest

.PHONY: build test vet clean docker-build

build: build-x86 build-aarch64

build-x86:
	mkdir -p "$(dir $(BINARY))/x86"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -buildvcs=false -ldflags="-s -w" -o "$(BINARY)/x86/dmsql-x86" ./cmd

build-aarch64:
	mkdir -p "$(dir $(BINARY))/aarch64"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -buildvcs=false -ldflags="-s -w" -o "$(BINARY)/aarch64/dmsql-aarch64" ./cmd

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -f "$(BINARY)"

docker-build:
	$(DOCKER) build -t "$(IMAGE)" .
