# ISSUE-003: Create Sample CLI "calyx"

## Status
- **Status**: Open / Ready for Development
- **Priority**: High (Blocking CLI-based feature integration)
- **Assignee**: AI Agent / Developer

## Objective
Create the skeleton for the command-line interface (CLI) application named `calyx` in `apps/cli`.
At this stage, the CLI should only support printing its version (`v0.0.0`) when invoked with the `--version` or `-version` flags, and displaying usage instructions when run without arguments or with helper flags.

## Background
According to "intent.md" and "spec.md", Calyx requires a client-side CLI tool (`apps/cli`) that users and AI agents will operate.
We need to set up the basic structure of this Go CLI so that it compiles and supports basic version/usage options.

## Technical Specifications

### CLI Details
- **Command Name**: `calyx`
- **Implementation Language**: Go (Golang)
- **Command Entry Point**: `apps/cli/main.go`
- **Output Binary Path**: `bin/calyx` (via compilation)

### Expected Behavior & Interface

#### 1. Version Output
When executed with `--version` or `-version`, the application must print its version string and exit.
- **Output**: `v0.0.0` (with a trailing newline)
- **Exit Code**: `0`
- **Output Stream**: `stdout`

#### 2. Usage Information
When executed with no arguments (i.e., `calyx` alone) or with `--help` / `-h`, it must print clear instructions on how to use the CLI.
- **Output Stream**: `stderr` (or `stdout` if standard `flag` package defaults are used)
- **Exit Code**: `0` (for direct invocations with no arguments or help flags)

Example output style:
```
Usage of calyx:
  -version
        print version information
```

#### 3. Invalid Flags
When executed with unknown or unsupported flags, the application must print an error message, display the usage guide, and terminate.
- **Exit Code**: Non-zero (e.g., `2` via Go's standard `flag` library)

## Directory and File Mapping
The implementation involves editing or creating files in the following paths:
- `apps/cli/main.go` (Create)
- `justfile` (Modify to add compile step for the CLI and update build recipe)

## Implementation Steps

### Step 1: Create CLI Scaffolding
Create `apps/cli/main.go` with a package name of `main`.
Use the standard Go library `flag` package to parse options.

```go
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	// Define the version flag.
	versionFlag := flag.Bool("version", false, "print version information")

	// Customize usage if necessary.
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of calyx:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	// Handle version command.
	if *versionFlag {
		fmt.Println("v0.0.0")
		os.Exit(0)
	}

	// If no arguments or flags are provided, show usage and exit.
	if len(os.Args) == 1 {
		flag.Usage()
		os.Exit(0)
	}
}
```

### Step 2: Update the `justfile`
Modify the `justfile` to build the new `calyx` binary under the `bin/` directory.

Update the `build` recipe:
```just
# Compile all packages and output binaries.
build:
    @mkdir -p bin
    go build -o bin/calyx ./apps/cli
    go build -o bin/backend ./apps/backend
```
Make sure `just build` correctly executes and saves the built binary into a local `bin` directory (which should be git-ignored if a `.gitignore` exists).

## Verification and Testing Plan

To verify this issue is successfully implemented, run the following verification steps:

### 1. Build Verification
Run the build task using `just`:
```bash
just build
```
Verify that the `bin/calyx` binary is successfully created.

### 2. Version Flag Check
Run the CLI with the version flag:
```bash
./bin/calyx --version
```
**Expected Output**:
```
v0.0.0
```

Run with `-version`:
```bash
./bin/calyx -version
```
**Expected Output**:
```
v0.0.0
```

### 3. Usage Output Check
Run the CLI without any arguments:
```bash
./bin/calyx
```
**Expected Output**:
```
Usage of calyx:
  -version
        print version information
```
(Exit code should be `0`)

Run with helper flag:
```bash
./bin/calyx --help
```
**Expected Output**:
```
Usage of calyx:
  -version
        print version information
```

### 4. Invalid Options Check
Run with an invalid flag:
```bash
./bin/calyx --invalid-flag
```
**Expected Output**:
An error message stating that the flag is undefined, followed by the usage information. The exit code must be non-zero (standard behavior of `flag.Parse`).
