# ISSUE-013: End-to-End Test Harness — Managed Backend Lifecycle + Deterministic Test Auth

## Status
- **Status**: Open / In Development
- **Priority**: High (the largest, highest-confidence layer of the release gate; proves the CLI
  and backend actually work together over a real socket)
- **Assignee**: AI Agent / Developer
- **Depends on**: ISSUE-012 (test strategy & conventions), and the features under test that
  already exist — ISSUE-006 (`AuthService.Login`), ISSUE-007 (CLI token store + Bearer
  attachment), ISSUE-008 (`AuthService.Status`), ISSUE-009 (`calyx auth status`),
  ISSUE-004 (`calyx sample hello`).
- **Related**: ISSUE-014 (unit layer), ISSUE-015 (runs this suite in CI).

## Objective
Build an **end-to-end (E2E) test layer** that:
1. **Starts a real backend process** before the E2E tests and **stops it** afterward (per
   suite, automatically), and
2. drives the **real `calyx` CLI binary** against it over a real TCP socket, asserting on the
   CLI's stdout/stderr/exit code, and
3. authenticates **deterministically, with no interactive Google login**, so authorized
   journeys (`auth status`, `sample hello`, an unauthorized/expired path) can run unattended in
   the dev container and in CI.

This is the concrete implementation of the E2E row of the ISSUE-012 pyramid and the
"start/stop the server around the tests" + "test without a real login" requirements.

## Background
The fast suite (`go test ./...`) exercises handlers in-process via `bufconn`
(`apps/cli/main_test.go`, `apps/backend/internal/server/auth_test.go`). That is fast and
valuable but never builds the binaries, never opens a real socket, and never runs the CLI's
`main` argument parsing / process exit codes. E2E closes that gap: it is the test that most
resembles what an AI agent actually executes.

The blocker for automated E2E is auth. Per `docs/spec.md`, a session JWT is normally obtained by
a **browser-based Google sign-in** — impossible to run per test. We need a deterministic seam so
the *rest* of the flow (token persistence, `authorization: Bearer <jwt>` metadata, backend
stateless verification, `auth status` rendering) runs exactly as in production.

### Relevant facts (verified against the code)
- The backend boots from env (`apps/backend/main.go`): it **requires**
  `CALYX_GOOGLE_CLIENT_ID` and `CALYX_JWT_SIGNING_KEY`, and listens on `CALYX_BACKEND_ADDR`
  (default `:50051`). `CALYX_JWT_ISSUER` (default `calyx-backend`), `CALYX_JWT_AUDIENCE`
  (default `calyx-cli`), and `CALYX_SESSION_TTL` (default `1h`) are optional.
- The session JWT is **HS256**, signed with `CALYX_JWT_SIGNING_KEY`; the backend verifies the
  same token statelessly (`AuthServer.verifySessionToken`), checking signature, `iss`, `aud`,
  and `exp`. Claims include `name`, `email`, `role` (fixed `admin`), `permissions` (fixed
  `["*"]`).
- The CLI persists the session token at `<base>/calyx/session.json`, where `<base>` is
  `CALYX_CONFIG_DIR` when set (`apps/cli/store.go`). The CLI dials `CALYX_BACKEND_ADDR` and
  attaches `authorization: Bearer <jwt>` to outgoing RPCs.
- `AuthService.Login` verifies a **Google ID token** against Google's network endpoints — so it
  must **not** be exercised with a real token in tests.

## Design Decisions

### 1. Real processes, real socket, OS-assigned port
The E2E suite compiles the binaries (or reuses `just build` artifacts), starts `bin/backend`
bound to an **OS-assigned free port** (`CALYX_BACKEND_ADDR=127.0.0.1:0` semantics; if `:0`
binding through env is awkward, pick a free port in the harness and pass it), and points the
CLI at it via `CALYX_BACKEND_ADDR`. **No hardcoded `50051`** — parallel/CI runs must not
collide.

### 2. Suite-scoped lifecycle via `TestMain`
Start the backend once per E2E package in `TestMain`, **wait until it is ready** (poll the port
/ a lightweight RPC, e.g. `grpcurl ... list` or a `Status` call, with a bounded timeout), run
the tests, then **guarantee teardown** (kill the process, `Wait` for it) even on failure. Each
*test* gets an isolated `CALYX_CONFIG_DIR` (`t.TempDir()`), so token state never leaks between
tests.

### 3. Deterministic test auth — recommended seam **(A) pre-minted session JWT**
> **Recommended default.** The harness mints a session JWT itself, signed with the **same**
> `CALYX_JWT_SIGNING_KEY` the backend was started with, using `iss`/`aud`/`exp` that match the
> backend config, and writes it to `<CALYX_CONFIG_DIR>/calyx/session.json`. The CLI then attaches
> it like any real token and the backend verifies it normally. This needs **zero production code
> change** and keeps the seam entirely inside the test harness.
>
> Trade-off: it does **not** exercise `AuthService.Login`. That is acceptable because `Login`'s
> only untestable part is the Google network verification, which is already covered at the unit
> layer with a stubbed verifier (`apps/backend/internal/server/auth_test.go`).

> **Open decision — also cover `Login` end to end? Alternative seam (B).** If E2E coverage of the
> `Login` exchange is wanted, add a **gated test verifier** to the backend: a Google-token
> verifier that is the real `idtoken` implementation by default but, when an explicit
> test/CI-only switch is set (e.g. an env flag that is unset in all normal runs), accepts a
> **sentinel** id-token and returns a fixed `googleUser`. The harness then calls the real
> `Login` RPC with the sentinel and receives a real session JWT through the real code path.
> Decide during implementation; if chosen, the switch MUST be off by default and impossible to
> enable in production config. Seams (A) and (B) are not mutually exclusive — (A) for most
> journeys, (B) only for the explicit `Login` journey.

### 4. Drive the CLI as a black box
Invoke `bin/calyx <args>` via `os/exec`, capture stdout/stderr, and assert on **exit code +
output text**. Assert on the user-visible strings the handlers already emit (e.g. the
`auth status` renderer and the backend messages `"session is valid"`,
`"no session token provided"`, `"session token is invalid or expired"`). This validates the
real argument parser, dispatch, and process exit semantics — none of which the `bufconn` tests
touch.

### 5. Tagged and opt-in for the fast loop, on-by-default in CI
Guard the E2E package with a build tag (e.g. `//go:build e2e`) so `go test ./...` stays fast.
Add a `just` recipe (e.g. `just test-e2e` → `go test -tags e2e ./test/e2e/...`) and a combined
`just test-all`. CI (ISSUE-015) runs the tagged suite as part of the release gate.

### 6. Determinism
Fixed signing key and claims in the harness; bounded readiness and RPC timeouts; no Google
network; no reliance on the developer's real `.env` (the harness sets every required env var
explicitly so the backend boots in isolation).

## Scope

### In Scope
- An E2E test package (recommended `test/e2e/`) guarded by a build tag.
- A reusable harness that: builds/locates the binaries, starts the backend on a free port with
  an isolated, fully-specified env, waits for readiness, and tears it down in `TestMain`.
- The deterministic test-auth seam — **(A)** as the default; **(B)** only if the `Login`
  journey is in scope (decision recorded in the code/PR).
- E2E journeys (minimum set):
  1. **Authenticated `auth status`** → exit 0; output shows name/email/role(`admin`)/
     permissions(`*`)/expiry.
  2. **No token `auth status`** (empty `CALYX_CONFIG_DIR`) → the not-authenticated / "not logged
     in" message; documented exit code.
  3. **Invalid/expired token `auth status`** → the "invalid or expired" path; documented exit
     code.
  4. **`sample hello <name>`** with a valid token → prints `Hello, <name>.`; backend receives
     the Bearer token.
- `just` recipes to run the E2E suite alone and together with the fast suite.

### Out of Scope (Future Issues)
- The GitHub Actions workflow that runs this suite (→ ISSUE-015).
- Unit/component coverage (→ ISSUE-014).
- New product features (timeouts, auto-update, telemetry) and their tests.
- Testing the real browser OAuth consent flow or real Google verification (intentionally never
  automated).
- Rewriting any existing `*_test.go` (ISSUE-012 constraint).

## Technical Specifications

### Backend startup env (set explicitly by the harness)
| Var | Value in harness | Why |
| --- | --- | --- |
| `CALYX_BACKEND_ADDR` | `127.0.0.1:<free port>` | isolate; avoid `50051` collisions |
| `CALYX_GOOGLE_CLIENT_ID` | a fixed test value | required to boot; only the audience for real Google tokens, unused by seam (A) |
| `CALYX_JWT_SIGNING_KEY` | a fixed test secret | the harness signs the session JWT with the same key |
| `CALYX_JWT_ISSUER` | `calyx-backend` (or explicit) | must match the minted token's `iss` |
| `CALYX_JWT_AUDIENCE` | `calyx-cli` (or explicit) | must match the minted token's `aud` |
| `CALYX_SESSION_TTL` | e.g. `1h` | so minted/issued tokens are unexpired during the run |

### CLI invocation env (per test)
| Var | Value | Why |
| --- | --- | --- |
| `CALYX_BACKEND_ADDR` | the backend's chosen `host:port` | point CLI at the test backend |
| `CALYX_CONFIG_DIR` | `t.TempDir()` | isolated token store per test |
| `CALYX_TOKEN_STORE` | unset/`file` | default file backend |

### Seam (A): minting the session JWT in the harness
Sign an HS256 JWT with the shared `CALYX_JWT_SIGNING_KEY` and the registered claims
`iss=CALYX_JWT_ISSUER`, `aud=CALYX_JWT_AUDIENCE`, `exp=now+TTL`, plus
`name`/`email`/`role=admin`/`permissions=["*"]`, then write
`{ "session_token": "<jwt>", "expires_at": "<RFC3339>" }` to
`<CALYX_CONFIG_DIR>/calyx/session.json` (dir `0700`, file `0600`). For the expired-token case,
mint with a past `exp`. (This mirrors what the backend's `Login` produces, so `Status`
verification accepts it.)

### Readiness & teardown
- **Readiness**: after `Start()`, poll the port (TCP dial) and/or call `AuthService.Status`
  until success or a bounded deadline (e.g. a few seconds); fail the suite if not ready.
- **Teardown**: send SIGTERM (then SIGKILL on a grace timeout), `Wait()` for the process, and
  surface captured backend logs on failure to aid debugging.

### Suggested layout
```
test/e2e/
  main_test.go      // TestMain: build/locate binaries, start backend, wait, run, teardown
  harness.go        // start/stop, free-port, env assembly, mint-token helper, run-cli helper
  auth_status_test.go
  sample_hello_test.go
```

## Directory and File Mapping
- `test/e2e/` (Add): the tagged E2E package, harness, and journey tests.
- `justfile` (Modify): add `test-e2e` (runs `go test -tags e2e ./test/e2e/...`) and a
  `test-all` that runs the fast suite + E2E; keep the existing `test` recipe unchanged.
- `apps/backend/internal/server/auth.go` (Modify — **only if** alternative seam (B) is chosen):
  introduce a gated test verifier seam that is off by default. Not needed for the recommended
  seam (A).
- No existing `*_test.go` is modified (ISSUE-012 constraint).

## Implementation Steps
1. **Harness skeleton**: free-port picker, env assembler, `startBackend`/`stop`, readiness
   poll, `runCLI(args...) (stdout, stderr string, exitCode int)`.
2. **`TestMain`**: build (or locate) `bin/backend` + `bin/calyx`, start the backend once, wait
   for readiness, run, guarantee teardown.
3. **Auth seam (A)**: `seedSession(t, dir, opts)` that mints the HS256 JWT and writes
   `session.json` under the test's `CALYX_CONFIG_DIR`. (Optionally implement seam (B) if the
   `Login` journey is in scope.)
4. **Journeys**: implement the four minimum tests above, asserting exit code + output.
5. **`just` recipes**: `test-e2e`, `test-all`; verify both run green in the dev container.
6. **Docs**: a short note in `README.md` (or the strategy issue) on how to run E2E locally.

## Verification and Testing Plan
### 1. Build
```bash
just build      # produces bin/calyx and bin/backend the harness drives
```
### 2. Run the E2E suite in the dev container
```bash
just test-e2e   # go test -tags e2e ./test/e2e/...
```
Expect: backend starts on a free port, the four journeys pass, and the backend process is gone
after the run (no orphan). Run twice back-to-back to confirm no port/state leakage.
### 3. Fast loop unaffected
```bash
just test       # still go test ./...; must NOT compile/run the e2e-tagged package
```
### 4. Combined gate (what CI will run — ISSUE-015)
```bash
just test-all   # fast suite + E2E, both green ⇒ releasable
```

## Security & Notes
- The test signing key, sentinel tokens, and any seam (B) switch are **test-only** and must be
  impossible to activate in production config (off by default; never read from a shipped
  `.env`).
- Never print real secrets in test output; the minted JWT is a throwaway signed with a
  throwaway key.
- The harness must always reach teardown (use `t.Cleanup`/`defer` and a `TestMain` that stops
  the backend in all exit paths) to satisfy the "no orphan processes" intent in
  `docs/intent.md`.
- `role`/`permissions` are fixed placeholders today; E2E asserts the current placeholders and
  should be updated when real resolution lands.
