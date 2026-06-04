# Plan: ISSUE-003 — Create Sample CLI "calyx"

## Context

Per `docs/intent.md`, this project builds a template GitHub repository for CLI tools that collaborate with AI agents. `apps/cli` is the CLI front-end component. ISSUE-003 is the first step: create a skeleton CLI named `calyx` that prints `v0.0.0` on `--version` / `-version`, and displays usage instructions when invoked with no arguments or `--help` / `-h`.

## Changes

### 1. `apps/cli/main.go` (new file)

Simple implementation using the standard `flag` package.

```go
package main

import (
    "flag"
    "fmt"
    "os"
)

func main() {
    versionFlag := flag.Bool("version", false, "print version information")

    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage of calyx:\n")
        flag.PrintDefaults()
    }

    flag.Parse()

    if *versionFlag {
        fmt.Println("v0.0.0")
        os.Exit(0)
    }

    if len(os.Args) == 1 {
        flag.Usage()
        os.Exit(0)
    }
}
```

### 2. `justfile` — update `build` recipe

Change the build recipe to output binaries to the `bin/` directory.

```just
# Compile all packages and output binaries.
build:
    @mkdir -p bin
    go build -o bin/calyx ./apps/cli
    go build -o bin/backend ./apps/backend
```

### 3. `.gitignore` — add `bin/`

Exclude generated binaries from version control.

```
bin/
```

## Files to Modify

| File | Action |
|---|---|
| `apps/cli/main.go` | Create |
| `justfile` | Update `build` recipe |
| `.gitignore` | Append `bin/` |

## Verification

```bash
just build                   # bin/calyx binary is created
./bin/calyx --version        # outputs: v0.0.0 (exit 0)
./bin/calyx -version         # outputs: v0.0.0 (exit 0)
./bin/calyx                  # outputs: Usage of calyx: ... (exit 0)
./bin/calyx --help           # outputs: Usage of calyx: ... (exit 0)
./bin/calyx --invalid-flag   # outputs: error + usage (exit 2)
```
