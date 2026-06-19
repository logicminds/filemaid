.PHONY: build install test fmt lint coverage

build:
	go build -ldflags "-X github.com/logicminds/filemaid/internal/cli.Version=$(shell git describe --tags --always) -X github.com/logicminds/filemaid/internal/cli.Commit=$(shell git rev-parse --short HEAD)" -o bin/filemaid ./cmd/filemaid

install:
	go install -ldflags "-X github.com/logicminds/filemaid/internal/cli.Version=$(shell git describe --tags --always) -X github.com/logicminds/filemaid/internal/cli.Commit=$(shell git rev-parse --short HEAD)" ./cmd/filemaid


test:
	go test ./...

fmt:
	go fmt ./...

lint:
	go vet ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
