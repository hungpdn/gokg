.PHONY: all build build-debug test clean format install

BINARY_NAME ?= gokg
GO ?= go
GO_BUILD_FLAGS ?= -trimpath
GO_LDFLAGS ?= -s -w -buildid=

all: format test build

build:
	$(GO) build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/gokg

build-debug:
	$(GO) build -o bin/$(BINARY_NAME)-debug ./cmd/gokg

test:
	$(GO) test -v ./...

format:
	$(GO) fmt ./...

clean:
	$(GO) clean
	rm -rf bin/
	rm -rf .gokg/

install:
	$(GO) install $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" ./cmd/gokg
