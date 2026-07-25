VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -ldflags "-X main.version=$(VERSION)"

.PHONY: build test build-all clean

build:
	go build $(LDFLAGS) -o bin/splatter ./cmd/splatter

test:
	go test ./...

build-all:
	GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o bin/darwin-arm64/splatter      ./cmd/splatter
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o bin/windows-amd64/splatter.exe ./cmd/splatter

clean:
	rm -rf bin
