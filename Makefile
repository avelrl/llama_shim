.PHONY: run build lint test vet diff-check ci-check maint-cleanup maint-optimize maint-vacuum maint-backup docker-build compose-up compose-down devstack-up devstack-down devstack-smoke devstack-ci-smoke devstack-full-smoke responses-websocket-smoke v3-coding-tools-smoke v3-constrained-decoding-smoke codex-cli-devstack-smoke codex-cli-shell-tool-smoke codex-cli-coding-task-smoke codex-cli-task-matrix-smoke

CONFIG ?= config.yaml
BACKUP ?= ./.data/shim-backup.db
IMAGE ?= llama-shim:local
DEVSTACK_COMPOSE ?= docker-compose.devstack.yml
GO ?= go
GOLANGCI_LINT ?= golangci-lint
TOOL_CACHE_DIR ?= ./.cache
TOOL_TMP_DIR ?= ./.tmp
TOOL_PATH_PREFIX := $(if $(HOMEBREW_PREFIX),$(HOMEBREW_PREFIX)/bin:)$(if $(GOBIN),$(GOBIN):)$(if $(GOPATH),$(GOPATH)/bin:)
TOOL_PATH_ENV := PATH="$(TOOL_PATH_PREFIX)$(PATH)"

ifeq ($(CODEX_SANDBOX),)
TOOL_ENV := $(TOOL_PATH_ENV)
TOOL_PREP := @:
else
TOOL_CACHE_ROOT := $(abspath $(TOOL_CACHE_DIR))
TOOL_TMP_ROOT := $(abspath $(TOOL_TMP_DIR))
TOOL_ENV := $(TOOL_PATH_ENV) GOCACHE="$(TOOL_CACHE_ROOT)/go-build" GOLANGCI_LINT_CACHE="$(TOOL_CACHE_ROOT)/golangci-lint" TMPDIR="$(TOOL_TMP_ROOT)"
TOOL_PREP := mkdir -p "$(TOOL_CACHE_ROOT)/go-build" "$(TOOL_CACHE_ROOT)/golangci-lint" "$(TOOL_TMP_ROOT)"
endif

run:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) run ./cmd/shim -config $(CONFIG)

build:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) build ./cmd/shim ./cmd/shimctl ./cmd/upstream-sse-capture ./cmd/devstack-fixture ./cmd/responses-websocket-smoke

lint:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GOLANGCI_LINT) run

vet:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) vet ./...

test:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) test ./...

diff-check:
	git diff --check

ci-check: lint vet test build diff-check

maint-cleanup:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) run ./cmd/shimctl -config $(CONFIG) cleanup

maint-optimize:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) run ./cmd/shimctl -config $(CONFIG) optimize

maint-vacuum:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) run ./cmd/shimctl -config $(CONFIG) vacuum

maint-backup:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) run ./cmd/shimctl -config $(CONFIG) backup -out $(BACKUP)

docker-build:
	docker build -t $(IMAGE) .

compose-up:
	docker compose up --build

compose-down:
	docker compose down

devstack-up:
	docker compose -f $(DEVSTACK_COMPOSE) up -d --build

devstack-down:
	docker compose -f $(DEVSTACK_COMPOSE) down --remove-orphans

devstack-smoke:
	bash ./scripts/devstack-smoke.sh

devstack-ci-smoke: devstack-smoke responses-websocket-smoke v3-coding-tools-smoke v3-constrained-decoding-smoke

devstack-full-smoke: devstack-ci-smoke codex-cli-devstack-smoke codex-cli-shell-tool-smoke codex-cli-task-matrix-smoke

responses-websocket-smoke:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) run ./cmd/responses-websocket-smoke

v3-coding-tools-smoke:
	bash ./scripts/v3-coding-tools-smoke.sh

v3-constrained-decoding-smoke:
	bash ./scripts/v3-constrained-decoding-smoke.sh

codex-cli-devstack-smoke:
	bash ./scripts/codex-cli-devstack-smoke.sh

codex-cli-shell-tool-smoke:
	bash ./scripts/codex-cli-shell-tool-smoke.sh

codex-cli-coding-task-smoke:
	bash ./scripts/codex-cli-coding-task-smoke.sh

codex-cli-task-matrix-smoke:
	bash ./scripts/codex-cli-task-matrix-smoke.sh
