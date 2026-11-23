VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint fmt clean cve-fixtures lint-scanner-fixtures chart-test image-test

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/ard ./cmd/ard

test:
	go test ./...

lint:
	go vet ./...
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

fmt:
	gofmt -w .

# Renders the two Argo CD charts under deploy/ and checks them (needs helm and yq;
# run by the ard-ci pipeline, not part of `make test`).
chart-test:
	deploy/test-charts.sh

# Builds the ard image for both platforms and checks it (needs Docker buildx and
# network; not part of `make test`).
image-test:
	deploy/test-image.sh

# Regenerates the real cve scanner fixtures inside the pinned tool image.
# Needs Docker and network; not part of `make test`.
cve-fixtures:
	docker build -t ard-cve-fixtures internal/roles/scanners/cve
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp \
	  -v "$$PWD/internal/roles:/roles" -w /roles ard-cve-fixtures sh testdata/cve/regen.sh

# Regenerates the real lint scanner fixtures in the pinned image (see
# internal/roles/testdata/lint/regen.sh). Needs Docker; not part of `make test`.
lint-scanner-fixtures:
	docker build -t ard-lint-fixtures internal/roles/scanners/lint
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp \
	  -v "$$PWD/internal/roles:/roles" -w /roles ard-lint-fixtures sh testdata/lint/regen.sh

clean:
	rm -rf bin
