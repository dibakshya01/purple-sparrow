# Purple Sparrow — developer tasks
BINARY      := purplesparrow
PKG         := ./cmd/purplesparrow
MODULE      := github.com/dibakshya01/purple-sparrow
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
	-X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
	-X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(MODULE)/internal/buildinfo.Date=$(DATE)

export CGO_ENABLED := 0

.PHONY: build run test fmt vet staticcheck vulncheck check tidy clean

build: ## build the single static binary
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

run: build ## build and run
	./bin/$(BINARY)

test: ## race tests
	go test -race ./...

fmt: ## check formatting
	@test -z "$$(gofmt -l .)" || { echo "needs gofmt:"; gofmt -l .; exit 1; }

vet:
	go vet ./...

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

check: fmt vet staticcheck vulncheck test ## full local gate (matches CI)

tidy:
	go mod tidy

clean:
	rm -rf bin dist
