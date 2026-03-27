.PHONY: test

test:
	mise exec -- go test -v ./...
