# Plan: ISSUE-004 — Add `calyx sample hello <name>` Command

## Context

Per `docs/intent.md` and `docs/spec.md`, Calyx is a template repository for CLIs that
collaborate with AI agents, where the CLI (`apps/cli`) is a gRPC client of the backend
(`apps/backend`). Prior issues bootstrapped the backend `SampleService.Hello` RPC
(ISSUE-001) and a skeleton `calyx` CLI that only supports `--version`/usage (ISSUE-003).

ISSUE-004 makes the CLI actually talk to the backend: implement
`calyx sample hello <name>`, which calls the existing
`mitsuhitofujita.calyx.v1.SampleService.Hello` RPC with `<name>` and prints the returned
greeting. This is the first end-to-end proof that the CLI→gRPC→backend pipeline works
from the client side.

## Scope

- **Modify only** `apps/cli/main.go` (the single file the issue maps).
- **No new dependencies.** `google.golang.org/grpc@v1.81.1` is already in `go.mod`, and its
  `credentials/insecure` subpackage ships inside that module (verified in the module
  cache). `context`, `time`, and `errors` are stdlib.
- Preserve the existing ISSUE-003 behavior: global `--version` prints `v0.0.0` (exit 0),
  and a bare invocation prints usage (exit 0).

## Reused Code (do not reimplement)

- `calyxv1.NewSampleServiceClient(conn)` and the `Hello(ctx, *HelloRequest) (*HelloResponse, error)`
  method — `shared/proto/mitsuhitofujita/calyx/v1/sample_grpc.pb.go:41,45`.
- Message types `HelloRequest{Name}` and accessor `HelloResponse.GetMessage()` —
  `shared/proto/mitsuhitofujita/calyx/v1/sample.pb.go:24,69,107`.
- Module import path for generated code:
  `github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1` (aliased
  `calyxv1`, same alias the backend uses in `apps/backend/main.go:13`).

## Design

Refactor `main()` into small, single-responsibility functions so the command tree is
explicit and easy to extend (this mirrors the AI-agent discoverability goal in
`intent.md`). Subcommand handlers return an `error` instead of calling `os.Exit`
directly, so deferred cleanup (closing the gRPC connection) always runs.

### Control flow

```
main()
  ├─ flag.Parse() global flags (--version)         → existing behavior, exit 0
  ├─ no positional args                            → print usage, exit 0
  └─ dispatch(args)
        └─ "sample"  → runSample(args[1:])
              └─ "hello" → runHello(args[1:])  → gRPC call
```

### Error / exit-code convention

- Runtime failures (connect / RPC) return a wrapped error; `main` prints
  `Error: <message>` to **stderr** and exits `1`.
- Argument/usage errors print the relevant `usage:` line to **stderr** themselves and
  return the sentinel `errUsage`; `main` exits `1` **without** re-printing (so usage text
  is not prefixed with `Error:`).

### Constants

```go
const (
    version            = "v0.0.0"
    defaultBackendAddr = "localhost:50051"
    dialTimeout        = 5 * time.Second
)
```

### `apps/cli/main.go` (rewrite)

```go
package main

import (
    "context"
    "errors"
    "flag"
    "fmt"
    "os"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

const (
    version            = "v0.0.0"
    defaultBackendAddr = "localhost:50051"
    dialTimeout        = 5 * time.Second
)

// errUsage signals that a handler has already written its usage message to
// stderr. main exits non-zero without printing it again.
var errUsage = errors.New("usage error")

func main() {
    versionFlag := flag.Bool("version", false, "print version information")

    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage of calyx:\n")
        flag.PrintDefaults()
        fmt.Fprintf(os.Stderr, "\nCommands:\n")
        fmt.Fprintf(os.Stderr, "  sample hello <name>   greet <name> via the backend\n")
    }

    flag.Parse()

    if *versionFlag {
        fmt.Println(version)
        os.Exit(0)
    }

    args := flag.Args()
    if len(args) == 0 {
        flag.Usage()
        os.Exit(0)
    }

    if err := dispatch(args); err != nil {
        if !errors.Is(err, errUsage) {
            fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        }
        os.Exit(1)
    }
}

func dispatch(args []string) error {
    switch args[0] {
    case "sample":
        return runSample(args[1:])
    default:
        fmt.Fprintf(os.Stderr, "calyx: unknown command %q\n", args[0])
        return errUsage
    }
}

func runSample(args []string) error {
    if len(args) == 0 {
        fmt.Fprintln(os.Stderr, "usage: calyx sample hello <name>")
        return errUsage
    }
    switch args[0] {
    case "hello":
        return runHello(args[1:])
    default:
        fmt.Fprintf(os.Stderr, "calyx sample: unknown command %q\n", args[0])
        return errUsage
    }
}

func runHello(args []string) error {
    if len(args) != 1 {
        fmt.Fprintln(os.Stderr, "usage: calyx sample hello <name>")
        return errUsage
    }
    name := args[0]

    conn, err := grpc.NewClient(backendAddr(),
        grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        return fmt.Errorf("failed to connect to backend: %w", err)
    }
    defer conn.Close()

    client := calyxv1.NewSampleServiceClient(conn)

    ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
    defer cancel()

    resp, err := client.Hello(ctx, &calyxv1.HelloRequest{Name: name})
    if err != nil {
        return fmt.Errorf("gRPC call failed: %w", err)
    }

    fmt.Println(resp.GetMessage())
    return nil
}

// backendAddr returns CALYX_BACKEND_ADDR, or defaultBackendAddr when unset/empty.
func backendAddr() string {
    if addr := os.Getenv("CALYX_BACKEND_ADDR"); addr != "" {
        return addr
    }
    return defaultBackendAddr
}
```

### Why `grpc.NewClient` (not `grpc.Dial`)

`grpc.Dial` is deprecated in the current grpc-go. `NewClient` connects lazily, so a dead
backend produces no error at construction time; the failure surfaces on the `Hello` call
and is bounded by the 5s timeout context, then returned as `gRPC call failed: ...`. This
matches the issue's "Verify Unreachable Backend Flow" expectation (clear stderr error,
non-zero exit, no indefinite hang).

## Files to Modify

| File | Action |
|---|---|
| `apps/cli/main.go` | Rewrite: add subcommand dispatch + `sample hello` gRPC client |

## Verification

End-to-end against the real backend (matches the issue's testing plan):

```bash
# Terminal A — start the backend (expect: "backend: listening on :50051")
just run

# Terminal B — build the binaries (expect: bin/calyx created)
just build

# 1. Success flow → stdout "Hello, Alice."   exit 0
./bin/calyx sample hello Alice; echo "exit=$?"

# 2. Missing argument → "usage: calyx sample hello <name>" on stderr, exit 1
./bin/calyx sample hello; echo "exit=$?"

# 3. Too many arguments → usage on stderr, exit 1
./bin/calyx sample hello Alice Bob; echo "exit=$?"

# 4. Custom backend address honored
CALYX_BACKEND_ADDR=localhost:50051 ./bin/calyx sample hello Bob; echo "exit=$?"

# 5. Regression: existing ISSUE-003 behavior preserved
./bin/calyx --version          # v0.0.0, exit 0
./bin/calyx                    # usage, exit 0
```

Unreachable-backend flow (stop `just run` first):

```bash
# Expect: "Error: gRPC call failed: ..." on stderr, exit 1, returns within ~5s
./bin/calyx sample hello Alice; echo "exit=$?"
```

Also confirm the package still builds and existing tests pass:

```bash
just build
just test
```
