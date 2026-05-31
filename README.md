# calyx

Calyx is a robust, high-performance CLI utility crafted in Go, serving as a protective and supportive abstraction layer for AI agents. By wrapping complex infrastructure procedures, secure authentication, and remote storage mutations into predictable terminal commands, Calyx drastically reduces the cognitive load for AI cooperators, allowing them to focus entirely on domain logic.

Engineered with AI-first principles, it features native structured JSON outputs, strict timeout orchestration to eliminate orphaned processes, fully non-interactive modes, and self-updating mechanisms. It provides a seamless bridge between local environments and remote data backends, empowering autonomous agents to explore, learn, and execute tasks with absolute safety and reliability.

## Architecture

- `apps/backend` — the remote gRPC server (authentication, verification, persistence).
- `apps/cli` — the command-line tool operated by users and AI agents (not yet implemented).
- `shared/proto` — Protocol Buffer definitions and the generated Go code.

## Development

The devcontainer preinstalls the Go toolchain plus `buf`, `protoc-gen-go`,
`protoc-gen-go-grpc`, and `grpcurl`. On a bare host, install them with `make tools`.

```bash
make generate   # regenerate Go code from shared/proto/**/*.proto (buf lint + generate)
make build      # compile all packages
make test       # run the test suite
make run        # start the backend gRPC server on :50051 (override via CALYX_BACKEND_ADDR)
```

### Try the sample service

In one shell start the server with `make run`, then in another:

```bash
grpcurl -plaintext -d '{"name":"World"}' localhost:50051 mitsuhitofujita.calyx.v1.SampleService/Hello
# => {"message": "Hello, World."}
```

Server reflection is enabled, so `grpcurl -plaintext localhost:50051 list` discovers
services without local `.proto` files.
