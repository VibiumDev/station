# Vibium Station. `make` builds for this machine; `make dist` makes the
# release tarballs; `make test` vets and tests.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)
ARCHES   = amd64 arm64

.PHONY: build test dist clean

build:  ## station and station-probe for this machine
	go build -trimpath -ldflags "$(LDFLAGS)" -o station ./cmd/station
	go build -trimpath -ldflags "$(LDFLAGS)" -o station-probe ./cmd/station-probe

test:  ## go vet and go test
	go vet ./...
	go test ./...

dist:  ## Static linux/amd64 and linux/arm64 tarballs in dist/, with checksums
	rm -rf dist && mkdir -p dist
	@for arch in $(ARCHES); do \
		name=station_$(VERSION)_linux_$$arch; dir=dist/$$name; \
		echo "  $$name"; \
		mkdir -p $$dir/deploy && \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$dir/station ./cmd/station && \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$dir/station-probe ./cmd/station-probe && \
		cp LICENSE README.md $$dir && cp -r deploy/systemd deploy/fly $$dir/deploy && \
		tar -C dist -czf $$dir.tar.gz $$name && rm -r $$dir || exit 1; \
	done
	cd dist && sha256sum *.tar.gz > sha256sums.txt

clean:
	rm -rf dist station station-probe
