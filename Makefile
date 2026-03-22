.PHONY: build test

build:
	mise exec -- go build -o bin/butcherie ./cmd/butcherie

test:
	mise exec -- go test -v ./...
