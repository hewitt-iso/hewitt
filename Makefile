GO ?= go
VERSION ?= dev
DOCKER ?= docker
IMAGE_REGISTRY ?= ghcr.io/hewitt-iso
IMAGE_TAG ?= latest
IMAGE_BUILD_FLAGS ?=

.DEFAULT_GOAL := build
.PHONY: build run test vet fmt fmt-check check tidy images image-hewitt image-egress push-images push-hewitt

images: image-hewitt image-egress

image-hewitt:
	$(DOCKER) build $(IMAGE_BUILD_FLAGS) --target hewitt -t hewitt:local -t $(IMAGE_REGISTRY)/hewitt:$(IMAGE_TAG) images

push-images: push-hewitt

push-hewitt:
	$(DOCKER) push $(IMAGE_REGISTRY)/hewitt:$(IMAGE_TAG)

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
