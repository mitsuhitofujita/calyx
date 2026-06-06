# Plan — ISSUE-007: Persist and Use the CLI Session Token

## Context

`calyx auth login` currently ends the ISSUE-005 browser OAuth flow by printing the raw
Google ID token to stdout (`apps/cli/auth.go:92`) — a development-only stopgap. ISSUE-006 has
since added the backend `AuthService.Login` RPC, which exchanges that Google ID token for a
short-lived **Calyx session JWT**.

Per `docs/intent.md` and `docs/spec.md`, the CLI must (1) store that session token locally and
(2) attach it as gRPC metadata (`authorization: Bearer <jwt>`) on subsequent requests, so the
backend can later verify it. This issue implements the **CLI half** of the auth flow:

- Rewire `calyx auth login` to call `AuthService.Login`, persist the returned session token, and
  print a non-secret confirmation (never the JWT).
- Persist behind a small `TokenStore` abstraction with a **file backend** (interim mechanism;
  the OS keyring backend is a future issue, reserved behind a `CALYX_TOKEN_STORE` flag).
- Attach the stored token to outgoing RPCs (`sample hello`).

Backend enforcement (the verifying interceptor / re-auth-on-expiry flow) is **out of scope** —
this issue only *attaches* the token; the backend does not yet require it.

## Locked Decisions

From the issue's proposals plus two clarifications from the user:

1. **Storage path**: `<base>/calyx/session.json`, where `<base>` is `CALYX_CONFIG_DIR` if set,
   else `os.UserConfigDir()`. Directory `0700`, file `0600`. JSON:
   `{ "session_token": "<jwt>", "expires_at": "<RFC3339>" }`.
2. **Backend selector**: `CALYX_TOKEN_STORE` ∈ { `file` (default, implemented), `keyring`
   (reserved → "not implemented this phase" error), other → fail fast }.
3. **Test depth (user choice)**: full unit tests for the store **plus** an integration test that
   refactors `runHello` for dependency injection and asserts the outgoing `authorization: Bearer
   <jwt>` header via a bufconn `SampleService` stub.
4. **Bad store config behaviour (user choice)**: `sample hello` **hard-fails** (stderr + non-zero
   exit) on a misconfigured/unimplemented `CALYX_TOKEN_STORE` or a corrupt token file. Only a
   genuinely *missing* token (`ErrNoToken`) proceeds unauthenticated, with a hint to log in.

Minor defaults: validate the store selector *before* opening the browser (fail fast); keep the
backend's already-second-precision `expires_at` as-is; surface the raw wrapped gRPC error on
login failure (it already includes the status code, e.g. `Unauthenticated`).

---

## File-by-File Changes

All files are package `main` under `apps/cli/`. Module path:
`github.com/mitsuhitofujita/calyx`; proto alias `calyxv1` →
`.../shared/proto/mitsuhitofujita/calyx/v1`.

### 1. `apps/cli/store.go` — **new**

The token-storage abstraction, file backend, path resolution, and selector.

```go
// SessionToken is the persisted Calyx session credential.
type SessionToken struct {
    Token     string    `json:"session_token"`
    ExpiresAt time.Time `json:"expires_at"` // RFC3339 via encoding/json
}

// TokenStore abstracts session-token persistence so the file backend can later be
// swapped for an OS keyring backend without touching call sites.
type TokenStore interface {
    Save(tok SessionToken) error
    Load() (SessionToken, error) // ErrNoToken when nothing is stored
    Delete() error               // idempotent; nil when absent
}

// ErrNoToken distinguishes "not authenticated" from real I/O / decode failures.
var ErrNoToken = errors.New("no session token stored")
```

Helpers / factory:

- `sessionFilePath() (string, error)` — `base := os.Getenv("CALYX_CONFIG_DIR")`; if empty use
  `os.UserConfigDir()` (wrap its error); return `filepath.Join(base, "calyx", "session.json")`.
  The `calyx` subdir is always appended, including under `CALYX_CONFIG_DIR`.
- `NewTokenStore() (TokenStore, error)` — switch on `CALYX_TOKEN_STORE`:
  - `""`, `"file"` → `&fileStore{path: <sessionFilePath()>}`.
  - `"keyring"` → error: `keyring backend is not implemented in this phase; use "file"`.
  - default → error: `unknown CALYX_TOKEN_STORE %q: valid values are "file" (default) or "keyring"`.

File backend `type fileStore struct{ path string }`:

- `Save` — `os.MkdirAll(dir, 0o700)`; `json.MarshalIndent`; **atomic write**: `os.CreateTemp(dir,
  "session-*.json.tmp")`, `Chmod(0o600)`, write, `Close`, `os.Rename(tmp, path)`. On any error
  after temp creation, `os.Remove(tmp)` so no partial/garbage file is left. Temp must be in the
  **same dir** as the target so the rename is atomic.
- `Load` — `os.ReadFile`; if `errors.Is(err, fs.ErrNotExist)` → `SessionToken{}, ErrNoToken`;
  other read error → wrapped; `json.Unmarshal` decode error → wrapped `session file %s is corrupt`
  (a real error, **not** `ErrNoToken`).
- `Delete` — `os.Remove`; treat `fs.ErrNotExist` as success (idempotent); else return wrapped error.

Imports: `encoding/json`, `errors`, `fmt`, `io/fs`, `os`, `path/filepath`, `time`.

### 2. `apps/cli/auth.go` — **modify** `runAuthLogin`

Replace the dev-only tail (current lines ~47–94). New flow after building `*oauth2.Config`:

1. `store, err := NewTokenStore()` — **before** `authorize(...)`, so a bad `CALYX_TOKEN_STORE`
   fails fast without opening a browser.
2. `idToken, err := authorize(conf, redirectAddr)` (unchanged).
3. `sess, err := exchangeIDToken(idToken)` — new helper (below).
4. `store.Save(sess)` — wrap error as `failed to save session token: %w`.
5. `fmt.Printf("Logged in. Session valid until %s.\n", sess.ExpiresAt.UTC().Format(time.RFC3339))`.
   **Never** print `sess.Token`.

New helper:

```go
const backendCallTimeout = 10 * time.Second // dial+RPC budget, independent of loginTimeout (3m)

// exchangeIDToken dials the backend and exchanges a Google ID token for a Calyx
// session token via AuthService.Login.
func exchangeIDToken(idToken string) (SessionToken, error) {
    conn, err := dialBackend()        // shared helper from main.go
    if err != nil { return SessionToken{}, err }
    defer conn.Close()
    client := calyxv1.NewAuthServiceClient(conn)
    ctx, cancel := context.WithTimeout(context.Background(), backendCallTimeout)
    defer cancel()
    resp, err := client.Login(ctx, &calyxv1.LoginRequest{
        GoogleCredential: &calyxv1.LoginRequest_IdToken{IdToken: idToken},
    })
    if err != nil { return SessionToken{}, fmt.Errorf("backend login failed: %w", err) }
    return SessionToken{Token: resp.GetSessionToken(), ExpiresAt: resp.GetExpiresAt().AsTime()}, nil
}
```

Also: delete the "DEVELOPMENT-ONLY" doc/inline comments and update the `runAuthLogin` doc comment
to describe the new behaviour. New imports: `google.golang.org/grpc/...` are reused via
`dialBackend`; add `calyxv1` import. (`metadata` not needed here.)

### 3. `apps/cli/main.go` — **modify**

Add a shared dial helper and rewire `runHello` for DI + token attachment.

```go
// dialBackend opens an insecure (dev) gRPC client conn to the backend; caller closes it.
func dialBackend() (*grpc.ClientConn, error) {
    conn, err := grpc.NewClient(backendAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil { return nil, fmt.Errorf("failed to connect to backend: %w", err) }
    return conn, nil
}

// withAuth attaches "authorization: Bearer <jwt>" when a token is stored. A *missing*
// token (ErrNoToken) returns ctx unchanged + a login hint; a misconfigured store or a
// load failure returns an error so the caller can hard-fail (per locked decision 4).
func withAuth(ctx context.Context) (context.Context, error) {
    store, err := NewTokenStore()
    if err != nil { return nil, err }
    tok, err := store.Load()
    if errors.Is(err, ErrNoToken) {
        fmt.Fprintln(os.Stderr, "hint: not logged in; run `calyx auth login` to authenticate.")
        return ctx, nil
    }
    if err != nil { return nil, fmt.Errorf("could not load session token: %w", err) }
    return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok.Token), nil
}

// sayHello is the testable RPC unit: applies withAuth then calls Hello.
func sayHello(client calyxv1.SampleServiceClient, name string) (string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
    defer cancel()
    ctx, err := withAuth(ctx)
    if err != nil { return "", err }
    resp, err := client.Hello(ctx, &calyxv1.HelloRequest{Name: name})
    if err != nil { return "", fmt.Errorf("gRPC call failed: %w", err) }
    return resp.GetMessage(), nil
}
```

`runHello` becomes: validate args → `conn, err := dialBackend()` → `defer conn.Close()` →
`msg, err := sayHello(calyxv1.NewSampleServiceClient(conn), name)` → `fmt.Println(msg)`.

Add import `google.golang.org/grpc/metadata` (already a transitive grpc dep). Fix the stale
`flag.Usage` line so `auth login` reads `sign in with Google and store a session token`
(it no longer prints a token).

### 4. `apps/cli/store_test.go` — **new** (unit)

`t.Setenv("CALYX_CONFIG_DIR", t.TempDir())` per test for isolation. Table-driven where it reads
well. Cases:

| Test | Asserts |
|------|---------|
| `SaveLoadRoundTrip` | `Save` then `Load` returns equal `Token`; `ExpiresAt.Equal(...)` (use `.Equal`, build input UTC, second precision) |
| `FilePermissions0600` | `os.Stat(path).Mode().Perm() == 0o600` (`t.Skip` on `runtime.GOOS=="windows"`) |
| `DirPermissions0700` | dir perm `0o700` (same Windows guard) |
| `LoadNotFoundBeforeSave` | `errors.Is(err, ErrNoToken)` on a fresh store |
| `DeleteRemovesFile` | after `Save`+`Delete`, file gone and `Load` → `ErrNoToken` |
| `DeleteIdempotent` | `Delete` on a fresh store returns `nil` |
| `LoadCorruptFile` | invalid JSON at path → error that is **not** `ErrNoToken` |
| `SaveOverwrites` | second `Save` wins (atomic overwrite) |
| `NewTokenStore_Selector` (table) | `""`/`file` → `*fileStore`; `keyring` → err "not implemented"; bogus → err "unknown" |
| `SessionFilePath` | equals `filepath.Join(tmp, "calyx", "session.json")` |

### 5. `apps/cli/main_test.go` — **new** (integration, per locked decision 3)

bufconn pattern mirroring `apps/backend/internal/server/auth_test.go`:

- A stub `SampleServiceServer.Hello` that records `metadata.FromIncomingContext(ctx)` and returns
  a fixed `HelloResponse`.
- Helper spins up the stub on `bufconn`, returns a connected `SampleServiceClient`; cleanup via
  `t.Cleanup`.
- `TestSayHello_AttachesBearer`: `t.Setenv("CALYX_CONFIG_DIR", t.TempDir())`, `Save` a token via
  `fileStore`, call `sayHello(client, "Alice")`; assert returned message and captured metadata
  `authorization[0] == "Bearer <jwt>"`.
- `TestSayHello_NoToken`: no token saved; assert the call still succeeds and **no** `authorization`
  header is present (unauthenticated path).
- `TestSayHello_BadStoreConfig`: `t.Setenv("CALYX_TOKEN_STORE", "bogus")`; assert `sayHello`
  returns a non-nil error (hard-fail).

### 6. `.env.example` — **modify**

Append a non-secret documentation block:

```
# Optional: CLI session-token storage backend.
#   file    (default) — store the session JWT at <config-dir>/calyx/session.json
#   keyring (reserved) — OS credential store; not implemented in this phase
CALYX_TOKEN_STORE=file

# Optional: override the base config directory for token storage. When set, the
# session file is $CALYX_CONFIG_DIR/calyx/session.json. Useful for tests/CI.
# Defaults to the OS per-user config dir (XDG on Linux, %AppData% on Windows).
# CALYX_CONFIG_DIR=
```

### 7. Stale-reference cleanup — `justfile`

The `login` recipe comment tells users to obtain a token via
`just login "$(go run ./apps/cli auth login)"`, which assumed `auth login` prints the ID token to
stdout — no longer true. Update the comment only (keep the grpcurl recipe as a low-level probe):
note that `calyx auth login` now performs the exchange directly and this recipe is a manual
grpcurl alternative.

---

## Reuse Notes

- `backendAddr()` (`main.go:115`) — reused by `dialBackend`.
- `authorize(conf, redirectAddr)` (`auth.go:105`) — unchanged; still returns the Google ID token.
- bufconn test scaffolding pattern — copy from `apps/backend/internal/server/auth_test.go`.
- `metadata.AppendToOutgoingContext` chosen over `PerRPCCredentials`: the token is loaded once for
  a single RPC with no refresh in scope, and `PerRPCCredentials` would fight the current `insecure`
  transport (`RequireTransportSecurity`). A central unary interceptor is the right future refactor
  once more authenticated commands exist — not built now.

---

## Verification

1. **Build**: `just build` → `bin/calyx` and `bin/backend` compile.
2. **Unit + integration tests**: `just test` (i.e. `go test ./...`) — all store and metadata tests
   pass, including the bufconn Bearer-header assertion.
3. **Login stores a token** (backend up via `just run`, valid `.env` OAuth creds):
   `./bin/calyx auth login` → browser sign-in completes; stdout shows
   `Logged in. Session valid until <RFC3339>.` (no raw JWT); `session.json` exists at the resolved
   path with `0600` perms (dir `0700`); exit `0`.
4. **Subsequent command sends the token**: `./bin/calyx sample hello Alice` → prints
   `Hello, Alice.`; backend dev logging (or the integration test) confirms the
   `authorization: Bearer ...` header; exit `0`.
5. **Failure paths**:
   - Backend unreachable during `auth login` → clear stderr error, non-zero exit, **no** partial
     token file left behind.
   - `CALYX_TOKEN_STORE=keyring` or an unknown value → `auth login` and `sample hello` both
     fail fast with an actionable message (locked decision 4).
   - No token stored → `sample hello` proceeds unauthenticated and prints the login hint to stderr.

## Out of Scope (future issues)

- OS-native `keyring` backend (Windows Credential Manager / Linux Secret Service).
- Backend verifying interceptor and the re-auth-on-expiry / token-deletion flow.
- Automatic token refresh (excluded by `intent.md`).
