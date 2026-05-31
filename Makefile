# Calyx developer tasks.
#
# In the devcontainer the proto toolchain is preinstalled (see
# .devcontainer/Dockerfile). For CI or a bare host, run `make tools` first.

# Pinned tool versions (keep in sync with .devcontainer/Dockerfile).
PROTOC_GEN_GO_VERSION      := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
BUF_VERSION                := v1.70.0
GRPCURL_VERSION            := v1.9.3

BACKEND_ADDR ?= localhost:50051

.PHONY: tools generate lint run test verify tidy build

## tools: install the proto/grpc toolchain into $GOBIN (fallback for non-devcontainer hosts)
tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	go install github.com/fullstorydev/grpcurl/cmd/grpcurl@$(GRPCURL_VERSION)

## lint: lint the protobuf definitions
lint:
	buf lint

## generate: regenerate Go code from the .proto files
generate:
	buf lint
	buf generate
	go mod tidy

## build: compile all packages
build:
	go build ./...

## test: run the Go test suite
test:
	go test ./...

## run: start the backend gRPC server (override port with CALYX_BACKEND_ADDR)
run:
	go run ./apps/backend

## verify: smoke-test a running backend with grpcurl (start `make run` in another shell)
verify:
	grpcurl -plaintext -d '{"name":"World"}' $(BACKEND_ADDR) mitsuhitofujita.calyx.v1.SampleService/Hello

## tidy: tidy go.mod / go.sum
tidy:
	go mod tidy
