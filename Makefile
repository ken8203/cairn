PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin

.PHONY: build test install demo

build:
	go build -o cairn ./cmd/cairn

test:
	go test ./...

install:
	mkdir -p $(BIN)
	go build -o $(BIN)/cairn ./cmd/cairn

# docs/demo.webp, from docs/demo/demo.html: needs Google Chrome, node and img2webp.
# Lossless, since lossy sub-frame blending leaves ghost text.
demo:
	cd docs/demo && node grab.mjs "$$PWD" 20 23 && img2webp -loop 0 -d 50 -lossless -q 75 -m 4 frames/f*.png -o ../demo.webp; rm -rf frames chrome
