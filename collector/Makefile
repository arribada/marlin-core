GOPATH      	?= $(shell go env GOPATH)
GOBIN       	?= ${GOPATH}/bin
SUDO			?=sudo

.PHONY: build
build: ## Build.
build:
	@echo ">> Building"
	@mkdir -p bin
	@go build -o bin/collect ./cmd/collect

.PHONY: lpmproto
lpmproto: ## Generate protobuf for comms with LPM.
lpmproto:
	@echo ">> Running protoc"
	@protoc -I=./pkg/lpm/ --go_out=./pkg/lpm ./pkg/lpm/proto/core.proto
