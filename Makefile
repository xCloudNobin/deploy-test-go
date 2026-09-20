GO ?= go
GOMAXPROCS ?= 2

# Build marker. Injected via -ldflags so every binary carries its source
# revision. VERSION/COMMIT/BUILD_TIME default to the git state at build time.
VERSION  ?= 1.0.0
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILDTIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X main.version="$(VERSION)" \
	-X main.commit="$(COMMIT)" \
	-X main.buildTime="$(BUILDTIME)"

.PHONY: all build test vet run clean

all: build

build:
	GOMAXPROCS=$(GOMAXPROCS) GOFLAGS="-p=2" $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/taskboard .

test:
	GOMAXPROCS=$(GOMAXPROCS) GOFLAGS="-p=2" $(GO) test ./...

vet:
	GOMAXPROCS=$(GOMAXPROCS) GOFLAGS="-p=2" $(GO) vet ./...

run: build
	./bin/taskboard

clean:
	rm -rf bin