BINARY := rtop
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test lint arm64 package release clean

build: | bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/rtop

test:
	go test ./...

lint:
	go vet ./...

arm64: | bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 ./cmd/rtop

package:
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" ./scripts/package.sh

release: test lint package

bin:
	mkdir -p bin

clean:
	go clean
