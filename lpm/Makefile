GOPATH      	?= $(shell go env GOPATH)
GOBIN       	?= ${GOPATH}/bin

.PHONY: proto
proto: ## Run protoc.
proto:
	@echo ">> Running protoc"
	@protoc -I=./cmd/lpm/ --go_out=./cmd/lpm ./cmd/lpm/proto/core.proto
