.PHONY: run build test maint-cleanup maint-optimize maint-vacuum maint-backup docker-build compose-up compose-down

CONFIG ?= config.yaml
BACKUP ?= ./.data/shim-backup.db
IMAGE ?= llama-shim:local

run:
	go run ./cmd/shim -config $(CONFIG)

build:
	go build ./cmd/shim ./cmd/shimctl ./cmd/upstream-sse-capture

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
