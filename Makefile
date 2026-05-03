.PHONY: run build lint test vet diff-check ci-check maint-cleanup maint-optimize maint-vacuum maint-backup docker-build compose-up compose-down devstack-up devstack-down devstack-smoke devstack-ci-smoke devstack-full-smoke responses-compat-external-smoke responses-compat-external-real-smoke responses-websocket-smoke v3-coding-tools-smoke v3-constrained-decoding-smoke v3-vllm-constrained-smoke codex-cli-devstack-smoke codex-cli-shell-tool-smoke codex-cli-coding-task-smoke codex-cli-task-matrix-smoke codex-cli-real-upstream-smoke codex-eval-smoke codex-eval-core codex-eval-core-shell codex-eval-core-websocket codex-eval-core-profiles codex-eval-bench-lite codex-eval-loop-bench-lite codex-eval-shim-native codex-eval-shim-native-websocket codex-eval-shim-native-profiles codex-eval-real-upstream codex-eval-real-upstream-expanded codex-eval-matrix codex-eval-loop codex-eval-prune codex-eval-clean

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
	$(TOOL_ENV) $(GO) build ./cmd/shim ./cmd/shimctl ./cmd/upstream-sse-capture ./cmd/devstack-fixture ./cmd/responses-websocket-smoke ./cmd/codex-eval-runner

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

responses-compat-external-smoke:
	bash ./scripts/responses-compat-external-smoke.sh

responses-compat-external-real-smoke:
	RESPONSES_COMPAT_RUN_MODE=real-upstream bash ./scripts/responses-compat-external-smoke.sh

responses-websocket-smoke:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) run ./cmd/responses-websocket-smoke

v3-coding-tools-smoke:
	bash ./scripts/v3-coding-tools-smoke.sh

v3-constrained-decoding-smoke:
	bash ./scripts/v3-constrained-decoding-smoke.sh

v3-vllm-constrained-smoke:
	bash ./scripts/v3-vllm-constrained-smoke.sh

codex-cli-devstack-smoke:
	bash ./scripts/codex-cli-devstack-smoke.sh

codex-cli-shell-tool-smoke:
	bash ./scripts/codex-cli-shell-tool-smoke.sh

codex-cli-coding-task-smoke:
	bash ./scripts/codex-cli-coding-task-smoke.sh

codex-cli-task-matrix-smoke:
	bash ./scripts/codex-cli-task-matrix-smoke.sh

codex-cli-real-upstream-smoke:
	bash ./scripts/codex-cli-real-upstream-smoke.sh

codex-eval-smoke:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} bash ./scripts/codex-eval-runner.sh

codex-eval-core:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} CODEX_EVAL_SUITE=codex-core bash ./scripts/codex-eval-runner.sh

codex-eval-core-shell:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} CODEX_EVAL_SUITE=codex-core-shell CODEX_EVAL_UNIFIED_EXEC=false bash ./scripts/codex-eval-runner.sh

codex-eval-core-websocket:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} CODEX_EVAL_SUITE=codex-core-websocket CODEX_EVAL_WEBSOCKETS=true bash ./scripts/codex-eval-runner.sh

codex-eval-core-profiles: codex-eval-core codex-eval-core-shell codex-eval-core-websocket

codex-eval-bench-lite:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} CODEX_EVAL_SUITE=codex-bench-lite bash ./scripts/codex-eval-runner.sh

codex-eval-loop-bench-lite:
	CODEX_EVAL_CONTROL_SUITE=codex-bench-lite CODEX_EVAL_CANDIDATE_SUITE=codex-bench-lite bash ./scripts/codex-eval-loop.sh

codex-eval-shim-native:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} CODEX_EVAL_SUITE=codex-shim-native bash ./scripts/codex-eval-runner.sh

codex-eval-shim-native-websocket:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} CODEX_EVAL_SUITE=codex-shim-native-websocket CODEX_EVAL_WEBSOCKETS=true bash ./scripts/codex-eval-runner.sh

codex-eval-shim-native-apply-patch-freeform:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} CODEX_EVAL_SUITE=codex-shim-native-apply-patch-freeform CODEX_EVAL_APPLY_PATCH_FREEFORM=true CODEX_EVAL_APPLY_PATCH_TOOL_TYPE=freeform bash ./scripts/codex-eval-runner.sh

codex-eval-shim-native-apply-patch-function:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} CODEX_EVAL_SUITE=codex-shim-native-apply-patch-function CODEX_EVAL_APPLY_PATCH_FREEFORM=false CODEX_EVAL_APPLY_PATCH_TOOL_TYPE=function bash ./scripts/codex-eval-runner.sh

codex-eval-shim-native-apply-patch-disabled:
	OPENAI_API_KEY=$${OPENAI_API_KEY:-shim-dev-key} CODEX_EVAL_SUITE=codex-shim-native-apply-patch-disabled CODEX_EVAL_APPLY_PATCH_FREEFORM=false CODEX_EVAL_APPLY_PATCH_TOOL_TYPE=disabled bash ./scripts/codex-eval-runner.sh

codex-eval-shim-native-apply-patch-profiles: codex-eval-shim-native-apply-patch-freeform codex-eval-shim-native-apply-patch-function codex-eval-shim-native-apply-patch-disabled

codex-eval-shim-native-profiles: codex-eval-shim-native codex-eval-shim-native-websocket codex-eval-shim-native-apply-patch-profiles

codex-eval-real-upstream:
	CODEX_EVAL_SUITE=$${CODEX_EVAL_SUITE:-codex-real-upstream} bash ./scripts/codex-eval-runner.sh

codex-eval-real-upstream-expanded:
	CODEX_EVAL_SUITE=codex-real-upstream-expanded bash ./scripts/codex-eval-runner.sh

codex-eval-matrix:
	$(TOOL_PREP)
	$(TOOL_ENV) $(GO) run ./cmd/codex-eval-runner matrix $${CODEX_EVAL_MATRIX_RUNS:-.tmp/codex-eval-runs}

codex-eval-loop:
	bash ./scripts/codex-eval-loop.sh

codex-eval-prune:
	find .tmp/codex-eval-runs .tmp/codex-eval-loops -path '*/codex-home/.tmp' -type d -prune -exec rm -rf {} + 2>/dev/null || true

codex-eval-clean:
	rm -rf .tmp/codex-eval-runs .tmp/codex-eval-loops
