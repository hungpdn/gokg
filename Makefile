.PHONY: all build build-debug test clean format install lint

BINARY_NAME ?= gokg
GO ?= go
GO_BUILD_FLAGS ?= -trimpath
VERSION ?= dev
COMMIT ?= none
DATE ?= unknown
VERSION_PKG := github.com/hungpdn/gokg/internal/version
GO_LDFLAGS ?= -s -w -buildid= -X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(DATE)

all: format test build

build:
	$(GO) build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/gokg

build-debug:
	$(GO) build -o bin/$(BINARY_NAME)-debug ./cmd/gokg

test:
	$(GO) test -v ./...

format:
	$(GO) fmt ./...

lint:
	golangci-lint run --timeout=5m ./...

clean:
	$(GO) clean
	rm -rf bin/
	rm -rf .gokg/

install:
	$(GO) install $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" ./cmd/gokg
