# ISSUE-012: Establish the Test Strategy and Layering (Release Gate for CLI + Backend)

## Status
- **Status**: Open / In Development
- **Priority**: High (defines the quality bar and the release gate for the whole project;
  every later test issue is a concrete instance of this policy)
- **Assignee**: AI Agent / Developer
- **Type**: Umbrella / policy issue (formulates the strategy; the buildable pieces live in the
  child issues below)
- **Depends on**: Nothing new. Builds on the existing test suites already present in
  `apps/backend/internal/server/*_test.go` and `apps/cli/*_test.go`.
- **Children (implementation issues)**:
  - ISSUE-013 — End-to-end test harness: managed backend lifecycle + deterministic test auth.
  - ISSUE-014 — Standalone unit/component tests (mocks, table-driven, boundary cases).
  - ISSUE-015 — CI pipeline (GitHub Actions) + SBOM / supply-chain gate.

## Objective
Define a **single, documented test strategy** that both the CLI (`apps/cli`) and the backend
(`apps/backend`) follow, so that **"all tests green" is a trustworthy release signal**. The
strategy fixes:
1. the **layers** of testing and what each layer is responsible for,
2. the principle that the **suite is the release gate** (a fully green run means the build is
   releasable),
3. how tests obtain a **valid authenticated session without an interactive Google login**, and
4. where tests run today (this dev container) and where they will run later (CI / GitHub
   Actions).

This issue produces the *strategy and conventions*; the *mechanics* are delivered by the child
issues. Treat this file as the source of truth for "how we test Calyx".

## Background
Calyx is a template repository (`docs/intent.md`): the goal is a reusable, AI-agent-facing CLI
plus its backend. For a template, the **test approach is itself a deliverable** — downstream
repositories will copy it. The architecture (`docs/spec.md`) is a CLI gRPC client talking to a
backend over gRPC, with a Google-auth → session-JWT flow.

Today there are good *unit-level* tests using Go's standard `testing` package and in-memory
`bufconn` gRPC servers (e.g. `apps/cli/main_test.go`, `apps/backend/internal/server/auth_test.go`).
What is missing is:
- a **larger, end-to-end** layer that runs the **real CLI binary against a real backend
  process** over a real socket (the closest thing to what a user/agent actually runs),
- an agreed way to **authenticate deterministically** in tests (the auth flow normally needs a
  human at a browser), and
- a **release gate** + **supply-chain (SBOM) checks** wired into CI.

## Design Decisions

### 1. A pragmatic test pyramid with an explicit E2E top
| Layer | Lives in | Runs against | Owner issue |
| --- | --- | --- | --- |
| **Unit / component** | `*_test.go` next to the code; `bufconn` for gRPC | In-process, mocked boundaries | ISSUE-014 |
| **End-to-end (E2E)** | a dedicated package/dir, gated by a build tag | The **real** `bin/backend` + `bin/calyx` over a TCP socket | ISSUE-013 |
The pyramid is wide at the bottom (fast table-driven units) and narrow at the top (a handful of
high-value E2E journeys). E2E is the "can a real agent actually use this end to end?" check.

### 2. The full suite is the release gate
A single command — `just test` for the fast suite, plus the E2E suite (ISSUE-013) — must, when
green, mean the build is **safe to release**. No separate manual checklist. CI (ISSUE-015)
enforces the same gate on every push/PR. This is the concrete meaning of *"all tests passing ⇒
releasable"*.

### 3. Tests authenticate without an interactive Google login
The auth flow (`docs/spec.md`) requires a browser-based Google sign-in, which **cannot run per
test**. The strategy is to provide a **deterministic, non-interactive auth seam** for tests so
the *rest* of the system (token attachment, backend verification, `auth status`, authorized
RPCs) is exercised normally. The seam is designed and built in ISSUE-013; this issue only
mandates that such a seam exist and that **no test ever drives a real Google consent screen**.

Two viable seams (ISSUE-013 picks and documents one):
- **(A) Pre-minted session JWT** — the harness signs a session JWT with the same HS256
  `CALYX_JWT_SIGNING_KEY` the backend verifies with, and writes it to the CLI's token store. No
  production code change; does not cover the `Login` exchange.
- **(B) Gated test verifier** — a backend Google-token verifier seam, off by default and enabled
  only in tests/CI, so the harness can call the real `AuthService.Login` with a sentinel token
  and receive a real session JWT through the real code path. Covers `Login` end to end.

### 4. Test where we deploy effort: dev container now, CI later
All layers must run **inside this dev container** with the preinstalled toolchain (`just test`,
`just build`). The **same** commands must run unchanged in CI (ISSUE-015). No test may depend on
host-specific state, a real Google account, or network access to Google.

### 5. Do not rewrite existing tests
Existing tests are **in scope to run, out of scope to revise**. New layers are **added
alongside** them. If an existing test blocks a new layer, prefer extending/refactoring *shared
harness helpers* over rewriting assertions. (Explicit constraint from the development plan.)

### 6. Determinism and isolation are non-negotiable
- No real network egress (Google, module proxy at test time, etc.). Mock or gate every external
  boundary.
- Inject clocks where time matters (the backend already does — `AuthServer.now`); never assert
  on wall-clock time.
- Each test owns its filesystem state via `t.TempDir()` + `CALYX_CONFIG_DIR`, and its TCP port
  via an OS-assigned free port (`:0`) — never a hardcoded `50051` in tests.

## Scope

### In Scope
- This document: the agreed layering, the release-gate principle, the no-interactive-auth
  principle, the dev-container-now/CI-later principle, and the "don't rewrite existing tests"
  constraint.
- A short **conventions** section (below) that child issues and future tests must follow.
- Defining the child issues (013/014/015) and their boundaries.

### Out of Scope (delegated to child issues)
- The E2E harness implementation, process lifecycle, and the chosen auth seam → **ISSUE-013**.
- New unit/component tests and table-driven coverage → **ISSUE-014**.
- The GitHub Actions workflow, the release gate wiring, SBOM generation, and dependency
  vulnerability scanning → **ISSUE-015**.
- Any change to product behavior. This issue is documentation + decisions only.

## Technical Specifications

### Conventions every Calyx test must follow
1. **Framework**: Go standard `testing`. No third-party assertion/BDD framework (keep the
   template dependency-light). `bufconn` for in-process gRPC; a real socket only at the E2E
   layer.
2. **Table-driven by default** for logic with multiple input/output cases; one `t.Run`
   sub-test per row, with a descriptive `name`.
3. **Golden files** under `testdata/` for stable serialized output (precedent:
   `apps/cli/testdata/schema.golden.json`). Provide an `-update` mechanism in the test, not a
   manual edit step.
4. **Environment isolation**: use `t.Setenv` + `t.TempDir()`; set `CALYX_CONFIG_DIR` so the
   token store never touches the developer's real config dir.
5. **No interactive auth, ever**: tests use the ISSUE-013 auth seam, never a browser flow.
6. **Build tags for slow/E2E tests**: the E2E layer is tagged (e.g. `//go:build e2e`) so the
   fast inner-loop (`go test ./...`) stays quick, while CI runs both.
7. **Naming**: `Test<Unit>_<Condition>` (existing convention, e.g.
   `TestSayHello_AttachesBearer`).

### Release-gate definition
The build is **releasable** iff, on a clean checkout in the dev container (and identically in
CI):
- `just build` succeeds (both binaries compile), and
- `just test` is green (unit/component layer), and
- the E2E suite (ISSUE-013) is green, and
- the supply-chain checks (ISSUE-015: `go mod verify`, `govulncheck`, SBOM generation) pass.

### What each layer must cover (acceptance summary)
- **Unit/component (ISSUE-014)**: input validation, error/branch coverage, boundary values,
  token-store edge cases, metadata attachment, response rendering — all with mocked boundaries.
- **E2E (ISSUE-013)**: at minimum the journeys
  `auth status` (authenticated) → name/role/permissions/expiry rendered; `auth status`
  (no/expired token) → not-authenticated message; `sample hello <name>` → greeting; and an
  unauthorized/expired path returns the expected error — all against the real binaries.

## Directory and File Mapping
- `docs/issues/012-test-strategy-and-layering.md` (this file): the strategy of record.
- `docs/issues/013-*.md`, `docs/issues/014-*.md`, `docs/issues/015-*.md` (Add): the child
  implementation issues.
- No source or test files change under this issue; the child issues add them. (Existing
  `*_test.go` files are referenced as the current baseline, not modified here.)

## Implementation Steps
1. Land this strategy doc and the three child issues (013/014/015) with the boundaries above.
2. Implement ISSUE-014 (unit/component coverage) and ISSUE-013 (E2E + auth seam) — order is
   independent; they share helper conventions defined here.
3. Implement ISSUE-015 (CI) to enforce the release gate on every push/PR.
4. Once all three are merged, update this file's Status to reflect the strategy as **in force**.

## Verification and Testing Plan
This issue is verified by **review and consistency**, not code:
- The three child issues exist and each cites this file for its layer and conventions.
- The conventions here do not contradict existing tests (they generalize the patterns already
  used in `apps/cli/*_test.go` and `apps/backend/internal/server/*_test.go`).
- After the children land: a clean dev-container run of `just build`, `just test`, and the E2E
  suite is green, demonstrating the release gate end to end.

## Future Work
- Coverage reporting (`go test -cover` aggregation) and an optional minimum-coverage threshold
  once the suites stabilize.
- Performance/timeout tests aligned with the future "timeout & orphan-process prevention"
  feature in `docs/intent.md`.
- Contract/compatibility tests if/when the gRPC surface gains backward-compatibility guarantees
  (buf breaking-change checks already exist via `buf lint`/`buf` tooling and can be extended).
