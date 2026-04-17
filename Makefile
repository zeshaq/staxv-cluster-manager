.PHONY: dev build test tidy clean install-deps frontend frontend-dev help

BIN       := ./tmp/staxv-cluster-manager
MAIN      := ./cmd/staxv-cluster-manager
VERSION   := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
LDFLAGS   := -X main.version=$(VERSION)

## dev         — backend live-reload (air). Runs on :5002 by default.
dev:
	@command -v air >/dev/null 2>&1 || { echo "air not found — run 'make install-deps' first"; exit 1; }
	air

## frontend    — one-shot React build (no frontend dir yet; placeholder for when we add one).
frontend:
	@if [ -d frontend ]; then \
		cd frontend && npm install && npm run build && \
		rm -rf ../internal/webui/dist && mkdir -p ../internal/webui/dist && \
		cp -R dist/. ../internal/webui/dist/ && \
		touch ../internal/webui/dist/.gitkeep ; \
	else \
		echo "no frontend/ dir yet — scaffold only serves the JSON API"; \
	fi

## build       — produce a release binary in ./tmp/ (includes frontend if present).
build: frontend
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(MAIN)

## build-backend — Go binary only (skip frontend step).
build-backend:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(MAIN)

## test        — run unit tests
test:
	go test -race -count=1 ./...

## tidy        — sync go.mod / go.sum
tidy:
	go mod tidy

## clean       — remove build artifacts
clean:
	rm -rf ./tmp internal/webui/dist
	mkdir -p internal/webui/dist
	@touch internal/webui/dist/.gitkeep

## install-deps — install dev dependencies (air).
install-deps:
	go install github.com/air-verse/air@latest

## help
help:
	@awk '/^## /{sub(/^## /, ""); printf "  %s\n", $$0}' $(MAKEFILE_LIST)
