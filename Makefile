BIN := bin/obs-interview-processor
GO_FILES := $(shell find cmd internal -type f -name '*.go' | sort)
NOTIFIER_SOURCE := cmd/obs-interview-processor/obs_interview_notifier.swift

.DEFAULT_GOAL := help

.PHONY: help setup build fmt fmt-check notifier-check test test-race check install doctor clean

help:
	@printf "Available commands:\n"
	@printf "  make setup      # download Go modules and build Telegram Harvest\n"
	@printf "  make build      # build the local worker binary\n"
	@printf "  make fmt        # format Go sources\n"
	@printf "  make notifier-check # type-check the native macOS notifier\n"
	@printf "  make test       # run unit and disposable integration tests\n"
	@printf "  make test-race  # run all tests with the race detector\n"
	@printf "  make check      # formatting, module, vet, and test validation\n"
	@printf "  make install    # install worker, OBS hook, config, and LaunchAgent\n"
	@printf "  make doctor     # validate the installed local pipeline\n"
	@printf "  make clean      # remove repo-local build artifacts\n"

setup:
	go mod download
	$(MAKE) -s -C ../telegram-harvest build

build:
	@mkdir -p "$(dir $(BIN))"
	go build -trimpath -o $(BIN) ./cmd/obs-interview-processor

fmt:
	gofmt -w $(GO_FILES)

fmt-check:
	@unformatted="$$(gofmt -l $(GO_FILES))"; test -z "$$unformatted" || { printf "Unformatted Go files:\n%s\n" "$$unformatted"; exit 1; }

notifier-check:
	/usr/bin/swiftc -swift-version 5 -typecheck -framework AppKit -framework UserNotifications $(NOTIFIER_SOURCE)

test:
	go test ./...

test-race:
	go test -race ./...

check: fmt-check notifier-check
	go mod tidy -diff
	go mod verify
	go vet ./...
	go test ./...

install: build
	$(BIN) install

doctor: build
	$(BIN) doctor

clean:
	rm -rf bin
