VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/onatakduman/rustdesk-api-lite/internal/server.Version=$(VERSION)

.PHONY: build test linux clean
build:
	go build -ldflags "$(LDFLAGS)" -o rustdesk-api-lite .

test:
	go vet ./...
	go test ./...

# Static binaries for servers; no C toolchain needed.
linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/rustdesk-api-lite-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/rustdesk-api-lite-linux-arm64 .

clean:
	rm -rf dist rustdesk-api-lite
