GOPATH      	?= $(shell go env GOPATH)
GOBIN       	?= ${GOPATH}/bin
SUDO			?=sudo

.PHONY: build
build: ## Build.
build:
	@echo ">> Building"
	@mkdir -p bin
	@go build -o bin/collect ./cmd/collect
