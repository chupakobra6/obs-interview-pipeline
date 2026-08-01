.PHONY: build fmt test check install doctor

BIN := bin/obs-interview-processor

build:
	go build -o $(BIN) ./cmd/obs-interview-processor

test:
	go test ./...

fmt:
	gofmt -w cmd internal

check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	go test ./...

install: build
	$(BIN) install

doctor: build
	$(BIN) doctor
