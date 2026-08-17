BINARY  := multidig
VERSION ?= 0.0.0-dev

DIST      := dist
GOREL     := -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
PLATFORMS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

.PHONY: build test fmt check dist clean

build:
	go build $(GOREL) -o $(DIST)/$(BINARY) ./cmd/$(BINARY)

test:
	go test ./...

fmt:
	gofmt -w .

check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "$$out"; echo "gofmt: run 'make fmt'"; exit 1; fi
	go mod tidy -diff
	go mod verify
	go test -race ./...
	@set -e; for platform in $(PLATFORMS); do \
	  os=$${platform%/*}; arch=$${platform#*/}; \
	  echo ">> vet + lint $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch go vet ./...; \
	  GOOS=$$os GOARCH=$$arch golangci-lint run --max-same-issues 0 --max-issues-per-linter 0 ./...; \
	done
	govulncheck ./...
	actionlint .github/workflows/*.yml
	go build -trimpath ./...
	@echo "all checks passed"

dist:
	@rm -rf $(DIST)
	@mkdir -p $(DIST)
	@set -e; for platform in $(PLATFORMS); do \
	  os=$${platform%/*}; arch=$${platform#*/}; \
	  echo ">> $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
	    go build $(GOREL) -o $(DIST)/$(BINARY)_$${os}_$${arch} ./cmd/$(BINARY); \
	done

clean:
	rm -rf $(DIST)
