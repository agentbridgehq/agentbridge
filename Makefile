GO ?= go
BIN := agentbridge
PKG := ./...

PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: all build test vet lint fmt tidy cross licenses clean

all: vet test build

build:
	$(GO) build -o $(BIN) ./cmd/agentbridge

test:
	$(GO) test -race -cover $(PKG)

vet:
	$(GO) vet $(PKG)

fmt:
	$(GO) fmt $(PKG)

tidy:
	$(GO) mod tidy

# Requires golangci-lint; CI installs it.
lint:
	golangci-lint run

# M0-2: every supported platform must build on every change.
cross:
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "  build $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -o dist/$(BIN)-$$os-$$arch$$ext ./cmd/agentbridge; \
	done

# M0-3: fail on copyleft licenses that would contaminate commercial code.
# See docs/08-tech-stack.md section 9.
licenses:
	$(GO) run ./internal/tools/licensecheck

clean:
	rm -rf dist $(BIN)
