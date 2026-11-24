VERSION ?= $(shell git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude 'v*-*' --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint fmt clean cve-fixtures lts-fixtures lint-scanner-fixtures chart-test image-test agent-image agent-image-test

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

# Builds the default ard-agent image for this host as ard-agent:dev and prints its
# ID. Local only, never published. Needs Docker and network; not part of `make test`.
agent-image:
	docker build -t ard-agent:dev deploy/agent
	docker image inspect --format '{{.Id}}' ard-agent:dev

# Checks ard-agent:dev (needs Docker; run `make agent-image` first; not part of `make test`).
agent-image-test:
	deploy/test-agent-image.sh ard-agent:dev

# Regenerates the real cve scanner fixtures inside the pinned tool image.
# Needs Docker and network; not part of `make test`.
cve-fixtures:
	docker build -t ard-cve-fixtures internal/roles/scanners/cve
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp \
	  -v "$$PWD/internal/roles:/roles" -w /roles ard-cve-fixtures sh testdata/cve/regen.sh

# Regenerates the real lts API fixtures inside the pinned lts image (see
# internal/roles/testdata/lts/regen.sh). Needs Docker and network; not part of `make test`.
lts-fixtures:
	docker build -t ard-lts-fixtures internal/roles/scanners/lts
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp \
	  -v "$$PWD/internal/roles:/roles" -w /roles ard-lts-fixtures sh testdata/lts/regen.sh

# Regenerates the real lint scanner fixtures in the pinned image (see
# internal/roles/testdata/lint/regen.sh). Needs Docker; not part of `make test`.
lint-scanner-fixtures:
	docker build -t ard-lint-fixtures internal/roles/scanners/lint
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp \
	  -v "$$PWD/internal/roles:/roles" -w /roles ard-lint-fixtures sh testdata/lint/regen.sh

# Builds the user docs site strictly (needs mkdocs-material: pip install -r
# docs/site/requirements.txt) and runs the docs drift test (part of `make test`).
docs-check:
	mkdocs build --strict -d /tmp/ard-site
	go test ./internal/docscheck/...

clean:
	rm -rf bin
