# ISSUE-007: Persist the Session Token in the CLI and Use It on Subsequent Requests

## Status
- **Status**: Open / In Development
- **Priority**: High (CLI half of the authentication flow in spec.md)
- **Assignee**: AI Agent / Developer
- **Depends on**: ISSUE-005 (browser Google sign-in), ISSUE-006 (`AuthService.Login` RPC)

## Objective
Rewrite the CLI so that `calyx auth login`:

1. Completes the browser-based Google sign-in (already implemented in ISSUE-005),
2. Sends the obtained Google credential to the backend `AuthService.Login` RPC (ISSUE-006),
3. **Stores the returned Calyx session token (JWT)** locally, and
4. **Attaches that session token to subsequent commands** so they authenticate
   automatically.

This replaces ISSUE-005's development-only behavior of printing the Google ID token to
stdout.

## Background
Per `intent.md` and `spec.md`, after the backend issues a short-lived session JWT, the CLI
must save it and send it as gRPC metadata on later requests. The long-term plan is to store
the token in the OS-native credential store (Windows Credential Manager / Linux Secret
Service via libsecret), with a **file-based fallback** for headless/development
environments.

For the **current phase**, file-based storage is the interim mechanism. As noted in the
development plan, the file backend may also be **retained going forward behind a feature
flag**, so it should be implemented behind a small storage abstraction rather than inlined.

## Proposals (Requested Decisions)
The development notes asked for proposals on the storage path and the feature flag. The
following are the proposed defaults for this issue.

### Proposal 1 — Session token storage path
Store the token under the OS-appropriate per-user config directory returned by Go's
`os.UserConfigDir()`, in a `calyx` subdirectory:

- File: `<os.UserConfigDir()>/calyx/session.json`
  - Linux / devcontainer: `${XDG_CONFIG_HOME:-~/.config}/calyx/session.json`
  - Windows 11: `%AppData%\calyx\session.json`
- Permissions: create the directory `0700` and the file `0600` (owner-only).
- `CALYX_CONFIG_DIR` (optional): overrides the base directory (useful for tests and CI).

**Rationale**: `os.UserConfigDir()` is the portable, modern convention (XDG on Linux,
`%AppData%` on Windows — both client OSes in `spec.md`), needs no hardcoded home-relative
path, and keeps Calyx state isolated in its own directory. It does not collide with the
future OS-credential-store backend, which uses no file at all.

### Proposal 2 — Storage backend feature flag
Select the storage backend with an environment variable:

- `CALYX_TOKEN_STORE` = `file` (default for this phase) | `keyring` (future OS credential
  store).

The flag is read once and resolves to a `TokenStore` implementation. The `file` backend is
implemented now; `keyring` is reserved for a future issue. This satisfies "the file backend
may be kept via a feature flag" while leaving room for the credential-store backend without
touching call sites.

## Scope

### In Scope
- A `TokenStore` abstraction with `Save`, `Load`, and `Delete`, plus a `file` backend that
  reads/writes `session.json` at the path above with `0700`/`0600` permissions.
- A `CALYX_TOKEN_STORE` feature flag selecting the backend (only `file` implemented now).
- Rewiring `calyx auth login` to: run the existing OAuth flow, call `AuthService.Login`
  with the Google credential, and persist the returned session token + expiry — instead of
  printing the Google ID token.
- Loading the stored session token and attaching it as gRPC metadata
  (`authorization: Bearer <session_token>`) on subsequent commands (e.g. `sample hello`).
- A friendly success message on login (e.g. signed-in email and expiry), with no secret
  token printed to stdout.

### Out of Scope (Future Issues)
- The OS-native credential-store (`keyring`) backend (Windows Credential Manager / Linux
  Secret Service). The flag value is reserved but unimplemented.
- Backend enforcement: the gRPC interceptor that verifies the session token and the
  re-authentication-on-expiry / token-deletion flow described in `spec.md`. This issue
  *attaches* the token; the backend does not yet require it (see ISSUE-006 Out of Scope).
- Automatic token refresh (excluded by `intent.md`).

## Technical Specifications

### `calyx auth login` (rewritten flow)
1. Run the existing browser OAuth flow to obtain the Google ID token (current
   `authorize(...)` in `apps/cli/auth.go`).
2. Dial the backend (reuse `backendAddr()` / `CALYX_BACKEND_ADDR`, default
   `localhost:50051`, insecure transport as today) and call `AuthService.Login` with the
   ID token (`id_token` variant).
3. On success, persist the returned `session_token` and `expires_at` via the selected
   `TokenStore`.
4. Print a concise, non-secret confirmation to stdout, e.g.
   `Logged in. Session valid until 2026-06-06T12:34:56Z.` Do **not** print the session JWT.
5. On failure (OAuth error, backend unreachable, `UNAUTHENTICATED`, write error), print a
   clear message to stderr and exit non-zero.

> The ISSUE-005 development-only `fmt.Println(idToken)` is removed by this change.

### Token Storage
- `TokenStore` interface:
  ```go
  type TokenStore interface {
      Save(tok SessionToken) error
      Load() (SessionToken, error) // distinguishable "not found"
      Delete() error
  }
  ```
  where `SessionToken` holds at least the JWT string and its expiry.
- `file` backend: marshal to `session.json`, e.g.
  ```json
  { "session_token": "<jwt>", "expires_at": "2026-06-06T12:34:56Z" }
  ```
  Create the parent directory `0700`; write the file `0600`. `Load` must return a clear
  "not authenticated" signal when the file is absent.
- Backend selection: read `CALYX_TOKEN_STORE` (default `file`); unknown values fail fast
  with an actionable error; `keyring` returns a "not implemented in this phase" error.

### Using the Token on Subsequent Commands
- Before issuing an authenticated RPC (e.g. `sample hello`), `Load()` the session token.
- Attach it as gRPC metadata using a Bearer scheme, e.g. via
  `metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)` (or per-RPC
  credentials).
- If no token is stored, the command may proceed unauthenticated for now (the backend does
  not yet enforce it). Optionally print a hint to run `calyx auth login`. Do not hard-fail
  on a missing token in this phase, since enforcement is a later issue.

### Configuration (Environment Variables)
- `CALYX_TOKEN_STORE` (optional, default `file`): storage backend selector.
- `CALYX_CONFIG_DIR` (optional): override the base config directory for token storage.
- Existing: `CALYX_BACKEND_ADDR`, plus the ISSUE-005 OAuth keys
  (`CALYX_GOOGLE_CLIENT_ID`, `CALYX_GOOGLE_CLIENT_SECRET`, `CALYX_OAUTH_REDIRECT_ADDR`).

Document any new keys in `.env.example`.

## Directory and File Mapping
- `apps/cli/auth.go` (Modify): after the OAuth flow, call `AuthService.Login` and persist
  the result instead of printing the ID token; emit a non-secret success message.
- `apps/cli/store.go` (Add, suggested): the `TokenStore` interface, `SessionToken` type,
  the `file` backend, path resolution (`os.UserConfigDir()` + `CALYX_CONFIG_DIR`), and the
  `CALYX_TOKEN_STORE` selector.
- `apps/cli/main.go` (Modify): in `runHello` (and future authenticated commands), load the
  stored token and attach it as gRPC metadata.
- `apps/cli/store_test.go` (Add): tests for the file backend and selector.
- `.env.example` (Modify): document `CALYX_TOKEN_STORE` / `CALYX_CONFIG_DIR` if added.

## Implementation Steps

### Step 1: Token Storage Layer
1. Add `SessionToken` and the `TokenStore` interface in `apps/cli/store.go`.
2. Implement the `file` backend (path via `os.UserConfigDir()`/`CALYX_CONFIG_DIR`,
   `0700` dir / `0600` file, JSON format, clear not-found behavior).
3. Add the `CALYX_TOKEN_STORE` selector (default `file`; `keyring` → not-implemented).

### Step 2: Rewire `auth login`
1. Keep the existing OAuth flow; capture the Google ID token.
2. Dial the backend and call `AuthService.Login`.
3. Persist `session_token` + `expires_at` via the selected store.
4. Replace the development-only token print with a non-secret success message; map errors
   to clear stderr output and non-zero exit codes.

### Step 3: Attach the Token to Subsequent Commands
1. In `runHello`, `Load()` the token and append it as `authorization: Bearer <jwt>`
   metadata on the outgoing context.
2. If no token is stored, proceed unauthenticated (optionally print a hint).

### Step 4: Documentation
1. Update `.env.example` and the CLI usage/help text to reflect that `auth login` now
   stores a session token (and no longer prints a raw token).

## Verification and Testing Plan

### 1. Build
```bash
just build
```
Verify `bin/calyx` builds.

### 2. Storage Unit Tests
```bash
just test
```
With `CALYX_CONFIG_DIR` pointed at a temp directory, assert that `Save` then `Load`
round-trips the token, the file is `0600` and its directory `0700`, `Load` reports
not-found before any `Save`, and `Delete` removes it. Assert `CALYX_TOKEN_STORE=keyring`
returns a not-implemented error and an unknown value fails fast.

### 3. Login Stores a Token
With the backend running (`just run`) and valid ISSUE-005 `.env` OAuth credentials:
```bash
./bin/calyx auth login
```
**Expected**: browser sign-in completes; a non-secret confirmation prints to stdout (no raw
JWT); `session.json` exists at the resolved path with `0600` permissions; exit code `0`.

### 4. Subsequent Command Sends the Token
```bash
./bin/calyx sample hello Alice
```
**Expected**: `Hello, Alice.` is printed. Confirm the request carried an
`authorization: Bearer ...` header (e.g. via backend logging during development, or a
metadata assertion in an integration test). Exit code `0`.

### 5. Failure Paths
- Backend unreachable during `auth login` → clear stderr error, non-zero exit, and no
  partial/garbage token file left behind.
- `CALYX_TOKEN_STORE` set to an unknown value → fail fast with an actionable error.

## Security & Future Work Notes
- File-based storage applies no proprietary encryption (consistent with `intent.md`:
  storing a key on the same filesystem would not improve security). Owner-only `0600`/`0700`
  permissions are the protection in this phase.
- Never print the session JWT to stdout/stderr or logs.
- A follow-up will add the `keyring` backend (Windows Credential Manager / Linux Secret
  Service) selectable via `CALYX_TOKEN_STORE`, and the re-authentication-on-expiry flow
  (delete the stored token on an auth error and prompt the user to run `calyx auth login`),
  paired with the backend verification interceptor from a future issue.
