VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/onatakduman/rustdesk-api-lite/internal/server.Version=$(VERSION)

.PHONY: build test linux release clean
build:
	go build -ldflags "$(LDFLAGS)" -o rustdesk-api-lite .

test:
	go vet ./...
	go test ./...

# Static binaries for servers; no C toolchain needed.
linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/rustdesk-api-lite-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/rustdesk-api-lite-linux-arm64 .

# Release archives for every platform, with checksums, in dist/. The names carry no
# version, so that releases/latest/download/<name> always points at the newest one.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
release:
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; name=rustdesk-api-lite-$$os-$$arch; exe=; \
		if [ $$os = windows ]; then exe=.exe; fi; \
		mkdir -p dist/stage/$$name; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/stage/$$name/rustdesk-api-lite$$exe .; \
		cp README.md LICENSE CHANGELOG.md dist/stage/$$name/; \
		if [ $$os = linux ]; then cp deploy/rustdesk-api-lite.service install.sh dist/stage/$$name/; fi; \
		if [ $$os = windows ]; then (cd dist/stage && zip -qr ../$$name.zip $$name); \
		else tar -C dist/stage -czf dist/$$name.tar.gz $$name; fi; \
		echo "built $$name"; \
	done; \
	cd dist && (sha256sum *.tar.gz *.zip 2>/dev/null || shasum -a 256 *.tar.gz *.zip) > SHA256SUMS

clean:
	rm -rf dist rustdesk-api-lite
