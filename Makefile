# Builds the CLI and the in-game LAN shim.
#
#   make            CLI + shim
#   make test       Go tests
#   make shim       only the ijl15.dll proxy
#   make install-shim GAME="/path/to/WarCraft III"

GO      ?= go
CC32    ?= i686-w64-mingw32-gcc
BIN     ?= bin/wc3lan
GAME    ?=

.PHONY: all build shim test install-shim clean

all: build shim

build:
	@mkdir -p $(dir $(BIN))
	$(GO) build -trimpath -o $(BIN) ./cmd/wc3lan
	@echo "built $(BIN)"

shim:
	$(CC32) -shared -O2 -Wall -Wno-format-truncation -Wno-cast-function-type \
		-o shim/ijl15.dll shim/wc3lan_shim.c shim/ijl15.def -lws2_32 -liphlpapi
	@echo "built shim/ijl15.dll"

test:
	$(GO) test ./...

install-shim:
	@test -n "$(GAME)" || { echo 'usage: make install-shim GAME="/path/to/WarCraft III"'; exit 1; }
	./shim/install.sh "$(GAME)"

clean:
	rm -rf bin
	rm -f shim/ijl15.dll
