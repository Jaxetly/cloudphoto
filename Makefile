GO ?= go
INSTANCE ?= claudlab
export GOPROXY ?= https://proxy.golang.org,direct

.PHONY: build test-build test vet fmt

build:
	mkdir -p dist/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w" -o dist/linux-amd64/cloudphoto ./cmd/cloudphoto

test-build:
	mkdir -p dist/tests
	@set -e; for pkg in $$($(GO) list ./internal/...); do \
		name=$${pkg##*/}; \
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) test -c -trimpath -o dist/tests/$$name.test $$pkg; \
	done

test: build test-build
	bash scripts/test.sh "$(INSTANCE)"

vet:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) vet ./...

fmt:
	$(GO) fmt ./...
	terraform -chdir=terraform fmt
