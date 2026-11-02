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

.PHONY: images image-claude image-codex image-opencode image-egress
images: image-claude image-codex image-opencode image-egress

image-claude:
	docker build --target claude -t hewitt/claude:local images

image-codex:
	docker build --target codex -t hewitt/codex:local images

image-opencode:
	docker build --target opencode -t hewitt/opencode:local images

image-egress:
	docker build --target egress -t hewitt/egress:local images
