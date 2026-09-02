BINARY := coslash
PKG := ./cmd/coslash
HELPER := coslash-helper
HELPER_PKG := ./cmd/coslash-helper
HELPER_PLATFORMS := linux/amd64 linux/arm64
HELPER_DIST := dist/helper
HELPER_ASSETS := internal/remote/helperassets
FRONTEND := ../frontend
STAGED := internal/web/dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DIST := dist
PLATFORMS := darwin/arm64 darwin/amd64
ARGS :=
LDFLAGS := -X main.version=$(VERSION)
EMBEDDED_LDFLAGS := -s -w -X main.version=$(VERSION) -X github.com/centauri-ai/coslash/collector/internal/remote.embeddedHelperVersion=$(VERSION)
MODELS := internal/session/models.json
LITELLM_URL := https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json
OPENCODE_MODELS_URL := https://models.opencode.ai/api.json

.PHONY: build run test check tidy clean release stage unstage smoke smoke-dev dist models \
	helper helper-dist helper-assets test-embedded-helpers

release: stage helper-assets
	@set -eu; trap 'rm -rf $(HELPER_ASSETS)' EXIT; \
		go build -tags embedded_helpers -ldflags '$(EMBEDDED_LDFLAGS)' -o bin/$(BINARY) $(PKG)
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
dist: stage helper-assets
	@test -f $(STAGED)/index.html || { \
		echo "error: $(STAGED)/index.html is missing; refusing to package a binary with no frontend"; exit 1; }
	rm -rf $(DIST)
	mkdir -p $(DIST)
	@set -eu; trap 'rm -rf $(HELPER_ASSETS)' EXIT; for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		stem=$(BINARY)_$(VERSION)_$${goos}_$${goarch}; \
		mkdir -p $(DIST)/$$stem; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			go build -trimpath -tags embedded_helpers -ldflags '$(EMBEDDED_LDFLAGS)' \
			-o $(DIST)/$$stem/$(BINARY) $(PKG) || exit 1; \
		cp ../LICENSE ../README.md $(DIST)/$$stem/; \
		find $(DIST)/$$stem -exec touch -t 198001010000 {} +; \
		COPYFILE_DISABLE=1 tar --format ustar -cf - -C $(DIST) $$stem | gzip -n > $(DIST)/$$stem.tar.gz; \
		rm -rf $(DIST)/$$stem; \
		echo "packaged $(DIST)/$$stem.tar.gz"; \
	done
	cd $(DIST) && shasum -a 256 *.tar.gz > checksums.txt

# The Linux collection helper, for local iteration on the host platform.
helper:
	CGO_ENABLED=0 go build -ldflags '-X main.build=$(VERSION)' -o bin/$(HELPER) $(HELPER_PKG)

# Generated immediately before a tagged Coslash build. The release executable
# embeds these exact bytes; the trap in every consumer removes them afterward.
helper-assets:
	rm -rf $(HELPER_ASSETS)
	mkdir -p $(HELPER_ASSETS)
	@for platform in $(HELPER_PLATFORMS); do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			go build -trimpath -ldflags '-s -w -X main.build=$(VERSION)' \
			-o $(HELPER_ASSETS)/$(HELPER)_$${goos}_$${goarch} $(HELPER_PKG) || exit 1; \
	done

test-embedded-helpers: helper-assets
	@set -eu; trap 'rm -rf $(HELPER_ASSETS)' EXIT; \
		go test -tags embedded_helpers \
		-ldflags '-X github.com/centauri-ai/coslash/collector/internal/remote.embeddedHelperVersion=$(VERSION)' \
		-run '^TestEmbeddedHelperReleaseProvider$$' ./internal/remote

# Cross-compiles the shipped helper targets. Reproducible on purpose: -trimpath
# and no CGO, so rebuilding one commit yields identical binaries and therefore
# the digests release metadata is signed over. Digests cover the binaries
# themselves rather than an archive, because installation verifies the exact
# executable it is about to place and run.
helper-dist:
	rm -rf $(HELPER_DIST)
	mkdir -p $(HELPER_DIST)
	@for platform in $(HELPER_PLATFORMS); do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		stem=$(HELPER)_$(VERSION)_$${goos}_$${goarch}; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			go build -trimpath -ldflags '-s -w -X main.build=$(VERSION)' \
			-o $(HELPER_DIST)/$$stem $(HELPER_PKG) || exit 1; \
		echo "built $(HELPER_DIST)/$$stem"; \
	done
	cd $(HELPER_DIST) && shasum -a 256 $(HELPER)_* > checksums.txt
	@cat $(HELPER_DIST)/checksums.txt

# Without this a build after `make release` would embed that release's frontend.
unstage:
	@mkdir -p $(STAGED)
	@find $(STAGED) -mindepth 1 ! -name .gitkeep -delete
	@touch $(STAGED)/.gitkeep

build: unstage helper-assets
	@set -eu; trap 'rm -rf $(HELPER_ASSETS)' EXIT; \
		go build -tags embedded_helpers -ldflags '$(EMBEDDED_LDFLAGS)' -o bin/$(BINARY) $(PKG)

run: unstage helper-assets
	@set -eu; trap 'rm -rf $(HELPER_ASSETS)' EXIT; \
		go run -tags embedded_helpers -ldflags '$(EMBEDDED_LDFLAGS)' $(PKG) --no-open $(ARGS)

test:
	go test ./...
	$(MAKE) test-embedded-helpers

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

# Rewrites the embedded model table from LiteLLM. The Models workflow runs this
# daily and opens a pull request; run it by hand for a model LiteLLM just added.
models:
	@set -eu; \
		trap 'rm -f $(MODELS).new $(MODELS).opencode' EXIT; \
		curl -sfL $(OPENCODE_MODELS_URL) -o $(MODELS).opencode; \
		curl -sfL $(LITELLM_URL) | jq -S --slurpfile opencode $(MODELS).opencode -f scripts/models.jq > $(MODELS).new; \
		test -s $(MODELS).new || { echo "error: the downloads produced no model table"; exit 1; }; \
		jq -e 'length > 50' $(MODELS).new > /dev/null || { \
			echo "error: $$(jq length $(MODELS).new) models is too few; an upstream format changed"; exit 1; }; \
		jq -e --slurpfile opencode $(MODELS).opencode \
			'. as $$table | all($$opencode[0].opencode.models | keys[]; . as $$id | $$table | has("opencode/" + $$id))' \
			$(MODELS).new > /dev/null || { echo "error: the OpenCode model merge is incomplete"; exit 1; }; \
		mv $(MODELS).new $(MODELS); \
		rm -f $(MODELS).opencode; \
		trap - EXIT; \
		echo "wrote $(MODELS) ($$(jq length $(MODELS)) models)"

clean: unstage
	rm -rf bin $(DIST)
