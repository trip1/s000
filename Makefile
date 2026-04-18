SHELL := /bin/bash

.PHONY: build test lint bench integration profile cross-build release-artifacts backup restore-validate cli-health-inspect cli-completion compatibility-awscli compatibility-sdk-go compatibility-sdk-python compatibility-sdk-js validate-low-resource watch

DIST_DIR ?= dist
TARGET_OSES ?= linux freebsd darwin
TARGET_ARCHES ?= amd64 arm64

build:
	go build ./...

cross-build:
	rm -rf "$(DIST_DIR)/bin"
	mkdir -p "$(DIST_DIR)/bin"
	for goos in $(TARGET_OSES); do \
		for goarch in $(TARGET_ARCHES); do \
			ext=""; \
			if [ "$$goos" = "windows" ]; then ext=".exe"; fi; \
			out="$(DIST_DIR)/bin/s000-$$goos-$$goarch$$ext"; \
			echo "building $$out"; \
			CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" \
				go build -trimpath -ldflags='-s -w -buildid=' -o "$$out" ./cmd/s000; \
		done; \
	done

release-artifacts:
	bash ./scripts/release-artifacts.sh

backup:
	@echo "usage: make backup DATA_DIR=./data METADATA_DSN='file:./data/s000-metadata.db' OUT=./backup"
	go run ./cmd/s000ctl backup-create --data-dir "$(DATA_DIR)" --metadata-dsn "$(METADATA_DSN)" --out "$(OUT)"

restore-validate:
	@echo "usage: make restore-validate BACKUP=./backup"
	go run ./cmd/s000ctl restore-validate --path "$(BACKUP)"

cli-health-inspect:
	@echo "usage: make cli-health-inspect ENDPOINT=http://127.0.0.1:9000"
	go run ./cmd/s000ctl health-inspect --endpoint "$(ENDPOINT)"

cli-completion:
	@echo "usage: make cli-completion SHELL=bash"
	go run ./cmd/s000ctl completion --shell "$(SHELL)"

compatibility-awscli:
	bash ./scripts/awscli-e2e.sh

compatibility-sdk-go:
	bash ./scripts/sdk-smoke-go.sh

compatibility-sdk-python:
	bash ./scripts/sdk-smoke-python.sh

compatibility-sdk-js:
	bash ./scripts/sdk-smoke-js.sh

validate-low-resource:
	bash ./scripts/validate-low-resource.sh

test:
	go test ./...

lint:
	@files="$$(go list -f '{{$$dir := .Dir}}{{range .GoFiles}}{{$$dir}}/{{.}} {{end}}{{range .TestGoFiles}}{{$$dir}}/{{.}} {{end}}' ./...)"; \
	if [ -n "$$files" ]; then \
		unformatted="$$(gofmt -l $$files)"; \
		if [ -n "$$unformatted" ]; then \
			echo "Unformatted Go files:"; \
			echo "$$unformatted"; \
			exit 1; \
		fi; \
	fi
	go vet ./...

bench:
	go test -bench=. -run=^$$ ./...

profile:
	mkdir -p profiles
	go test -run=^$$ -bench='BenchmarkObjectIO' -benchmem -cpuprofile profiles/cpu.out -memprofile profiles/mem.out ./internal/blob

integration:
	go test -tags=integration ./test/integration/...

watch:
	@command -v air >/dev/null 2>&1 || { \
		echo "air is not installed. Install with:"; \
		echo "  go install github.com/air-verse/air@latest"; \
		exit 1; \
	}
	air -c .air.toml
