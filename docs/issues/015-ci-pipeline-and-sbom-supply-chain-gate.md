# ISSUE-015: CI Pipeline (GitHub Actions) + SBOM / Supply-Chain Gate

## Status
- **Status**: Open / In Development
- **Priority**: High (turns "all tests green ⇒ releasable" into an automated, enforced gate, and
  adds the supply-chain/SBOM checks the development plan calls for)
- **Assignee**: AI Agent / Developer
- **Depends on**: ISSUE-012 (release-gate definition & conventions), ISSUE-013 (E2E suite to
  run), ISSUE-014 (unit/component suite to run).
- **Related**: `docs/intent.md` (template repo: CI is itself a deliverable downstream repos
  copy).

## Objective
Add a **GitHub Actions** workflow that enforces the ISSUE-012 release gate on every push and
pull request, running the **same commands** developers run in the dev container:
1. build both binaries,
2. run the fast unit/component suite (ISSUE-014),
3. run the E2E suite (ISSUE-013),
4. run **supply-chain / SBOM** checks — generate a Software Bill of Materials, verify module
   integrity, and scan dependencies for known vulnerabilities,

such that a **fully green run means the build is safe to release**. This realizes the
"all-tests-pass ⇒ releasable", "future CI", and "SBOM countermeasure" items from the
development plan.

## Background
There is **no CI yet** (no `.github/` in the repo). All tests currently run only by hand in the
dev container via `just`. `docs/intent.md` already anticipates a CI/CD pipeline (e.g. the
auto-update feature signs release binaries with a key kept in CI secrets). This issue stands up
the first, foundational workflow: the **test + supply-chain gate**.

"SBOM対策" (SBOM countermeasure) reflects the industry shift toward **software-supply-chain
security**: knowing exactly which dependencies (and versions) ship, and proving none carry known
vulnerabilities or have been tampered with. For a Go project the standard, low-friction tools
are: `go mod verify` (checksum integrity against `go.sum`), `govulncheck` (Go vulnerability
database scan), and an SBOM generator (CycloneDX or SPDX). Treating these as **gating "tests"**
is exactly what the development plan asks for.

## Design Decisions

### 1. CI runs the dev-container commands verbatim
The workflow drives the **`just` recipes** (`just build`, `just test`, and the E2E/all recipes
from ISSUE-013), not a parallel set of hand-written `go` invocations. One definition of "the
gate", runnable identically locally and in CI. Install `just` + the Go toolchain in the runner;
the proto toolchain is only needed if codegen is part of CI (see decision 5).

### 2. The gate = build + unit + E2E + supply-chain, all green
A PR is mergeable / a commit is releasable only when **every** job passes:
- `build` (both binaries compile),
- `test` (fast suite, ISSUE-014) — run with `-race`,
- `test-e2e` (ISSUE-013) — real backend lifecycle + deterministic test auth,
- `supply-chain` (`go mod verify` + `govulncheck` + SBOM generation).
Make these **required status checks** on the default branch (documented in the issue; branch
protection is configured in repo settings).

### 3. SBOM + vulnerability scanning as first-class gate steps
- **SBOM**: generate a machine-readable SBOM (recommended **CycloneDX** via `cyclonedx-gomod`,
  or SPDX via Syft) for the modules and the built binaries; **upload it as a build artifact**
  (and attach to releases later). This is the "bill of materials" downstream consumers and
  audits need.
- **Vulnerabilities**: run **`govulncheck ./...`**; a finding that affects reachable code fails
  the gate. This is the active "countermeasure".
- **Integrity**: `go mod verify` ensures the dependency tree matches `go.sum` (tamper check).
- *(Optional, recommended for a template):* enable **Dependabot** and **CodeQL** for ongoing,
  scheduled supply-chain coverage beyond per-PR runs.

### 4. Deterministic, hermetic CI
- Pin tool versions (mirror the `justfile`'s pinned `buf`/`protoc-gen-*`/`grpcurl` versions and
  pin `govulncheck`/SBOM tool versions) so runs are reproducible.
- Cache the Go build/module cache for speed, keyed on `go.sum`.
- The E2E job sets every backend env var explicitly (per ISSUE-013) and uses an OS-assigned
  port — **no real Google account, no network to Google**, no developer `.env`.
- Run on a pinned `ubuntu-latest`-class runner (Linux is the dev/runtime target per
  `docs/spec.md`); a Windows job can be added later since the CLI ships on Windows 11.

### 5. Optional codegen drift check
Optionally add a job that runs `just generate` and fails if it produces a diff (the committed
`*.pb.go` must match the `.proto` sources). Keep it separate from the test gate so a toolchain
hiccup doesn't mask test results. Decide during implementation.

### 6. Do not change product code or existing tests
This issue adds CI config (and possibly small `justfile` recipes for the supply-chain steps)
only. No source or existing-test changes (ISSUE-012 constraint).

## Scope

### In Scope
- A GitHub Actions workflow (recommended `.github/workflows/ci.yml`) triggered on `push` and
  `pull_request`, with jobs:
  - **build** → `just build`.
  - **test** → `just test` (add `-race`; either via the recipe or a CI-only flag).
  - **e2e** → the ISSUE-013 E2E recipe (`just test-e2e`).
  - **supply-chain** → `go mod verify`, `govulncheck ./...`, and SBOM generation + artifact
    upload.
- Go toolchain + `just` setup, dependency caching, and pinned tool versions in the workflow.
- New `just` recipes for the supply-chain steps (e.g. `just vuln`, `just sbom`) so they are
  runnable locally too, keeping CI = local.
- Documentation: a CI/status note in `README.md` and the list of **required checks** for branch
  protection.

### Out of Scope (Future Issues)
- The **release/publish** pipeline: building cross-platform binaries, **signing** them (the
  `docs/intent.md` auto-update private key lives in CI secrets), attaching SBOMs to releases,
  and the distribution server. This issue is the **gate**, not the release.
- Coverage thresholds as a hard gate (ISSUE-014 keeps coverage informational for now).
- Multi-OS (Windows) test matrix — a follow-up once the Linux gate is stable.
- Implementing the test layers themselves (ISSUE-013/014).

## Technical Specifications

### Trigger & gate
- **On**: `push` (default branch + topic branches) and `pull_request`.
- **Gate**: all jobs must pass; configure them as **required status checks** on the default
  branch (repo settings — documented here).

### Jobs (illustrative shape)
| Job | Command(s) | Fails when |
| --- | --- | --- |
| `build` | `just build` | either binary fails to compile |
| `test` | `just test` (with `-race`) | any unit/component test fails or a race is detected |
| `e2e` | `just test-e2e` | any E2E journey fails or the backend can't start/stop cleanly |
| `supply-chain` | `go mod verify` → `govulncheck ./...` → SBOM gen | tampered modules, a reachable known vuln, or SBOM generation error |

### Supply-chain tooling (recommended, pin versions)
- **Integrity**: `go mod verify`.
- **Vulnerabilities**: `golang.org/x/vuln/cmd/govulncheck` → `govulncheck ./...`.
- **SBOM**: `cyclonedx-gomod` (CycloneDX) → emit `sbom.cdx.json`; or Syft for SPDX. Upload via
  `actions/upload-artifact`.
- *(Optional ongoing)*: `.github/dependabot.yml` for `gomod` + `github-actions`; a CodeQL
  workflow for Go.

### Suggested `just` additions (so CI == local)
```
vuln:
    go run golang.org/x/vuln/cmd/govulncheck@<pinned> ./...

sbom:
    # cyclonedx-gomod (or syft) → sbom.cdx.json
    cyclonedx-gomod mod -json -output sbom.cdx.json

supply-chain: vuln sbom
    go mod verify
```
(Exact recipes finalized in implementation; keep tool versions pinned as the existing `justfile`
already does for `buf`/`grpcurl`.)

## Directory and File Mapping
- `.github/workflows/ci.yml` (Add): the gate workflow (build / test / e2e / supply-chain jobs).
- `justfile` (Modify): add `vuln`, `sbom`, `supply-chain` recipes; reference the ISSUE-013
  `test-e2e`/`test-all` recipes.
- `.github/dependabot.yml`, `.github/workflows/codeql.yml` (Add — optional): ongoing supply-chain
  coverage.
- `README.md` (Modify): a CI/status badge and the required-checks list.
- No source or existing-test changes.

## Implementation Steps
1. **Workflow skeleton**: triggers, Linux runner, checkout, `setup-go` (pinned), install `just`,
   Go module/build cache keyed on `go.sum`.
2. **build + test jobs**: `just build`; `just test` with `-race`.
3. **e2e job**: run the ISSUE-013 suite; ensure the backend env is fully specified and the run
   leaves no orphan process.
4. **supply-chain job**: `go mod verify`; install + run `govulncheck ./...`; generate the SBOM
   and upload it as an artifact. Add the matching `just` recipes.
5. **(Optional)** codegen-drift job (`just generate` → assert no diff); Dependabot + CodeQL.
6. **Branch protection**: mark the jobs as required checks (document the setting); add the
   README badge.

## Verification and Testing Plan
### 1. Dry-run locally (CI == local)
```bash
just build
just test          # add -race locally too
just test-e2e      # ISSUE-013
just supply-chain  # go mod verify + govulncheck + SBOM (new recipe)
```
All green locally ⇒ the workflow should be green.
### 2. On a PR
Open a draft PR; confirm every job runs and passes, the SBOM artifact is attached, and a
deliberately-introduced failing test / a `govulncheck` finding **blocks** the gate (then revert).
### 3. Reproducibility
Re-run the workflow; pinned tool versions + caching produce the same result without network
flakiness.

## Notes & Future Work
- This is the **gate**, not the **release**. The signing-and-publish pipeline (auto-update
  binaries, SBOM attached to GitHub Releases, distribution server) is a separate future issue
  building on these checks and `docs/intent.md`.
- Keep the gate fast: cache aggressively, run the wide unit suite first (fail fast), E2E and
  supply-chain in parallel jobs.
- As the template is reused downstream, this workflow is copied as-is — keep it minimal,
  pinned, and dependency-light.
