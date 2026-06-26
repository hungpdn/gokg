.PHONY: all build build-debug test clean format install install-tools lint security

BINARY_NAME ?= gokg
GO ?= go
GO_BUILD_FLAGS ?= -trimpath
VERSION ?= dev
COMMIT ?= none
DATE ?= unknown
GOLANGCI_LINT_VERSION ?= v2.12.2
VERSION_PKG := github.com/hungpdn/gokg/internal/version
GO_LDFLAGS ?= -s -w -buildid= -X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(DATE)

all: format test build

build:
	$(GO) build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/gokg

build-debug:
	$(GO) build -o bin/$(BINARY_NAME)-debug ./cmd/gokg

test:
	$(GO) test -v ./...

test-race:
	$(GO) test -v -race ./...

test-coverage:
	$(GO) test -v -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

format:
	$(GO) fmt ./...

lint:
	golangci-lint run --timeout=5m ./...

security:
	govulncheck ./...

install-tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

clean:
	$(GO) clean
	rm -rf bin/
	rm -rf .gokg/
	rm -rf dist/
	rm -f coverage.out coverage.html
	rm -f *.dot

install:
	$(GO) install $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" ./cmd/gokg
