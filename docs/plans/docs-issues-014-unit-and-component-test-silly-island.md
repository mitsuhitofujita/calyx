# Plan: ISSUE-014 — Strengthen Standalone Unit / Component Tests

> Execution plan for an AI coding agent. Test-only changes. No production code is
> modified. Follow the constraints in §6 exactly.

## 1. Context

**Why this change.** ISSUE-012 defines a test pyramid whose wide base is fast,
in-process, table-driven unit/component tests with mocked boundaries. ISSUE-014
is the task to fill the remaining gaps in that base for both `apps/cli` and
`apps/backend`, so logic branches and boundary conditions are caught in
milliseconds rather than by the coarse E2E layer (ISSUE-013) or in CI
(ISSUE-015).

**Important finding (read before coding).** The suite is *already ~90% there*.
Prior feature issues (004–009) shipped strong tests using exactly the patterns
ISSUE-014 prescribes (`bufconn`, stub verifier, injected clock, golden file,
`t.TempDir()` + `CALYX_CONFIG_DIR`). This plan therefore **closes the specific
remaining branch/boundary gaps** and **does not rewrite or duplicate** existing
coverage. §3 lists what is already covered (no action) and §4 lists the exact
gaps to fill.

**Outcome.** After this work, every reachable branch in `auth.go`, `sample.go`,
`store.go`, and the CLI leaf handlers is asserted by a fast hermetic test, and
`go test -race ./...` is green.

## 2. Reusable assets (do not reinvent — ISSUE-012/014 constraint)

Backend (`apps/backend/internal/server/`):
- `auth_test.go`: `newTestAuthClient(t, cfg, v)` (bufconn + stub verifier +
  fixed clock), `stubVerifier`, fixtures `testSigningKey`/`testNow`/`testUser`/
  `testAuthConfig()`, `mintSessionToken(t, sessionTokenOpts)`, `statusContext(token)`.
- `sample_test.go`: `newTestClient(t)` + the existing `TestSampleServer_Hello` table.

CLI (`apps/cli/`):
- `main_test.go`: `recordingSampleServer`, `newTestSampleClient(t)`.
- `auth_test.go`: `recordingAuthServer`, `newTestAuthClient(t, resp)`.
- `store_test.go`: `newFileStore(t)`, `sampleToken()`.
- `registry_test.go`: `TestResolve` table.
- `schema_test.go` + `testdata/schema.golden.json` + the `-update` flag.

Module path: `github.com/mitsuhitofujita/calyx`. Deps already present:
`golang-jwt/jwt/v5`, grpc/`bufconn`, `godotenv`.

## 3. Already covered — NO ACTION (do not duplicate)

- **Backend Login**: happy path (full claim assertions), verifier error →
  `Unauthenticated`, `auth_code` → `Unimplemented`, empty request (`default`
  branch) → `InvalidArgument`, exp/expires_at consistency.
- **Backend Status**: valid, no metadata, malformed, wrong signature, expired,
  wrong issuer, wrong audience, Login→Status round-trip.
- **CLI store** (`store.go`): save/load round-trip, `ErrNoToken`, corrupt file,
  `0600`/`0700` perms, overwrite, delete (+ idempotent), `CALYX_TOKEN_STORE`
  selector matrix (``/`file`/`keyring`/unknown), `sessionFilePath` via
  `CALYX_CONFIG_DIR`. **This file is effectively complete; add nothing.**
- **CLI outgoing metadata** (`sayHello`): Bearer attached / absent / bad-store.
- **CLI auth status renderer**: authenticated render, not-authenticated render,
  empty-email omission, no-token short-circuit, bad-store, extra-args.
- **CLI dispatch routing** (`TestResolve`): unknown root/sub commands, group
  without subcommand, correct leaf + unconsumed args.
- **CLI schema**: registry↔schema no-drift, JSON validity (no `null`), golden.
  **No new command is added in this issue, so the golden MUST stay byte-identical
  — do not run `-update`.**

## 4. Gaps to close (the actual work)

Each task names the exact file, the target branch in production code, the new
test, and the assertion. All are additive (new test funcs or new table rows).

### Backend — `apps/backend/internal/server/`

| ID | File | Production branch (uncovered) | New test |
| --- | --- | --- | --- |
| B1 | `auth_test.go` | `loginWithIDToken` empty-token guard (`auth.go:95-97`): `LoginRequest_IdToken{IdToken: ""}` → `InvalidArgument`. Distinct from the existing `default`-branch test. | `TestAuthServer_Login_EmptyIDToken` |
| B2 | `auth_test.go` | `verifySessionToken` `jwt.WithValidMethods(["HS256"])` (`auth.go:182`): a non-HS256 (`alg=none`) token must be rejected. Also gives a fast **direct** unit for the function (the issue's `TestVerifySessionToken` skeleton). | `TestVerifySessionToken` (table) |
| B3 | `auth_test.go` | `bearerTokenFromContext` (`auth.go:195-209`): empty header, bare token (no scheme), lowercase `bearer`, surrounding whitespace, `"Bearer "` with empty remainder. | `TestBearerTokenFromContext` (table) |
| B4 | `sample_test.go` | `Hello` boundary inputs. | extend existing `TestSampleServer_Hello` table |

**B1** — mirror `TestAuthServer_Login_EmptyRequest` but send an explicit empty
id_token; assert `status.Code(err) == codes.InvalidArgument`.

**B2** — construct the server directly (no bufconn, faster):
`s := newAuthServer(testAuthConfig(), stubVerifier{}); s.now = func() time.Time { return testNow }`.
Build inputs with the existing `mintSessionToken(t, sessionTokenOpts{...})`; add
a local helper for the `alg=none` token:
`jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)`.
Table rows: `valid`→ok; `expired`; `wrong key`; `wrong issuer`; `wrong audience`;
`malformed string`; **`none algorithm`** (the new branch). Assert
`(claims, err)` shape per row (valid → non-nil claims & nil err; others → err).

**B3** — build incoming contexts with
`metadata.NewIncomingContext(ctx, metadata.MD{"authorization": []string{v}})`
(and a no-metadata case = plain `context.Background()`). Table of
`{name, mdValue/present, want}`:
- no metadata → `""`
- key absent / empty value `""` → `""`
- `"Bearer abc"` → `"abc"`
- `"bearer abc"` → `"abc"` (EqualFold)
- `"  Bearer   abc  "` → `"abc"`
- `"abc"` (bare) → `"abc"`
- `"Bearer "` → `""`

**B4** — append rows to the existing table (do not restructure it):
`{"unicode name", "世界", "Hello, 世界."}` and a long-name row built from
`strings.Repeat("a", 1000)` with `want = "Hello, " + name + "."`. (Add the
`strings` import.)

### CLI — `apps/cli/`

| ID | File | Production branch (uncovered) | New test |
| --- | --- | --- | --- |
| C1 | `main_test.go` | `runHello` arity guard (`main.go:69-72`): 0 args and 2 args → `errUsage` (returns before any dial). | `TestRunHello_RejectsBadArity` (table) |
| C2 | `auth_test.go` | `runAuthLogin` arity guard (`auth.go:47-50`): extra args → `errUsage` (returns before OAuth). | `TestRunAuthLogin_RejectsExtraArgs` |
| C3 | `auth_test.go` | `runAuthLogin` config validation (`auth.go:56-63`): missing `CALYX_GOOGLE_CLIENT_ID` / `CALYX_GOOGLE_CLIENT_SECRET` → actionable error (returns before browser). | `TestRunAuthLogin_MissingConfig` (table) |
| C4 | `auth_test.go` | `fetchStatus` RPC-error mapping (`auth.go:178-180`): Status RPC error → wrapped `"backend status check failed"`. | `TestFetchStatus_RPCError` |

**C1** — `runHello` returns `errUsage` for `len(args) != 1` *before* `dialBackend`,
so no backend is needed. Table: `[]string{}` and `[]string{"a", "b"}`, each
`errors.Is(err, errUsage)`. Set `t.Setenv("CALYX_CONFIG_DIR", t.TempDir())` for
hygiene. Do **not** test the 1-arg success path here (that needs a backend → it
is covered by `sayHello` tests and E2E/ISSUE-013).

**C2** — `runAuthLogin([]string{"extra"})` → `errors.Is(err, errUsage)`.

**C3** — arity passes, then config check fires before `NewTokenStore`/`authorize`.
Set the vars explicitly to empty with `t.Setenv` (godotenv never overrides a set
var, so this is deterministic regardless of any `.env`). Rows:
- id `""`, secret `""` → err contains `CALYX_GOOGLE_CLIENT_ID`
- id `"x"`, secret `""` → err contains `CALYX_GOOGLE_CLIENT_SECRET`

Call `runAuthLogin(nil)`; assert non-nil err containing the expected substring.

**C4** — additive helper change: add an `err error` field to `recordingAuthServer`
and return `s.resp, s.err` from its `Status`. Extract the bufconn boilerplate of
`newTestAuthClient` into `registerAuthStub(t, *recordingAuthServer) AuthServiceClient`
and keep `newTestAuthClient(t, resp)` as a thin wrapper (signature & behavior
unchanged — sanctioned "additive helper refactor"). New test: build a stub with
`err: errors.New("backend down")`, call `fetchStatus(client, "jwt")`, assert err
is non-nil and `strings.Contains(err.Error(), "backend status check failed")`.

### Out of scope / explicitly skipped
- `sessionFilePath` `os.UserConfigDir` fallback (CALYX_CONFIG_DIR unset): OS-
  dependent and the named requirement (path via `CALYX_CONFIG_DIR`) is already
  covered. Skip to keep tests hermetic.
- Consolidating existing per-case test funcs into tables — forbidden by the
  "add, don't rewrite" constraint.
- Any `schema.golden.json` change — none is warranted (no command added).

## 5. Critical files

Modify (extend only, append new funcs / table rows):
- `apps/backend/internal/server/auth_test.go` — B1, B2, B3
- `apps/backend/internal/server/sample_test.go` — B4
- `apps/cli/main_test.go` — C1
- `apps/cli/auth_test.go` — C2, C3, C4 (+ additive `recordingAuthServer.err` /
  `registerAuthStub` helper)

Do **not** touch: any `*.go` production file, `apps/cli/testdata/schema.golden.json`,
`store_test.go`, `registry_test.go`, `schema_test.go`, `justfile`.

## 6. Constraints (enforced)

1. **Add, don't rewrite** — never weaken or restructure existing assertions;
   only append test funcs or table rows; helper refactors must preserve existing
   signatures and behavior.
2. **Hermetic & fast** — no real socket, no Google, no `time.Sleep`. Use
   `bufconn`, `stubVerifier`, the injected `now`, `t.TempDir()` + `t.Setenv`.
3. **Table-driven** for multi-case logic; one `t.Run(tc.name, ...)` per row.
4. **Naming** — `Test<Unit>_<Condition>` (match existing style); failure
   messages print `got`/`want` and name the case.
5. **Std `testing` only** — no new third-party deps.

## 7. Verification

Run from repo root after implementation:

```bash
# 1. Fast suite — all new cases pass.
just test                 # == go test ./...

# 2. No data races in the bufconn/goroutine harnesses.
go test -race ./...

# 3. Targeted coverage rose on the previously-unasserted branches.
go test -cover ./apps/backend/internal/server/ ./apps/cli/
#   spot-check the new branches with a profile if needed:
#   go test -coverprofile=/tmp/c.out ./apps/cli/ && go tool cover -func=/tmp/c.out

# 4. Golden integrity — must be unchanged (NO -update run this issue).
go test ./apps/cli/ -run TestSchema_Golden
```

Pass criteria: steps 1–2 green; step 3 shows new coverage on `loginWithIDToken`
empty branch, `verifySessionToken` method-validation, `bearerTokenFromContext`
branches, `runHello`/`runAuthLogin` arity + config, and `fetchStatus` error path;
step 4 green with the golden untouched (`git diff --stat` shows only the four
`*_test.go` files changed).
