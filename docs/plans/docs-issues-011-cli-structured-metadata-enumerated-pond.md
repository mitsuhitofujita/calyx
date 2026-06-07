# PLAN: ISSUE-011 — `calyx schema` (Machine-Readable CLI Contract via a Command Registry)

## Context

**Why this change.** `docs/intent.md` ("Structured Metadata Output for Autonomous Learning")
requires a command that emits the CLI's full command hierarchy — args, flags, env vars, exit
codes — in a machine-readable form, so an AI agent can learn the CLI spec **without reading the
Go source**. This is the CLI counterpart to ISSUE-010, which made `shared/proto` the single
source of truth for the gRPC contract (no hand-written reference doc).

**The problem today.** The CLI surface is implicit: a `switch`-based `dispatch` in
`apps/cli/main.go` plus inline `flag`/`os.Getenv` usage scattered across handlers. There is no
machine-readable contract and nothing to introspect. A hand-written Markdown reference would be
dual-maintenance and would drift.

**Intended outcome.** Introduce a small **declarative command registry** that *both* the
dispatcher and a new `calyx schema` command consume. Dispatch routes off it; `schema` serializes
it to JSON. One definition, two consumers → the metadata cannot drift from what actually runs.
This supersedes any hand-maintained CLI reference doc (`schema` *is* the reference).

**Decisions already made** (confirmed with the user):
- Command name: **`calyx schema`**.
- Output: **pretty-printed JSON** (2-space indent) to stdout, exit `0`.

**Scope guardrails.** Behavior-preserving refactor only. Do **not** change any command's runtime
behavior, add new runtime commands, or document not-yet-built features (auto-update, telemetry,
timeouts, a global `--json` mode). Keep dependency-light: standard library `encoding/json` only.

---

## Current Surface (verified against `apps/cli/`)

Encode exactly this. Env defaults and exit codes are read from the live code, not guessed.

| Path | Run handler | Args | Env consumed | Exit 0 | Exit 1 |
|---|---|---|---|---|---|
| `calyx` (root) | — (group; `--version` + no-args handled in `main`) | — | — | no args → usage; `--version` → prints `v0.0.0` | unknown command |
| `schema` | `runSchema` (new) | none | — | JSON printed | extra args → usage |
| `sample` | — (group) | — | — | — | no/unknown subcommand |
| `sample hello` | `runHello` | `<name>` (required, 1) | `CALYX_BACKEND_ADDR`, `CALYX_TOKEN_STORE`, `CALYX_CONFIG_DIR` | greeting printed | usage error or RPC failure |
| `auth` | — (group) | — | — | — | no/unknown subcommand |
| `auth login` | `runAuthLogin` | none | `CALYX_GOOGLE_CLIENT_ID`*, `CALYX_GOOGLE_CLIENT_SECRET`*, `CALYX_OAUTH_REDIRECT_ADDR`, `CALYX_TOKEN_STORE`, `CALYX_CONFIG_DIR`, `CALYX_BACKEND_ADDR` | logged in, token saved | usage / missing config / OAuth / backend failure |
| `auth status` | `runAuthStatus` | none | `CALYX_TOKEN_STORE`, `CALYX_CONFIG_DIR`, `CALYX_BACKEND_ADDR` | status reported (authenticated **or** not) | usage / misconfigured store / load error / unreachable backend |

\* required, no default. Env defaults to encode:
- `CALYX_BACKEND_ADDR` → `localhost:50051` (`main.go:21` `defaultBackendAddr`)
- `CALYX_OAUTH_REDIRECT_ADDR` → `127.0.0.1:8765` (`auth.go:32` `defaultRedirectAddr`)
- `CALYX_TOKEN_STORE` → `file` (`store.go:48` `NewTokenStore`)
- `CALYX_CONFIG_DIR` → OS per-user config dir; purpose note: *base dir for the session file
  (`<config-dir>/calyx/session.json`)*. **Never emit an absolute host path** (`store.go:33`
  `sessionFilePath`).

`auth login` also reads `.env` from the working dir via `godotenv.Load()` (`auth.go:71`) — note
this in the `auth login` summary; it is not an env var to list.

---

## Output Schema (target JSON)

Flat `commands` array; each entry carries its full `path`. Slices are always `[]`, never `null`.

```json
{
  "schema_version": "1",
  "cli": { "name": "calyx", "version": "v0.0.0" },
  "commands": [
    {
      "path": [],
      "summary": "Calyx sample CLI: a thin gRPC client of the backend.",
      "group": true,
      "args": [],
      "flags": [{ "name": "version", "type": "bool", "purpose": "print version information" }],
      "env": [],
      "exit_codes": [
        { "code": 0, "when": "usage printed (no args) or --version printed" },
        { "code": 1, "when": "unknown command" }
      ],
      "examples": ["calyx --version"]
    },
    {
      "path": ["sample", "hello"],
      "summary": "greet <name> via the backend",
      "args": [{ "name": "name", "required": true, "repeated": false }],
      "flags": [],
      "env": [
        { "name": "CALYX_BACKEND_ADDR", "default": "localhost:50051", "purpose": "backend gRPC address" }
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

- `group: true` (omitempty) marks non-runnable namespaces (root, `sample`, `auth`). Runnable
  leaves omit it. This is a small, agent-friendly addition beyond the issue's illustrative shape.
- `schema_version` is a `const = "1"`. Bump only on a breaking shape change.
- `cli.version` reuses the existing `version` const (`main.go:20`).

---

## Implementation

### Step 1 — Registry types + builder (`apps/cli/registry.go`, new)

Plain Go values (no framework, no reflection). `Run` is `nil` for groups.

```go
type argSpec struct {
    Name     string `json:"name"`
    Required bool   `json:"required"`
    Repeated bool   `json:"repeated"`
}
type flagSpec struct {
    Name    string `json:"name"`
    Type    string `json:"type"`
    Default string `json:"default,omitempty"`
    Purpose string `json:"purpose"`
}
type envSpec struct {
    Name     string `json:"name"`
    Default  string `json:"default,omitempty"`
    Required bool   `json:"required,omitempty"`
    Purpose  string `json:"purpose"`
}
type exitCodeSpec struct {
    Code int    `json:"code"`
    When string `json:"when"`
}

type command struct {
    Name      string
    Summary   string
    Args      []argSpec
    Flags     []flagSpec
    Env       []envSpec
    ExitCodes []exitCodeSpec
    Examples  []string
    Run       func(args []string) error // nil ⇒ group
    Sub       []*command
}

// commandRegistry builds and returns the root command tree. Cheap to call; both
// dispatch and runSchema call it. No init-time cycle: schema's node references the
// runSchema *function value*, it does not call it.
func commandRegistry() *command { ... }
```

The builder encodes the table above, including the `schema` node:
`{Name: "schema", Summary: "print the CLI command tree and metadata as JSON",
ExitCodes: {0: "JSON printed", 1: "usage error"}, Examples: ["calyx schema"], Run: runSchema}`.

### Step 2 — Routing off the registry (`apps/cli/registry.go`)

Split routing (testable, no side effects) from execution:

```go
// resolve walks groups by name and returns the target command plus its remaining
// args, or errUsage (after writing a usage message to stderr) for an unknown or
// missing subcommand. It does NOT validate leaf arg counts — handlers still do that.
func (c *command) resolve(args []string) (*command, []string, error)
```

Rules (reproduce current behavior):
- Group (`len(Sub) > 0`) with no args → write usage, return `errUsage`
  (replaces `runSample`/`runAuth` no-arg messages with a registry-derived list).
- Group with `args[0]` matching a sub → recurse with `args[1:]`.
- Group with unmatched `args[0]` → `"<path>: unknown command %q"` to stderr, `errUsage`.
- Leaf (`Run != nil`) → return `(c, args, nil)`.

Then in `main.go`:

```go
func dispatch(args []string) error {
    cmd, rest, err := commandRegistry().resolve(args)
    if err != nil {
        return err
    }
    return cmd.Run(rest)
}
```

**Delete** `runSample` (`main.go`) and `runAuth` (`auth.go`) — routing is now generic. **Keep**
`runHello`, `runAuthLogin`, `runAuthStatus` intact, including their own arg-count validation and
`usage:`/`errUsage` messages. (Per the issue, leaf `Args` metadata is *descriptive*; the handler
remains the enforcer. The no-drift test guarantees command *presence*, not arity, so this is the
intended trade-off — call it out in a comment.)

### Step 3 — Registry-derived usage (`apps/cli/registry.go` + `main.go`)

Add `func (c *command) writeUsage(w io.Writer)` that prints global flags then walks the tree
listing **leaf** commands as `  <full path><arg suffix>   <summary>` (arg suffix from `Args`:
` <name>` for required, ` [name...]` for repeated). Point `flag.Usage` (`main.go:32`) at it so
help and metadata agree (this replaces the hardcoded command list). Reuse the same walk in
`resolve`'s group-usage output.

### Step 4 — `schema` command + serializer (`apps/cli/schema.go`, new)

```go
const schemaVersion = "1"

type cliSchema struct {
    SchemaVersion string          `json:"schema_version"`
    CLI           cliInfo         `json:"cli"`
    Commands      []commandSchema `json:"commands"`
}
type cliInfo struct{ Name, Version string } // json:"name","version"
type commandSchema struct {
    Path      []string       `json:"path"`
    Summary   string         `json:"summary"`
    Group     bool           `json:"group,omitempty"`
    Args      []argSpec      `json:"args"`
    Flags     []flagSpec     `json:"flags"`
    Env       []envSpec      `json:"env"`
    ExitCodes []exitCodeSpec `json:"exit_codes"`
    Examples  []string       `json:"examples"`
}

// buildSchema flattens the registry depth-first into cliSchema, computing each
// node's path and forcing nil slices to []. Pure: no I/O. Used by tests too.
func buildSchema(root *command) cliSchema

// schemaJSON marshals buildSchema(commandRegistry()) with 2-space indent. Pure.
func schemaJSON() ([]byte, error)

// runSchema: reject extra args (usage → errUsage); else print schemaJSON() to
// stdout + trailing newline; return nil (exit 0).
func runSchema(args []string) error
```

`runSchema` writes to `os.Stdout`; tests call the pure `schemaJSON`/`buildSchema` instead of
capturing stdout.

### Step 5 — Tests (`apps/cli/registry_test.go`, `apps/cli/schema_test.go`, new)

Match existing style (table tests, `t.Setenv`, `errors.Is(err, errUsage)`):

1. **No-drift (key test).** `TestSchema_CoversRegistry`: independently walk `commandRegistry()`
   collecting every node's path; assert `buildSchema` emits exactly that set (each path once, no
   extras, no missing). Adding a command without a descriptor — or vice versa — fails here.
2. **Routing.** `TestResolve` table: `["sample","hello","World"]`→hello + `["World"]`;
   `["auth","login"]`→login + `[]`; `["auth","status"]`→status; `["schema"]`→schema; `["bogus"]`,
   `["sample"]`, `["sample","x"]`, `["auth","x"]` → `errUsage`. (Tests routing without invoking
   network handlers.)
3. **Valid JSON / round-trip.** `TestSchemaJSON_Valid`: unmarshal `schemaJSON()` into `cliSchema`;
   assert `schema_version=="1"`, `cli.name=="calyx"`, `cli.version==version`, and the expected
   paths are present (`["sample","hello"]`, `["auth","login"]`, `["auth","status"]`, `["schema"]`,
   root `[]`).
4. **Usage.** `TestRunSchema_RejectsExtraArgs`: `runSchema([]string{"x"})` is `errUsage`.
5. **Golden snapshot** (full-shape guard). `TestSchema_Golden` compares pretty `schemaJSON()`
   against `apps/cli/testdata/schema.golden.json`, regenerated via a `-update` flag
   (`flag.Bool("update", ...)`, write file when set). Catches unintended env/exit-code/wording
   changes; the no-drift test (#1) remains the command-presence guarantee.

Existing tests (`runAuthStatus`, `sayHello`, store) must keep passing unchanged.

---

## Files

- `apps/cli/registry.go` *(new)* — spec types, `command`, `commandRegistry()`, `resolve`,
  `writeUsage`.
- `apps/cli/schema.go` *(new)* — `cliSchema` types, `buildSchema`, `schemaJSON`, `runSchema`.
- `apps/cli/main.go` *(modify)* — `dispatch` routes via `resolve`; `flag.Usage` → `writeUsage`;
  delete `runSample`; tidy imports.
- `apps/cli/auth.go` *(modify)* — delete `runAuth`; keep `runAuthLogin`/`runAuthStatus`.
- `apps/cli/registry_test.go`, `apps/cli/schema_test.go` *(new)*; `apps/cli/testdata/schema.golden.json` *(new)*.

---

## Verification

### Automated
```bash
just build      # both binaries compile
just test       # all packages pass, incl. new no-drift / routing / golden tests
```

### Manual
```bash
./bin/calyx schema | jq .                       # valid, pretty JSON
./bin/calyx schema | jq -r '.commands[].path|@tsv'   # lists every command path
./bin/calyx schema | jq '.schema_version,.cli'  # "1", {name:"calyx",version:"v0.0.0"}
./bin/calyx schema extra; echo "exit=$?"        # usage to stderr, exit 1
```

### Behavior preservation (must be unchanged)
```bash
./bin/calyx --version           # v0.0.0, exit 0
./bin/calyx                     # usage (now lists `schema` too), exit 0
./bin/calyx bogus; echo $?      # unknown command, exit 1
./bin/calyx sample hello World  # greeting (backend up) / RPC error (down)
./bin/calyx auth status         # "not logged in" with no token, exit 0
```

### No-drift guarantee
`TestSchema_CoversRegistry` fails if the registry and `schema` output disagree on the command
set — the structural promise that this command cannot drift from what dispatch runs.

---

## Out of Scope (future)
- Rendered Markdown/HTML reference (generate from `calyx schema` if ever needed — never
  hand-write).
- New commands (auto-update, telemetry, timeouts) and a uniform execution-output `--json` mode.
- Enforcing leaf arg arity from the registry (handlers stay the enforcer this pass).
