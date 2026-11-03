GO ?= go
VERSION ?= dev
DOCKER ?= docker
CODEX_VERSION ?= latest
CLAUDE_VERSION ?= stable
OPENCODE_VERSION ?= latest
IMAGE_REGISTRY ?= ghcr.io/hewitt-iso
IMAGE_TAG ?= latest
IMAGE_BUILD_FLAGS ?=

.DEFAULT_GOAL := build
.PHONY: build run test vet fmt fmt-check check tidy images image-codex image-claude image-opencode image-egress push-images push-codex push-claude push-opencode

images: image-codex image-claude image-opencode image-egress

image-codex:
	$(DOCKER) build $(IMAGE_BUILD_FLAGS) --target codex --build-arg CODEX_VERSION=$(CODEX_VERSION) -t hewitt/codex:local -t hewitt-codex:local -t $(IMAGE_REGISTRY)/hewitt-codex:$(IMAGE_TAG) images

image-claude:
	$(DOCKER) build $(IMAGE_BUILD_FLAGS) --target claude --build-arg CLAUDE_VERSION=$(CLAUDE_VERSION) -t hewitt/claude:local -t hewitt-claude:local -t $(IMAGE_REGISTRY)/hewitt-claude:$(IMAGE_TAG) images

image-opencode:
	$(DOCKER) build $(IMAGE_BUILD_FLAGS) --target opencode --build-arg OPENCODE_VERSION=$(OPENCODE_VERSION) -t hewitt/opencode:local -t hewitt-opencode:local -t $(IMAGE_REGISTRY)/hewitt-opencode:$(IMAGE_TAG) images

push-images: push-codex push-claude push-opencode

push-codex:
	$(DOCKER) push $(IMAGE_REGISTRY)/hewitt-codex:$(IMAGE_TAG)

push-claude:
	$(DOCKER) push $(IMAGE_REGISTRY)/hewitt-claude:$(IMAGE_TAG)

push-opencode:
	$(DOCKER) push $(IMAGE_REGISTRY)/hewitt-opencode:$(IMAGE_TAG)

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

image-egress:
	$(DOCKER) build $(IMAGE_BUILD_FLAGS) --target egress -t hewitt/egress:local images
