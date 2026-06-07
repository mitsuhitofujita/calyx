# ISSUE-011: Implement the Structured Metadata Output Command (Machine-Readable CLI Contract)

## Status
- **Status**: Open / Ready for Development
- **Priority**: Medium (gives AI agents a machine-readable, drift-free description of the CLI
  surface — the CLI counterpart to "read `shared/proto`" for gRPC)
- **Assignee**: AI Agent / Developer
- **Depends on**: ISSUE-003 (`calyx` skeleton + dispatch), ISSUE-004 (`sample hello`),
  ISSUE-005 (`auth login`), ISSUE-007 (token storage / env selectors), ISSUE-009 (`auth status`)
  — the commands to describe must exist.
- **Supersedes**: the earlier plan to author a hand-maintained `docs/cli-reference.md`.
- **Related**: ISSUE-010 (the gRPC equivalent: `shared/proto` is the single source of truth, no
  separate doc).

## Objective
Implement the command from `intent.md` ("Structured Metadata Output for Autonomous Learning"):
a `calyx` command that emits the **full command hierarchy and each command's expected arguments,
flags, environment variables, and exit codes** in a **machine-readable** form, so an AI agent
can learn the CLI's specification in advance **without reading the Go source**.

Crucially, this output must be **derived from the same in-code command definitions that drive
dispatch**, so it is a single source of truth that cannot drift — replacing the idea of a
hand-written, separately-maintained CLI reference document.

## Background
`intent.md` calls for a command that outputs the command hierarchy and expected arguments in
help format so agents can understand the tool's spec ahead of time. Unlike the gRPC side — where
the `.proto` files already are a machine-readable contract (see ISSUE-010) — the CLI currently
has **no** machine-readable contract: its surface is implicit in the standard-library `flag`
handlers and the `switch`-based `dispatch` in `apps/cli/`. A hand-written Markdown reference
would be dual-maintenance and would drift. The correct, drift-free solution is to generate the
spec from the code itself.

## Design Decisions
1. **Single source of truth via a declarative command registry.** Refactor the command surface
   into a small data structure (a registry/tree of command descriptors) that **both** the
   dispatcher and this metadata command consume. The metadata is then *derived* from the same
   definitions that execute commands, so it cannot drift. (Today's ad-hoc `switch` dispatch and
   inline `flag` usage cannot be introspected reliably; a lightweight registry is the enabling
   refactor.)
2. **Machine-readable output (JSON) by default.** The consumer is an AI agent, so the command
   emits stable, well-formed JSON to stdout. A stable top-level schema (with a `schema_version`)
   lets agents parse it positionally and lets tests assert it.
3. **Describe the whole tree, recursively.** Output the root command and every group/subcommand
   with: path, summary, positional arguments (name, required, repeatable), flags, consumed
   environment variables (with defaults), exit codes, and one or more examples.
4. **No hand-maintained CLI reference doc.** This command *is* the reference. If a rendered
   document is ever wanted, generate it from this command's JSON output — never hand-write it.
5. **Keep it dependency-light.** Prefer the standard library (`encoding/json`) and the existing
   structure; do not pull in a CLI framework solely for this. The registry can stay a plain Go
   value.

> **Open decision — command name.** Recommended: a top-level **`calyx schema`** that prints the
> JSON to stdout. Alternatives considered: `calyx meta`, `calyx introspect`, or
> `calyx help --json`. Pick one during implementation and keep it stable thereafter.

## Scope

### In Scope
- A command (recommended `calyx schema`) that prints the full CLI metadata as JSON to stdout and
  exits `0`.
- A declarative command registry describing the **current** surface, consumed by both dispatch
  and the metadata command:
  - root `calyx` (global: `--version`; no-args usage),
  - `calyx sample hello <name>`,
  - `calyx auth login`,
  - `calyx auth status`.
- A documented, versioned JSON schema for the output (see *Technical Specifications*).
- Refactoring `dispatch` (and the per-group routers) to drive off the registry so command names,
  argument arity, and routing have one definition.
- Tests asserting the emitted metadata matches the registered commands (golden/round-trip).

### Out of Scope (Future Issues)
- Documenting or implementing not-yet-built commands (auto-update, telemetry, timeout flags, a
  general `--json` output mode for *all* commands). The registry should make adding them cheap.
- A rendered (Markdown/HTML) reference generated from this JSON (optional future work).
- Changing any command's runtime behavior; this issue adds introspection and a (behavior-
  preserving) dispatch refactor only.

## Technical Specifications

### Command
- **Invocation** (recommended): `calyx schema` — no arguments; extra args → usage to stderr,
  exit `1` (matching the existing handlers' `errUsage` convention).
- **Output**: pretty-printed (or compact) JSON to stdout; exit `0`.

### Output Schema (illustrative; finalize during implementation)
```json
{
  "schema_version": "1",
  "cli": { "name": "calyx", "version": "v0.0.0" },
  "commands": [
    {
      "path": ["sample", "hello"],
      "summary": "greet <name> via the backend",
      "args": [{ "name": "name", "required": true, "repeated": false }],
      "flags": [],
      "env": [
        { "name": "CALYX_BACKEND_ADDR", "default": "localhost:50051",
          "purpose": "backend gRPC address" }
      ],
      "exit_codes": [
        { "code": 0, "when": "greeting printed" },
        { "code": 1, "when": "usage error or RPC failure" }
      ],
      "examples": ["calyx sample hello World"]
    }
  ]
}
```
The root entry carries global flags (`--version`) and the no-args behavior. `auth login` /
`auth status` carry their env vars (`CALYX_GOOGLE_CLIENT_ID`, `CALYX_GOOGLE_CLIENT_SECRET`,
`CALYX_OAUTH_REDIRECT_ADDR`, `CALYX_TOKEN_STORE`, `CALYX_CONFIG_DIR`, `CALYX_BACKEND_ADDR`) and
exit codes per the current handlers.

### Registry Shape (illustrative)
A plain Go value, e.g. a `command` struct with `Name`, `Summary`, `Args`, `Flags`, `Env`,
`ExitCodes`, `Examples`, `Run func([]string) error`, and `Sub []command`. `dispatch` walks this
tree to route; `schema` walks the same tree to serialize. One definition, two consumers.

### Current Surface to Encode (verify against `apps/cli/` before writing)
Root `calyx`: `--version` → prints `v0.0.0`; no args → usage (exit 0).
`sample hello <name>`; `auth login`; `auth status`. Env vars and exit codes as documented in the
handlers and `store.go`. Describe the session file location relative to `CALYX_CONFIG_DIR` /
the OS config dir (`<config-dir>/calyx/session.json`) — never an absolute host path.

## Directory and File Mapping
- `apps/cli/` (Modify/Add): introduce the command registry; refactor `dispatch` /
  `runSample` / `runAuth` to route from it; add the `schema` command and its JSON serializer.
- `apps/cli/main.go` (Modify): register the root, global flags, and the `schema` command;
  ensure `flag.Usage` is derived from (or consistent with) the registry.
- Tests (Add): assert the emitted JSON matches the registry and that dispatch still routes every
  command correctly after the refactor.

## Implementation Steps
1. **Define the registry** (`command` descriptor + tree) capturing the current surface.
2. **Refactor dispatch** to walk the registry (behavior-preserving); keep handlers intact.
3. **Add the `schema` command** that serializes the registry to JSON (with `schema_version`).
4. **Derive usage text** from the registry where practical, so help and metadata agree.
5. **Tests**: golden JSON for `schema`; routing tests confirming each command still dispatches;
   a check that every dispatchable command appears in the metadata (no drift).

## Verification and Testing Plan
### 1. Build & Test
```bash
just build
just test
```
### 2. Manual
```bash
./bin/calyx schema
```
Confirm valid JSON listing every command with args/flags/env/exit-codes; pipe through `jq` to
validate structure (e.g. `./bin/calyx schema | jq '.commands[].path'`). Confirm existing
commands (`--version`, `sample hello`, `auth login`, `auth status`) still behave unchanged.

### 3. No-Drift Check
A test enumerates the registry and asserts the `schema` output contains exactly those commands —
adding a command without updating its descriptor (or vice-versa) fails the test.

## Future Work
- Generate a rendered reference (Markdown/HTML) from `calyx schema` output if an external
  audience ever needs one — never hand-maintain it.
- Extend the registry as new commands land (auto-update, telemetry, timeouts), and consider a
  uniform `--json` execution-output mode as a separate concern.
