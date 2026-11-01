GO ?= go
VERSION ?= dev

.DEFAULT_GOAL := build
.PHONY: build run test vet fmt fmt-check check tidy

build:
	$(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/hewitt .

run:
	$(GO) run . $(ARGS)

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

fmt-check:
	@files=$$(gofmt -l .) || exit 1; \
	if [ -n "$$files" ]; then printf 'Run make fmt:\n%s\n' "$$files"; exit 1; fi

check: fmt-check vet test

tidy:
	$(GO) mod tidy
