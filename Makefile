.PHONY: all run server build fmt clean help

ifneq (,$(wildcard ./.env))
    include .env
    export
endif

BINDIR=bin
BINARY=nimbus

all: run

run:
	@echo "🚀  Starting client…"
	go run cmd/*.go client

server:
	@echo "🖥️  Starting server…"
	go run cmd/*.go server

build:
	@echo "🔨  Building $(BINARY)…"
	go build -o ${BINDIR}/${BINARY} cmd/*.go
	@echo "✓  Built $(BINDIR)/$(BINARY)"

fmt:
	@echo "🎨  Formatting code…"
	gofmt -l -s -w .

clean:
	@echo "🧹  Cleaning up…"
	rm bin/*

install: build
	@echo "📦  Installing $(BINARY) to /usr/local/bin…"
	install -m 0755 ${BINDIR}/${BINARY} /usr/local/bin/${BINARY}
	@echo "✓  Installed $(BINARY) to /usr/local/bin"

help:
	@cat Makefile
