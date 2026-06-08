# ISSUE-014: Strengthen Standalone Unit / Component Tests (Mocks, Table-Driven, Boundary)

## Status
- **Status**: Open / In Development
- **Priority**: Medium-High (the wide base of the ISSUE-012 pyramid; fast feedback that guards
  logic and edge cases the E2E layer is too coarse to pin down)
- **Assignee**: AI Agent / Developer
- **Depends on**: ISSUE-012 (test strategy & conventions). Independent of ISSUE-013 (they share
  only the conventions, not code).
- **Related**: ISSUE-015 (runs this suite in CI as part of the gate).

## Objective
Fill the **standalone unit/component** layer for both `apps/cli` and `apps/backend`: fast,
isolated, **table-driven** tests with **mocked boundaries**, focused on **logic branches and
boundary conditions**. These run in-process (no real socket, no real backend), complete the
fast `go test ./...` loop, and back the release gate alongside the E2E layer.

This is the "create standalone tests for CLI and gRPC, using mocks / boundary / table tests"
requirement from the development plan, scoped to the unit layer.

## Background
The codebase already has solid examples to extend (not rewrite — ISSUE-012):
- **Backend**: `apps/backend/internal/server/auth_test.go` uses a `bufconn` server, a stub
  Google verifier (`stubVerifier`), an injected clock (`AuthServer.now`), and fixed test
  fixtures (`testSigningKey`, `testNow`, `testUser`, `testAuthConfig`).
  `apps/backend/internal/server/sample_test.go` covers the `Hello` RPC.
- **CLI**: `apps/cli/main_test.go` (a recording `bufconn` `SampleService` stub asserting the
  outgoing `authorization: Bearer` header), `apps/cli/store_test.go`, `apps/cli/auth_test.go`,
  `apps/cli/registry_test.go`, and `apps/cli/schema_test.go` (golden file under
  `apps/cli/testdata/`).

The goal is to **systematically close gaps** in branch and boundary coverage using these same
patterns, so regressions in pure logic are caught in milliseconds rather than by the slower
E2E layer.

## Design Decisions

### 1. Table-driven everywhere there is more than one case
Each function with multiple input/output or error branches gets a `cases := []struct{ ... }`
table and a `t.Run(tc.name, ...)` loop. This is the default shape; single-path helpers may stay
simple.

### 2. Mock at the boundary, never reach the network
- **Backend**: reuse the `googleIDTokenVerifier` seam (`stubVerifier`) so `Login` tests never
  hit Google; reuse `bufconn` for in-process RPC; inject `now` for time-dependent claims/expiry.
- **CLI**: reuse the `bufconn` stub-server pattern (`newTestSampleClient` and an analogous
  `AuthService` stub) to exercise client logic — metadata attachment, response rendering, error
  mapping — without a real backend.

### 3. Boundary and error cases are first-class
Explicitly cover empty/whitespace/oversized inputs, missing/empty metadata, expired vs. valid
`exp`, wrong signing method / wrong `iss`/`aud`, corrupt/missing token files, and unknown
config selector values. These are exactly the cases the coarse E2E layer should *not* be
burdened with.

### 4. Determinism & isolation (per ISSUE-012)
`t.TempDir()` + `CALYX_CONFIG_DIR` for any filesystem touchpoint; `t.Setenv` for config;
injected clocks for time; no `Date.now`-style reliance on wall clock in assertions; golden
files for serialized output with an `-update` flag.

### 5. Add, don't rewrite
Extend existing `*_test.go` files or add new sibling files. Do not restructure or weaken
existing assertions (ISSUE-012 constraint). Shared helpers may be refactored *additively*.

## Scope

### In Scope — coverage gaps to close (extend existing files / add siblings)
**Backend (`apps/backend/internal/server`)**
- `AuthService.Login` (table-driven): valid Google token → well-formed session JWT with correct
  `iss`/`aud`/`exp`/`name`/`email`/`role=admin`/`permissions=["*"]`; verifier error → mapped
  gRPC status; unimplemented `auth_code` branch → `Unimplemented`.
- `AuthService.Status` / `verifySessionToken` (table-driven): valid; missing metadata;
  malformed token; wrong signature; wrong `iss`/`aud`; expired (advance injected clock). Assert
  the response-body semantics (`authenticated`, `message`, populated `session`) — not a gRPC
  error for the not-authenticated cases.
- `SampleService.Hello`: name echo, plus boundary inputs (empty, unicode, long).

**CLI (`apps/cli`)**
- Argument parsing / dispatch: missing required `<name>`, unknown subcommands, extra args →
  the `errUsage` path and exit semantics (component-level, in-process).
- Token store (`store.go`): round-trip save/load; `ErrNoToken` on missing file; corrupt-file
  error; `0700`/`0600` permissions; `CALYX_TOKEN_STORE` selector matrix (``/`file` ok,
  `keyring` → reserved error, other → fail-fast error); path resolution via `CALYX_CONFIG_DIR`.
- Outgoing metadata: `authorization: Bearer <jwt>` attached when a token exists; absent when not
  (extend the existing `main_test.go` patterns).
- `auth status` renderer: authenticated → fields rendered; not-authenticated → message + hint;
  RPC error mapping. Use a `bufconn` `AuthService` stub returning each `StatusResponse` shape.
- `schema` output: keep the golden-file assertion authoritative; ensure new/changed commands
  update the golden via the `-update` flow (registry/schema drift check already exists).

### Out of Scope (Future Issues)
- E2E (real binaries / real socket / lifecycle) → **ISSUE-013**.
- CI wiring, coverage gates, SBOM/supply-chain checks → **ISSUE-015**.
- New product features and their first-line tests.
- Rewriting existing tests (ISSUE-012 constraint).

## Technical Specifications

### Patterns to reuse (do not reinvent)
- Backend `bufconn` harness + stub verifier + injected clock: `auth_test.go`
  (`newTestAuthClient`, `stubVerifier`, `testNow`, `testAuthConfig`).
- CLI `bufconn` recording stub: `main_test.go` (`recordingSampleServer`,
  `newTestSampleClient`); mirror it for an `AuthService` stub to test `auth status`.
- Golden-file pattern: `schema_test.go` + `apps/cli/testdata/schema.golden.json`.
- Env/fs isolation: `t.Setenv("CALYX_CONFIG_DIR", t.TempDir())` (see `main_test.go`).

### Table-driven skeleton (illustrative)
```go
func TestVerifySessionToken(t *testing.T) {
    cases := []struct {
        name      string
        mutate    func(*sessionClaims) // or a raw-token builder
        wantValid bool
    }{
        {"valid", nil, true},
        {"expired", func(c *sessionClaims) { /* exp in the past */ }, false},
        {"wrong audience", func(c *sessionClaims) { /* aud mismatch */ }, false},
        // ...
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) { /* build, verify, assert */ })
    }
}
```

## Directory and File Mapping
- `apps/backend/internal/server/auth_test.go`, `sample_test.go` (Modify/Extend): add the
  table-driven `Login`/`Status`/`Hello` cases above.
- `apps/cli/store_test.go`, `auth_test.go`, `main_test.go`, `registry_test.go`,
  `schema_test.go` (Modify/Extend) and/or new sibling `*_test.go` files: the CLI cases above;
  add an `AuthService` `bufconn` stub for the `auth status` renderer.
- `apps/cli/testdata/` (Modify only via the `-update` flow if the golden legitimately changes).
- No production code change is required for this issue (test-only), except possibly extracting a
  pure helper to make a branch testable — kept behavior-preserving.

## Implementation Steps
1. **Inventory gaps**: list branches/boundaries in `auth.go`, `sample.go`, `store.go`,
   `registry.go`, the `auth`/`hello`/`schema` handlers not yet asserted.
2. **Backend tables**: extend `auth_test.go` (`Login`, `Status`, `verifySessionToken`) and
   `sample_test.go` boundaries, reusing the existing harness and fixtures.
3. **CLI tables**: extend store/dispatch/metadata/renderer tests; add the `AuthService`
   `bufconn` stub mirroring `recordingSampleServer`.
4. **Run & iterate** with `-race`; keep everything in the fast `go test ./...` loop.

## Verification and Testing Plan
### 1. Run the fast suite
```bash
just test          # go test ./...  — all new cases pass
go test -race ./... # no data races in the bufconn/goroutine harnesses
```
### 2. Coverage spot-check (informational, no hard gate here)
```bash
go test -cover ./apps/...
```
Confirm the targeted packages (`apps/backend/internal/server`, `apps/cli`) gain coverage on the
previously-unasserted branches.
### 3. Golden integrity
```bash
go test ./apps/cli/...   # schema golden matches; -update only when intentional
```

## Notes
- Keep tests **fast and hermetic**: no socket, no Google, no sleep-based waits — inject clocks
  and use `bufconn`.
- Failure messages should name the case (`t.Run` name) and print `got`/`want`, matching the
  existing tests' style.
- When real `role`/`permission` resolution replaces today's placeholders, update the asserted
  expectations here in lockstep.
