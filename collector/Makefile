BINARY := coslash
PKG := ./cmd/coslash
ARGS :=

.PHONY: build run test tidy clean

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
