BINARY := coslash
PKG := ./cmd/coslash
FRONTEND := ../frontend
STAGED := internal/web/dist
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
ARGS :=

.PHONY: build run test tidy clean release stage

# One command from a clean checkout to a single executable that needs no Node.
release: stage
	go build -ldflags '-X main.version=$(VERSION)' -o bin/$(BINARY) $(PKG)
	@echo "built bin/$(BINARY) $(VERSION)"

# Go cannot embed $(FRONTEND)/dist directly, so copy it into the module first.
stage:
	npm --prefix $(FRONTEND) ci
	npm --prefix $(FRONTEND) run build
	@test -f $(FRONTEND)/dist/index.html || { \
		echo "error: the frontend build produced no $(FRONTEND)/dist/index.html"; exit 1; }
	rm -rf $(STAGED)
	mkdir -p $(STAGED)
	cp -R $(FRONTEND)/dist/. $(STAGED)/
	touch $(STAGED)/.gitkeep

build:
	go build -o bin/$(BINARY) $(PKG)

run:
	go run $(PKG) $(ARGS)

test:
	go test ./...

tidy:
	go mod tidy

clean:
	rm -rf bin
	find $(STAGED) -mindepth 1 ! -name .gitkeep -delete
