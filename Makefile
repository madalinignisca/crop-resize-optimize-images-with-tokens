# Default target builds the pure-Go binary (jpeg/png/gif, no libvips required).
# The `vips` variants build the production backend and need libvips + cgo.

BIN := bin/imgd

.PHONY: build build-vips test test-race vet fmt fmt-check tidy run docker clean

build:
	go build -o $(BIN) ./cmd/imgd

build-vips:
	CGO_ENABLED=1 go build -tags vips -o $(BIN) ./cmd/imgd

test:
	go test ./...

test-race:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

tidy:
	go mod tidy

# Runs the pure-Go binary against ./.env (copy from .env.example first).
run: build
	set -a; . ./.env; set +a; $(BIN)

docker:
	docker build -t imgd:latest .

clean:
	rm -rf bin
