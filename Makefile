.PHONY: all run server build fmt clean help


ifneq (,$(wildcard ./.env))
    include .env
    export
endif


all: run

run:
	@echo "🚀  Starting client…"
	go run cmd/*.go client

server:
	@echo "🖥️  Starting server…"
	go run cmd/*.go server

build:
	@echo "🔨  Building $(BINARY)…"
	go build -o bin/nimbus cmd/*.go
	@echo "✓  Built $(BINDIR)/$(BINARY)"

fmt:
	@echo "🎨  Formatting code…"
	gofmt -l -s -w .

clean:
	@echo "🧹  Cleaning up…"
	rm bin/*

help:
	@cat Makefile
