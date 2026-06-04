# Calyx developer tasks.
#
# In the devcontainer the proto toolchain is preinstalled (see
# .devcontainer/Dockerfile). For CI or a bare host, run `just tools` first.

# Pinned tool versions (keep in sync with .devcontainer/Dockerfile).
protoc_gen_go_version      := "v1.36.11"
protoc_gen_go_grpc_version := "v1.6.2"
buf_version                := "v1.70.0"
grpcurl_version            := "v1.9.3"

backend_addr               := "localhost:50051"

# Show the list of recipes by default.
default:
    @just --list

# Install the proto/grpc toolchain into $GOBIN (fallback for non-devcontainer hosts).
tools:
    go install google.golang.org/protobuf/cmd/protoc-gen-go@{{protoc_gen_go_version}}
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@{{protoc_gen_go_grpc_version}}
    go install github.com/bufbuild/buf/cmd/buf@{{buf_version}}
    go install github.com/fullstorydev/grpcurl/cmd/grpcurl@{{grpcurl_version}}

# Lint the protobuf definitions.
lint:
    buf lint

# Regenerate Go code from the .proto files.
generate: lint
    buf generate
    go mod tidy

# Compile all packages.
build:
    go build ./...

# Run the Go test suite.
test:
    go test ./...

# Start the backend gRPC server.
run:
    go run ./apps/backend

# Smoke-test a running backend with grpcurl (start `just run` in another shell).
verify addr=backend_addr:
    grpcurl -plaintext -d '{"name":"World"}' {{addr}} mitsuhitofujita.calyx.v1.SampleService/Hello

# Tidy go.mod / go.sum.
tidy:
    go mod tidy
