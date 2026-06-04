# ISSUE-002: Migrate Task Runner from make to just

## Status
- **Status**: Open / Ready for Development
- **Priority**: Medium
- **Assignee**: AI Agent / Developer

## Objective
Migrate the project's task runner from GNU Make (`Makefile`) to `just` (`justfile`). This improvement will simplify script maintenance and leverage `just` features such as built-in help listing, clean syntax, and clean parameter handling.

## Background
According to [.local/state.md](file:///home/mitsuhito/repos/github/calyx/.local/state.md), the project is transitioning its task execution framework from `make` to `just`. The `just` binary is already installed inside the development container environment, so the developer only needs to write the `justfile`, test its tasks, and delete the obsolete `Makefile`.

## Technical Specifications

### Target Tool: `just`
- Configuration File: `justfile` (placed in the repository root directory).
- Default Recipe: Executing `just` without arguments should list all available commands (similar to `just --list` or by defining a `default` recipe).

### Recipe Mapping
All targets in the existing `Makefile` must be accurately ported to `justfile`:

| Makefile Target | just Recipe | Description / Behavior |
| --- | --- | --- |
| `tools` | `tools` | Install proto/gRPC tooling via `go install` with pinned versions. |
| `lint` | `lint` | Run `buf lint` for protobuf linting. |
| `generate` | `generate` | Run `lint`, then `buf generate`, followed by `go mod tidy`. |
| `build` | `build` | Compile all Go packages (`go build ./...`). |
| `test` | `test` | Run the Go unit tests (`go test ./...`). |
| `run` | `run` | Start the backend gRPC server (`go run ./apps/backend`). |
| `verify` | `verify` | Smoke-test the running backend. Accept an optional address parameter `backend_addr` defaulting to `localhost:50051`. |
| `tidy` | `tidy` | Tidy `go.mod` and `go.sum` (`go mod tidy`). |

## Directory and File Mapping
The migration involves files in the repository root:
- Create: `justfile`
- Delete: `Makefile`

## Implementation Steps

### Step 1: Create the `justfile`
Create a `justfile` in the repository root directory. Define the variables and recipes to replicate the `Makefile` behavior.

#### Variable Definitions:
```just
# Pinned tool versions
protoc_gen_go_version      := "v1.36.11"
protoc_gen_go_grpc_version := "v1.6.2"
buf_version                := "v1.70.0"
grpcurl_version            := "v1.9.3"

backend_addr               := "localhost:50051"
```

#### Default Recipe:
Show the list of recipes by default.
```just
default:
    @just --list
```

#### Recipes:
Translate all `Makefile` targets. Ensure correct syntax for referencing variables (`{{variable_name}}`) and calling dependencies (e.g., `generate` depending on `lint`).
Note that `just` allows recipe arguments. The `verify` recipe can accept an optional argument:
```just
verify addr=backend_addr:
    grpcurl -plaintext -d '{"name":"World"}' {{addr}} mitsuhitofujita.calyx.v1.SampleService/Hello
```

### Step 2: Verify `just` Recipes
Validate that all recipes work correctly.
1. Run `just` to list available recipes.
2. Run `just lint` to verify it runs without issues.
3. Test running `just build` and `just test`.
4. Spin up the backend server in one shell using `just run`, and in another shell, test `just verify` to ensure communication works.

### Step 3: Remove `Makefile`
Once all recipes in `justfile` are successfully validated, remove the obsolete `Makefile`.

### Step 4: Update State File
Once completed, update the `.local/state.md` to reflect that the task runner migration has been completed.

## Verification and Testing Plan

To confirm this issue is successfully resolved, verify the following:
1. Running `just` in the project root lists the available recipes:
   - `tools`
   - `lint`
   - `generate`
   - `build`
   - `test`
   - `run`
   - `verify`
   - `tidy`
2. `just generate` correctly invokes `just lint` (or runs `buf lint` before generation) and completes successfully.
3. `just verify` works and correctly hits the backend.
4. `Makefile` is no longer present in the directory.
