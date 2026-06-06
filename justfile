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

# Compile all packages and output binaries.
build:
    @mkdir -p bin
    go build -o bin/calyx ./apps/cli
    go build -o bin/backend ./apps/backend

# Run the Go test suite.
test:
    go test ./...

# Start the backend gRPC server.
run:
    go run ./apps/backend

# Smoke-test a running backend with grpcurl (start `just run` in another shell).
verify addr=backend_addr:
    grpcurl -plaintext -d '{"name":"World"}' {{addr}} mitsuhitofujita.calyx.v1.SampleService/Hello

# Exchange a Google ID token for a Calyx session JWT via AuthService.Login
# (start `just run` in another shell). This is a low-level grpcurl probe:
# `calyx auth login` now performs this exchange directly and persists the session
# token, so it no longer prints an ID token to stdout. Supply a token inline or set
# it once via the CALYX_GOOGLE_ID_TOKEN env var.
login id_token=env_var_or_default("CALYX_GOOGLE_ID_TOKEN", "") addr=backend_addr:
    grpcurl -plaintext -d '{"id_token":"{{id_token}}"}' {{addr}} mitsuhitofujita.calyx.v1.AuthService/Login

# Tidy go.mod / go.sum.
tidy:
    go mod tidy
