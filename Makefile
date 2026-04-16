.PHONY: run build lint test vet maint-cleanup maint-optimize maint-vacuum maint-backup docker-build compose-up compose-down devstack-up devstack-down devstack-smoke

CONFIG ?= config.yaml
BACKUP ?= ./.data/shim-backup.db
IMAGE ?= llama-shim:local
DEVSTACK_COMPOSE ?= docker-compose.devstack.yml
GOLANGCI_LINT ?= golangci-lint

run:
	go run ./cmd/shim -config $(CONFIG)

build:
	go build ./cmd/shim ./cmd/shimctl ./cmd/upstream-sse-capture ./cmd/devstack-fixture

lint:
	$(GOLANGCI_LINT) run

vet:
	go vet ./...

test:
	go test ./...

maint-cleanup:
	go run ./cmd/shimctl -config $(CONFIG) cleanup

maint-optimize:
	go run ./cmd/shimctl -config $(CONFIG) optimize

maint-vacuum:
	go run ./cmd/shimctl -config $(CONFIG) vacuum

maint-backup:
	go run ./cmd/shimctl -config $(CONFIG) backup -out $(BACKUP)

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
