GOPATH      	?= $(shell go env GOPATH)
GOBIN       	?= ${GOPATH}/bin
SUDO			?=sudo

.PHONY: build
build: ## Build.
build:
	@echo ">> Building"
	@mkdir -p bin
	@go build -o bin/collect ./cmd/collect

.PHONY: protoc
protoc: ## Generate protobuf for comms with LPM.
protoc:
	@echo ">> Running protoc"
	@protoc -I=./pkg/sat/ --go_out=./pkg/sat ./pkg/sat/proto/core.proto
