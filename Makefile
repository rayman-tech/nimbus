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

.PHONY: server
server:
	@echo "🖥️  Starting server…"
	go run cmd/*.go server

.PHONY: build
build:
	@echo "🔨  Building $(BINARY)…"
	go build -o ${BINDIR}/${BINARY} cmd/*.go
	@echo "✓  Built $(BINDIR)/$(BINARY)"

.PHONY: fmt
fmt:
	@echo "🎨  Formatting code…"
	gofmt -l -s -w .

.PHONY: clean
clean:
	@echo "🧹  Cleaning up…"
	rm bin/*

.PHONY: install
install: build
	@echo "📦  Installing $(BINARY) to /usr/local/bin…"
	install -m 0755 ${BINDIR}/${BINARY} /usr/local/bin/${BINARY}
	@echo "✓  Installed $(BINARY) to /usr/local/bin"

.PHONY: help
help:
	@cat Makefile
