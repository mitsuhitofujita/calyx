# ISSUE-009: Add the `calyx auth status` CLI Command (Report the Stored Session's Status)

## Status
- **Status**: Open / In Development
- **Priority**: High (CLI side of the session-verification flow in spec.md; the user-facing counterpart of the backend `AuthService.Status` RPC)
- **Assignee**: AI Agent / Developer
- **Depends on**: ISSUE-007 (CLI persists the session token via `TokenStore` and attaches it as `authorization: Bearer <jwt>` metadata), ISSUE-008 (backend `AuthService.Status` RPC that verifies the token and reports its status)
- **Blocks**: Nothing currently; completes the "inspect my current login" capability for the CLI.

## Objective
Add an `auth status` subcommand to the `calyx` CLI so a user (or an AI agent) can ask:
**"Am I logged in, and as whom?"**

`calyx auth status`:

1. Loads the locally stored Calyx session token (from ISSUE-007's `TokenStore`),
2. Calls the backend `AuthService.Status` RPC (ISSUE-008), attaching the token as
   `authorization: Bearer <session_token>` gRPC metadata, and
3. Renders the result in human-readable form:
   - **Authenticated** → the user's **name**, **role**, **permissions (authorizations)**, and
     **session expiry** (plus email as a supplement).
   - **Not authenticated** (no token stored, or the token is invalid/expired) → a clear
     "not authenticated" message and a hint to run `calyx auth login`.

This issue covers the **CLI side only**. The backend verification endpoint already exists
(ISSUE-008); this command consumes it.

## Background
Per `intent.md` and `spec.md`, the backend verifies the short-lived session JWT statelessly
on each request. ISSUE-008 exposed a single explicit endpoint — effectively a `whoami` — that
verifies a presented session token and reports the outcome in the response body
(`authenticated: true/false`, a `message`, and a populated `session` when valid), returning a
gRPC error only on genuine server faults.

ISSUE-007 already gave the CLI everything needed to *talk* to that endpoint: a `TokenStore`
that loads the persisted session token, and the established pattern of attaching it as
`authorization: Bearer <jwt>` metadata on outgoing requests. This issue wires those together
behind a new subcommand and presents the verified result to the user.

## Design Decisions
These keep the command small and consistent with the existing CLI structure and error model.

1. **The backend is the source of truth for a *present* token; the CLI does not verify the
   JWT itself.** Only the backend holds the signing key and can validate the signature,
   issuer, audience, and expiry. When a token is stored, the command always calls
   `AuthService.Status` and renders whatever the backend reports. The CLI never parses or
   trusts the token's claims locally.
2. **A missing local token is reported without a backend round-trip.** If `TokenStore.Load`
   returns `ErrNoToken`, the user is certainly not authenticated, so the command prints the
   "not authenticated / run `calyx auth login`" message directly and skips the network call.
   This keeps `auth status` fast and usable offline, and mirrors how `withAuth` in
   `apps/cli/main.go` already special-cases `ErrNoToken`.
3. **Both auth states are a *successful* status report (exit code 0).** "Authenticated" and
   "not authenticated" are both valid answers to "what is my status?", so neither is treated
   as a command failure. Only genuine operational problems — a misconfigured store, a token
   load/read failure, the backend being unreachable, or a gRPC `Internal` error — return an
   `error`, which the existing `main` handler prints as `Error: ...` to stderr and exits `1`.
   This fits the current CLI error model (handlers return `error`; `main` maps it to a
   non-zero exit) with no new machinery. (A machine-readable `--json` mode and/or a distinct
   exit code for the not-authenticated state are noted as future work below.)
4. **Rendering is a pure function, separate from the RPC call, for testability.** Following
   the existing `sayHello` pattern (a small, testable RPC unit), split the work into a
   `fetchStatus(client, token)` unit (attaches the Bearer token and calls `Status`) and a
   pure `formatStatus(resp)` renderer. The pure renderer is unit-tested without a network,
   and `fetchStatus` is tested against an in-memory bufconn stub that records metadata.
5. **The "no local token" path reuses the not-authenticated rendering.** Rather than a
   bespoke print, synthesize a local `StatusResponse{ authenticated: false, message: ... }`
   and pass it through the same `formatStatus` renderer, so the no-token and
   backend-reported-not-authenticated outputs stay uniform.
6. **No new configuration.** The command reuses `CALYX_BACKEND_ADDR` (backend address) and the
   ISSUE-007 storage selectors (`CALYX_TOKEN_STORE`, `CALYX_CONFIG_DIR`). No `.env.example`
   change is required.

## Scope

### In Scope
- A new `status` subcommand under `auth`, dispatched from `runAuth` in `apps/cli/auth.go`.
- Loading the stored session token via `NewTokenStore()` / `TokenStore.Load()`; short-circuit
  to the not-authenticated output on `ErrNoToken`; hard-fail on a misconfigured store or a
  real load error.
- Calling `AuthService.Status` with the token attached as `authorization: Bearer <jwt>`
  metadata, over the existing insecure dev transport, with a bounded timeout.
- Rendering the response: on success, the user's name (and email), role, permissions, and
  expiry; on failure, the backend's not-authenticated message plus a re-login hint.
- Updating the CLI usage/help text (`flag.Usage` and the `auth` subcommand usage string) to
  list `auth status`.
- Unit/integration tests for: token attached and response rendered (authenticated),
  not-authenticated rendering, the no-local-token short-circuit (no backend call), a store
  misconfiguration hard-fail, and argument validation.

### Out of Scope (Future Issues)
- The backend gRPC verification **interceptor** that enforces the session token on *other*
  protected RPCs and drives the re-authentication-on-expiry flow (`spec.md` "Request
  Handling"). `auth status` only *reports* status; it does not delete the token or trigger a
  re-login automatically on expiry.
- Automatic deletion of an expired/invalid token from the store on detection (belongs with the
  interceptor / re-auth work).
- A machine-readable output mode (`--json`) and/or a distinct exit code for the
  not-authenticated state, for AI-agent scripting.
- The OS-native keyring storage backend (still reserved behind `CALYX_TOKEN_STORE=keyring`
  from ISSUE-007).
- Real (non-fixed) role/permission resolution (the backend still echoes the placeholder
  `role = "admin"`, `permissions = ["*"]`).

## Technical Specifications

### Command
- **Invocation**: `calyx auth status` (no arguments).
- **Dispatch**: add a `case "status"` to the `switch` in `runAuth` (alongside `login`),
  calling a new `runAuthStatus(args []string) error`.
- **Argument validation**: reject any extra arguments with a usage message to stderr and
  `errUsage` (matching `runAuthLogin`).

### Backend RPC Consumed
- **gRPC Path**: `/mitsuhitofujita.calyx.v1.AuthService/Status`
- **Request**: `StatusRequest{}` (empty — the token travels in metadata, not the body).
- **Response**: `StatusResponse{ authenticated, message, session }`, where `SessionInfo`
  carries `name`, `email`, `role`, `permissions[]`, and `expires_at`.

### Control Flow (`runAuthStatus`)
1. Validate no extra args.
2. `store, err := NewTokenStore()` — a misconfigured `CALYX_TOKEN_STORE` (e.g. `keyring` or an
   unknown value) fails fast here with an actionable error (return it).
3. `tok, err := store.Load()`:
   - `errors.Is(err, ErrNoToken)` → render the not-authenticated output (synthesize a local
     `StatusResponse{ authenticated: false, message: "not logged in" }`) and return `nil`
     (no backend call).
   - other `err` → wrap and return (hard-fail).
4. Dial the backend (`dialBackend()` / `CALYX_BACKEND_ADDR`, insecure dev transport, as in
   `login` and `sample hello`).
5. `fetchStatus(client, tok.Token)`: build a context with a bounded timeout (reuse
   `backendCallTimeout`), attach the token via
   `metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)`, then call
   `client.Status(ctx, &calyxv1.StatusRequest{})`. A transport/`Internal` gRPC error is
   wrapped and returned (hard-fail).
6. `formatStatus(resp)` → print to stdout; return `nil`.

### Output Format (human-readable, plain text)
On **authenticated** (`resp.GetAuthenticated() == true`), print the session details, e.g.:

```
Status:      authenticated
Name:        Alice Example
Email:       alice@example.com
Role:        admin
Permissions: *
Expires:     2026-06-07T12:34:56Z
```

- Render `expires_at` as RFC3339 UTC (`resp.GetSession().GetExpiresAt().AsTime().UTC().Format(time.RFC3339)`).
- Join `permissions` with `, `.
- Email is supplementary; if empty, the line may be omitted.

On **not authenticated** (`authenticated == false`, whether from the backend or the local
no-token short-circuit), print the reason and the re-login hint, e.g.:

```
Status:  not authenticated (session token is invalid or expired)
Run `calyx auth login` to authenticate.
```

The reason in parentheses is `resp.GetMessage()` (e.g. `no session token provided`,
`session token is invalid or expired`, or the local `not logged in`).

### Output / Exit-Code Semantics Summary

| Situation | Backend called? | stdout | Exit code |
| --- | --- | --- | --- |
| Valid session token | yes | authenticated + session details | `0` |
| Token stored but invalid/expired (backend says so) | yes | not authenticated + reason + hint | `0` |
| No token stored locally (`ErrNoToken`) | no | not authenticated ("not logged in") + hint | `0` |
| Misconfigured store / token load error | no | — (`Error: ...` to stderr) | `1` |
| Backend unreachable / gRPC `Internal` | attempted | — (`Error: ...` to stderr) | `1` |
| Extra arguments | no | — (usage to stderr) | `1` |

### Configuration (Environment Variables)
None new. Reuses `CALYX_BACKEND_ADDR` and ISSUE-007's `CALYX_TOKEN_STORE` / `CALYX_CONFIG_DIR`.
No `.env.example` change is required.

## Directory and File Mapping
- `apps/cli/auth.go` (Modify): add the `status` case to `runAuth`; add `runAuthStatus`,
  `fetchStatus`, and the pure `formatStatus` renderer; update the `auth` usage string to
  `usage: calyx auth <login|status>`.
- `apps/cli/main.go` (Modify): add an `auth status` line to `flag.Usage`. (No change to
  `dialBackend`; reuse it.)
- `apps/cli/auth_test.go` (Add): tests for `fetchStatus` (metadata attachment + response),
  `formatStatus` (authenticated and not-authenticated), the no-token short-circuit, the
  misconfigured-store hard-fail, and argument validation — using an in-memory bufconn
  `AuthService.Status` stub that records incoming metadata, in the style of the existing
  `recordingSampleServer` harness.

> No proto or backend changes: `StatusRequest` / `StatusResponse` / `SessionInfo` and the
> `Status` RPC already exist from ISSUE-008.

## Implementation Steps

### Step 1: Dispatch and Command Skeleton
1. In `runAuth` (`apps/cli/auth.go`), add `case "status": return runAuthStatus(args[1:])`.
2. Update the `auth` usage string to mention `status`.
3. Add `runAuthStatus` with argument validation (no extra args).

### Step 2: Token Loading and Short-Circuit
1. Resolve the store with `NewTokenStore()` (fail fast on misconfiguration).
2. `Load()` the token; on `ErrNoToken`, render the local not-authenticated output and return
   `nil`; on any other error, wrap and return.

### Step 3: Backend Call and Rendering
1. Add `fetchStatus(client calyxv1.AuthServiceClient, token string) (*calyxv1.StatusResponse, error)`
   that builds a bounded context, attaches `authorization: Bearer <token>`, and calls
   `Status`.
2. Add the pure `formatStatus(resp *calyxv1.StatusResponse) string` renderer for the
   authenticated and not-authenticated cases.
3. Wire `runAuthStatus`: dial via `dialBackend()`, call `fetchStatus`, print `formatStatus`.

### Step 4: Help Text
1. Add the `auth status` line to `flag.Usage` in `apps/cli/main.go`.

### Step 5: Tests
1. Add the cases in the testing plan below to `apps/cli/auth_test.go`.

## Verification and Testing Plan

### 1. Build
```bash
just build
```
Verify `bin/calyx` builds.

### 2. Unit / Integration Tests
```bash
just test
```
Add to `apps/cli/auth_test.go`, reusing a bufconn stub that implements `AuthService.Status`
and records the incoming metadata (mirror `recordingSampleServer` from the existing CLI
tests). Set `CALYX_CONFIG_DIR` to a `t.TempDir()` so token storage is isolated. Cover:

- **Authenticated render**: stub returns
  `StatusResponse{ authenticated: true, message: "session is valid", session: {name, email, role: "admin", permissions: ["*"], expires_at} }`;
  assert the stub received `authorization: Bearer <token>` and that `formatStatus` includes
  the name, role, permissions, and the RFC3339 expiry.
- **Not-authenticated render**: stub returns
  `StatusResponse{ authenticated: false, message: "session token is invalid or expired" }`;
  assert `formatStatus` shows "not authenticated", the reason, and the re-login hint.
- **No-token short-circuit**: with a temp config dir and no token saved, `runAuthStatus` (or
  the load step) takes the `ErrNoToken` path, prints the not-authenticated output, and makes
  **no** backend call (verify the stub's recorded metadata stays empty / the stub is never
  invoked); exit-equivalent return is `nil`.
- **Misconfigured store hard-fail**: `CALYX_TOKEN_STORE=bogus` → `runAuthStatus` returns a
  non-nil error (matches `TestSayHello_BadStoreConfig`).
- **Argument validation**: `runAuthStatus([]string{"extra"})` returns `errUsage`.

### 3. Manual Smoke Test (optional)
With the backend running (`just run`) and a session token obtained via `calyx auth login`
(ISSUE-007):
```bash
./bin/calyx auth status
```
**Expected (valid token)**: `authenticated`, with name, role (`admin`), permissions (`*`), and
the expiry printed; exit code `0`.

Then, with no stored token (fresh `CALYX_CONFIG_DIR`, or after the session file is absent):
```bash
CALYX_CONFIG_DIR="$(mktemp -d)" ./bin/calyx auth status
```
**Expected**: `not authenticated` with the "not logged in" reason and the re-login hint; exit
code `0`.

Cross-check the backend's view of the same token directly:
```bash
grpcurl -plaintext -H "authorization: Bearer <session-token>" \
  localhost:50051 mitsuhitofujita.calyx.v1.AuthService/Status
```
**Expected**: `authenticated: true` with the matching `session` fields.

## Security & Future Work Notes
- Never print the raw session JWT to stdout/stderr or logs; `auth status` prints only the
  derived, non-secret session fields returned by the backend.
- The CLI does not verify the token locally — signature/issuer/audience/expiry validation is
  the backend's responsibility (it holds the signing key). The CLI only renders the
  authoritative result.
- `role` / `permissions` are fixed placeholders echoed from the token; they will reflect real
  resolution once the backend implements it.
- Future enhancements (separate issues): a `--json` output mode and/or a distinct exit code
  for the not-authenticated state (for AI-agent scripting), and automatic token deletion +
  re-login prompting on an expired/invalid token, paired with the backend verification
  interceptor.
