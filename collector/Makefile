BINARY := coslash
PKG := ./cmd/coslash
FRONTEND := ../frontend
STAGED := internal/web/dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DIST := dist
PLATFORMS := darwin/arm64 darwin/amd64
ARGS :=

.PHONY: build run test check tidy clean release stage unstage smoke smoke-dev dist

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

# Cross-compile every shipped platform from one staged frontend build.
dist: stage
	@test -f $(STAGED)/index.html || { \
		echo "error: $(STAGED)/index.html is missing; refusing to package a binary with no frontend"; exit 1; }
	rm -rf $(DIST)
	mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		stem=$(BINARY)_$(VERSION)_$${goos}_$${goarch}; \
		mkdir -p $(DIST)/$$stem; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			go build -trimpath -ldflags '-s -w -X main.version=$(VERSION)' \
			-o $(DIST)/$$stem/$(BINARY) $(PKG) || exit 1; \
		cp ../LICENSE ../README.md $(DIST)/$$stem/; \
		tar -czf $(DIST)/$$stem.tar.gz -C $(DIST) $$stem; \
		rm -rf $(DIST)/$$stem; \
		echo "packaged $(DIST)/$$stem.tar.gz"; \
	done
	cd $(DIST) && shasum -a 256 *.tar.gz > checksums.txt

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
	rm -rf bin $(DIST)
