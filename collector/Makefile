BINARY := coslash
PKG := ./cmd/coslash
FRONTEND := ../frontend
STAGED := internal/web/dist
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
ARGS :=

.PHONY: build run test check tidy clean release stage unstage smoke smoke-dev

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

# Without this a build after `make release` would embed that release's frontend.
unstage:
	@mkdir -p $(STAGED)
	@find $(STAGED) -mindepth 1 ! -name .gitkeep -delete
	@touch $(STAGED)/.gitkeep

build: unstage
	go build -o bin/$(BINARY) $(PKG)

run: unstage
	go run $(PKG) --no-open $(ARGS)

test:
	go test ./...

check:
	@unformatted=$$(gofmt -l .); \
		test -z "$$unformatted" || { echo "gofmt needed:"; echo "$$unformatted"; exit 1; }
	go vet ./...

# Checks the release binary over HTTP; run `make release` first.
smoke:
	./scripts/smoke.sh bin/$(BINARY) embedded

# Checks that a dev binary embeds no frontend; run `make build` first.
smoke-dev:
	./scripts/smoke.sh bin/$(BINARY) bare

tidy:
	go mod tidy

clean: unstage
	rm -rf bin
