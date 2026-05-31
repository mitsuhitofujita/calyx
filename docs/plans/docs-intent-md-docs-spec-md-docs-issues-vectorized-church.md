# Plan: ISSUE-001 — Implement Sample `Hello` RPC in Backend

## Context

`calyx` is intended to become a GitHub **template repository** for Go CLIs that
collaborate with AI agents (`docs/intent.md`). The architecture (`docs/spec.md`)
splits into `apps/cli`, `apps/backend`, and `shared/proto`, communicating over
gRPC. Before building the real Google-OAuth + JWT auth flow, ISSUE-001 asks for a
minimal end-to-end gRPC slice — a `SampleService.Hello` RPC — to prove that proto
code generation, the network listener, and message (de)serialization all work.

**Current state:** the repo contains only docs and devcontainer config. There is
**no `go.mod`, no Go source, and no proto tooling installed** (`protoc`, `buf`,
`grpcurl`, and the `protoc-gen-*` plugins are all absent; Go 1.26.3 is present).
So this issue effectively bootstraps the whole Go project skeleton in addition to
the sample RPC. Decisions made here set precedent for the template.

## Environment facts (verified)

- Go 1.26.3; `GOTOOLCHAIN=local` → `go.mod` must declare `go 1.26` (not higher).
- `go install` targets land in `/go/bin`, which is already on `PATH`.
- `proxy.golang.org` reachable → module + tool downloads work.
- No proto compiler present. We will use **`buf`** (pure-Go compiler, installable
  via `go install` — avoids the C++ `protoc` apt dependency).

## Decisions (confirmed with user)

- **Codegen tool: `buf`** (pure-Go, `go install`; adds lint/breaking checks).
- **Generated `.pb.go` files are committed** → repo builds with plain `go build`.
- **Toolchain baked into `.devcontainer/Dockerfile`** (preinstalled on rebuild);
  Makefile `tools` target kept as a fallback for CI / non-devcontainer use.

## Target layout

```
go.mod / go.sum                 module github.com/mitsuhitofujita/calyx (go 1.26)
Makefile                        tools / generate / run / test / verify targets
buf.yaml                        buf v2 workspace (module = shared/proto, STANDARD lint)
buf.gen.yaml                    buf v2 codegen (local protoc-gen-go[-grpc], source_relative)
.devcontainer/Dockerfile        MODIFIED: install buf + protoc-gen-go[-grpc] + grpcurl
shared/proto/mitsuhitofujita/calyx/v1/
    sample.proto                hand-written (from issue spec)
    sample.pb.go                GENERATED (committed)
    sample_grpc.pb.go           GENERATED (committed)
apps/backend/
    main.go                     listener + grpc.Server + reflection + register
    internal/server/sample.go   SampleServiceServer implementation
    internal/server/sample_test.go  in-memory (bufconn) tests
```

Single Go module rooted at `github.com/mitsuhitofujita/calyx` — matches the
`go_package` import path given in the issue
(`.../shared/proto/mitsuhitofujita/calyx/v1;calyxv1`).

## Implementation steps

### 1. Bootstrap module + tooling
- `go mod init github.com/mitsuhitofujita/calyx` (set `go 1.26`).
- **Edit `.devcontainer/Dockerfile`** to preinstall the toolchain (pinned versions),
  e.g. a root `RUN` after the apt block:
  - `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.x`
  - `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.x`
  - `go install github.com/bufbuild/buf/cmd/buf@v1.5x.x`
  - `go install github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.9.x` (for `make verify`)
  Binaries land in `/go/bin` (already on `PATH`).
- **For this session** the running container does not yet have these tools, so run the
  equivalent `go install`s (or `make tools`) now to generate + test; the Dockerfile
  edit makes future rebuilds reproducible.
- Runtime deps `google.golang.org/grpc`, `google.golang.org/protobuf` are pulled in by
  `go mod tidy` after codegen.

### 2. Proto definition
- Create `shared/proto/mitsuhitofujita/calyx/v1/sample.proto` exactly as the issue
  specifies (package `mitsuhitofujita.calyx.v1`, `go_package` ending `;calyxv1`,
  `SampleService.Hello`, `HelloRequest{name}`, `HelloResponse{message}`).

### 3. buf config + generation
- `buf.yaml` (v2): module `shared/proto`, `lint: STANDARD`, `breaking: FILE`.
  (Proto already satisfies STANDARD: `v1` suffix, `*Service`, `*Request/*Response`.)
- `buf.gen.yaml` (v2): two local plugins (`protoc-gen-go`, `protoc-gen-go-grpc`),
  `out: shared/proto`, `opt: paths=source_relative` → outputs land beside the
  `.proto` at `shared/proto/mitsuhitofujita/calyx/v1/sample{,_grpc}.pb.go`.
- Run `buf lint && buf generate`, then `go mod tidy`.

### 4. Server implementation — `apps/backend/internal/server/sample.go`
- `type SampleServer struct { calyxv1.UnimplementedSampleServiceServer }` (forward-compat).
- `Hello(ctx, req)` returns `&calyxv1.HelloResponse{Message: "Hello, " + req.GetName() + "."}`.
  Empty name → `"Hello, ."` (matches issue Test Case 1; no special-casing).

### 5. Entrypoint — `apps/backend/main.go`
- Listen on addr from `CALYX_BACKEND_ADDR`, default `:50051`.
- `grpc.NewServer()`, `calyxv1.RegisterSampleServiceServer(...)`,
  **`reflection.Register(srv)`** (required so the issue's `grpcurl` command, which
  passes no proto files, can resolve the service), then `Serve`.

### 6. Tests — `apps/backend/internal/server/sample_test.go`
- In-memory server via `google.golang.org/grpc/test/bufconn`; client via the
  current non-deprecated API:
  `grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(lis.DialContext... ), grpc.WithTransportCredentials(insecure.NewCredentials()))`.
- Table-driven: `""` → `"Hello, ."`, `"Alice"` → `"Hello, Alice."`.

### 7. Makefile + README
- `Makefile`: `tools` (go install the 3 binaries), `generate` (buf lint+generate),
  `run` (`go run ./apps/backend`), `test` (`go test ./...`), `verify` (grpcurl call).
- README: short "Development" section (prereqs, `make tools`, `make generate`,
  `make run`, `make test`).

## Verification (end-to-end)

1. `make generate` (tools preinstalled in container) — generated `.pb.go` files
   appear, `buf lint` passes.
2. `go build ./...` and `go vet ./...` — clean.
3. `go test ./...` — bufconn tests pass (both cases: `""`→`"Hello, ."`, `"Alice"`→`"Hello, Alice."`).
4. Manual: `make run` (listens on `:50051`), then in another shell
   `grpcurl -plaintext -d '{"name":"World"}' localhost:50051 mitsuhitofujita.calyx.v1.SampleService/Hello`
   → `{"message":"Hello, World."}` (works thanks to server reflection).

## Out of scope (deferred)

- `apps/cli` scaffolding, auth/JWT/credential-store, auto-update, telemetry,
  metadata-schema output, timeout/orphan-process handling. ISSUE-001 is backend-only.
