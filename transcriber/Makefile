GO111MODULE 	?= on
export GO111MODULE

GOPATH			?= $(shell go env GOPATH)

# We want to make sure that we use the same source for whisper when building
# libwhisper.a and when depending on in our Go code.
# So we have a replace directive in our go.mod, to ./whisper.cpp
# We vendor it for now, because it's the simplest solution that works both
# for local dev, and for in prod. But might do smarter things instead later.
WHISPER_CPP = ${PWD}/whisper.cpp
LIBWHISPER = ${WHISPER_CPP}/libwhisper.a
TRANSCRIBE = transcribe

.PHONY: all
all: build

.PHONY: clean
clean: ## Clean.
clean:
	@cd ${WHISPER_CPP} && make clean
	@rm ./bin/${TRANSCRIBE}

.PHONY: vendor
vendor: ## Vendor.
vendor:
	@./bin/vendor.sh

.PHONY: build
build: ## Build.
build: ${LIBWHISPER} ./bin/${TRANSCRIBE}
	@echo ">> Building"

${LIBWHISPER}:
	@cd ${WHISPER_CPP}/bindings/go && make whisper

.PHONY: ./bin/${TRANSCRIBE}
./bin/${TRANSCRIBE}: # build the binary
./bin/${TRANSCRIBE}: ${LIBWHISPER}
	@mkdir -p bin
	@C_INCLUDE_PATH=${WHISPER_CPP} LIBRARY_PATH=${WHISPER_CPP} go build -o ./bin/${TRANSCRIBE} ./cmd/transcribe/main.go

.PHONY: prelint
prelint: ## Prepare for linting.
# Note: looks like just ${LIBWHISPER} (instead of build), is not enough, as it does not make whisper.h available or something.
prelint: build
	@echo ">> Prepare deps for linting"

.PHONY: transcribe
transcribe: ## transcribe audio from network
transcribe: ./bin/${TRANSCRIBE}
	@echo ">> Running transcribe"
	@./bin/${TRANSCRIBE} -source=http://localhost:8362 -host=:8361 -modelpath=./data/models/ggml-base.en.bin -stdout=true -v=true -gcgrace=120 -gcinterval=60

default: ./bin/${TRANSCRIBE}
