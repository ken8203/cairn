PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin

.PHONY: build test install

build:
	go build -o cairn ./cmd/cairn

test:
	go test ./...

install:
	mkdir -p $(BIN)
	go build -o $(BIN)/cairn ./cmd/cairn
