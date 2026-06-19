.PHONY: build install test fmt lint coverage

build:
	go build -o bin/filemaid ./cmd/filemaid

install:
	go install ./cmd/filemaid

test:
	go test ./...

fmt:
	go fmt ./...

lint:
	go vet ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
