VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint fmt clean cve-fixtures

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/ard ./cmd/ard

test:
	go test ./...

lint:
	go vet ./...
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

fmt:
	gofmt -w .

# Regenerates the real cve scanner fixtures inside the pinned tool image.
# Needs Docker and network; not part of `make test`.
cve-fixtures:
	docker build -t ard-cve-fixtures internal/roles/scanners/cve
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp \
	  -v "$$PWD/internal/roles:/roles" -w /roles ard-cve-fixtures sh testdata/cve/regen.sh

clean:
	rm -rf bin
