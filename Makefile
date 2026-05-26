.PHONY: all build test clean format

BINARY_NAME=gokg

all: format test build

build:
	go build -o bin/$(BINARY_NAME) ./cmd/gokg

test:
	go test -v ./...

format:
	go fmt ./...

clean:
	go clean
	rm -rf bin/
	rm -rf .gokg/

install:
	go install ./cmd/gokg
