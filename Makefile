ifneq (,$(wildcard ./.env))
    include .env
    export
endif

BINDIR=bin
BINARY=nimbus

.PHONY: all
all: run

.PHONY: run
run:
	@echo "🚀  Starting client…"
	go run cmd/*.go client

.PHONY: docker-up
docker-up:
	@echo "🚀  Starting docker compose…"
	docker-compose -f docker-compose.dev.yaml up -d

.PHONY: docker-down
docker-down:
	@echo "💤 Shutting down docker compose..."
	docker-compose -f docker-compose.dev.yaml down

.PHONY: docker-down-volumes
docker-down-volumes:
	@echo "🔇 Removing docker compose volumes..."
	docker-compose -f docker-compose.dev.yaml down -v

.PHONY: server
server:
	@echo "🖥️  Starting server…"
	go run cmd/*.go server

.PHONY: build
build:
	@echo "🔨  Building $(BINARY)…"
	go build -o ${BINDIR}/${BINARY} cmd/*.go
	@echo "✓  Built $(BINDIR)/$(BINARY)"

.PHONY: lint
lint: fmt
	@echo "🔍  Linting code..."
	golangci-lint run -v

.PHONY: fmt
fmt: sql-fmt
	@echo "🎨  Formatting code…"
	golangci-lint fmt -v

.PHONY: clean
clean:
	@echo "🧹  Cleaning up…"
	rm bin/*

.PHONY: sqlc
sqlc:
	@echo "🗄️  Generating SQLC code..."
	sqlc generate

.PHONY: sql-fmt
sql-fmt:
	@echo "🎨 Formatting SQL"
	pg_format -i internal/sql/query.sql internal/sql/schema.sql

.PHONY: install
install: build
	@echo "📦  Installing $(BINARY) to /usr/local/bin…"
	install -m 0755 ${BINDIR}/${BINARY} /usr/local/bin/${BINARY}
	@echo "✓  Installed $(BINARY) to /usr/local/bin"

.PHONY: help
help:
	@cat Makefile
