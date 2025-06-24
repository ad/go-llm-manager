CWD = $(shell pwd)
SRC_DIRS := .
BUILD_VERSION=$(shell cat config.json | awk 'BEGIN { FS="\""; RS="," }; { if ($$2 == "version") {print $$4} }')
REPO=danielapatin/go-llm-manager

.PHONY: build publish

build:
	@BUILD_VERSION=$(BUILD_VERSION) KO_DOCKER_REPO=$(REPO) ko build ./cmd/server --bare --local --sbom=none --tags="$(BUILD_VERSION),latest"

dev: dev-worker

dev-processor: ## Start only Go processor dev
	docker compose build processor-dev
	docker-compose up -d processor-dev

dev-worker: ## Start only Go worker dev server
	docker compose build worker-dev
	docker-compose up -d worker-dev

publish:
	@BUILD_VERSION=$(BUILD_VERSION) KO_DOCKER_REPO=$(REPO) ko publish ./cmd/server --bare --sbom=none --tags="$(BUILD_VERSION),latest"

lint:
	@golangci-lint run -v

test:
	@chmod +x ./test.sh
	@./test.sh $(SRC_DIRS)
