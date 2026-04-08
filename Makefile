.PHONY: build test lint clean install

BINARY := s3lo
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/s3lo

test:
	go test ./... -v

test-integration:
	S3LO_TEST_DOCKER=1 go test ./... -v

lint:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -rf dist/

install: build
	sudo cp $(BINARY) /usr/local/bin/
