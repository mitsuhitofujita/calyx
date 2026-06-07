# Plan: Add the `calyx auth status` CLI command (ISSUE-009)

## Context

The CLI can already sign in (`calyx auth login`, ISSUE-007) and persist a session
token, and the backend already verifies a presented token via `AuthService.Status`
(ISSUE-008). What is missing is the user-facing command that answers **"Am I logged
in, and as whom?"**.

This change adds `calyx auth status`: it loads the locally stored session token,
asks the backend to verify it, and prints the result in human-readable form
(authenticated → name/email/role/permissions/expiry; not authenticated → reason +
re-login hint). The CLI never inspects the JWT itself — the backend is the sole
source of truth for a *present* token. The only exception is a *missing* local token,
which is reported directly without a network round-trip.

This is **CLI-only**. No proto or backend changes: `StatusRequest`,
`StatusResponse`, `SessionInfo`, and the `Status` RPC already exist
(`shared/proto/mitsuhitofujita/calyx/v1/auth.proto`, regenerated `auth_grpc.pb.go`).

## Existing code to reuse (do not re-invent)

- `runAuth` dispatch + `runAuthLogin` arg-validation/error pattern — `apps/cli/auth.go:40`, `apps/cli/auth.go:58`.
- `dialBackend()` (insecure dev transport) — `apps/cli/main.go:111`.
- `backendCallTimeout` (10s, bounded RPC timeout) — `apps/cli/auth.go:36`.
- Bearer-attach idiom `metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)` — `apps/cli/main.go:137`.
- Testable-RPC-unit pattern `sayHello` (build ctx → call RPC → return) — `apps/cli/main.go:141`.
- `errUsage` sentinel (handler prints usage, `main` exits 1 without re-printing) — `apps/cli/main.go:27`.
- Token store: `NewTokenStore()`, `TokenStore.Load()`, `ErrNoToken`, `SessionToken.Token` — `apps/cli/store.go:48`, `apps/cli/store.go:28`, `apps/cli/store.go:14`.
- Generated client `calyxv1.NewAuthServiceClient` + `client.Status(ctx, &calyxv1.StatusRequest{})` — `auth_grpc.pb.go:43`, `auth_grpc.pb.go:57`.
- Response getters: `GetAuthenticated()`, `GetMessage()`, `GetSession()`; `SessionInfo` getters `GetName/GetEmail/GetRole/GetPermissions/GetExpiresAt` — confirmed in `auth.pb.go`.
- Test harness `recordingSampleServer` + `newTestSampleClient` (bufconn stub that records incoming metadata) — `apps/cli/main_test.go:18`, `apps/cli/main_test.go:32`. Mirror this for `AuthService.Status`.

Backend message strings (`apps/backend/internal/server/auth.go:139`) that the
renderer will display verbatim and tests may assert on:
`"session is valid"`, `"no session token provided"`,
`"session token is invalid or expired"`. The local no-token short-circuit uses
`"not logged in"`.

## Implementation

### 1. `apps/cli/auth.go` — dispatch + new handler

In `runAuth`, add a case alongside `login`:

```go
case "status":
    return runAuthStatus(args[1:])
```

Update the no-subcommand usage line and the `default` to reflect the new command,
e.g. `usage: calyx auth <login|status>`.

Add `runAuthStatus(args []string) error` with this control flow (mirrors
`runAuthLogin` for validation/store-resolution and `runHello` for dial+call):

1. Reject extra args: `if len(args) != 0 { fmt.Fprintln(os.Stderr, "usage: calyx auth status"); return errUsage }`.
2. `store, err := NewTokenStore()` — return `err` on a misconfigured/unknown
   `CALYX_TOKEN_STORE` (fail fast, no browser/network).
3. `tok, err := store.Load()`:
   - `errors.Is(err, ErrNoToken)` → render local not-authenticated and return `nil`
     (no backend call):
     `fmt.Println(formatStatus(&calyxv1.StatusResponse{Authenticated: false, Message: "not logged in"}))`.
   - other `err` → `return fmt.Errorf("could not load session token: %w", err)`.
4. `conn, err := dialBackend()` → return `err`; `defer conn.Close()`.
5. `resp, err := fetchStatus(calyxv1.NewAuthServiceClient(conn), tok.Token)` → return `err`.
6. `fmt.Println(formatStatus(resp))`; `return nil`.

Add the testable RPC unit (do **not** reuse `withAuth` — the store load /
`ErrNoToken` decision is made in `runAuthStatus`, and here the token is already in
hand):

```go
// fetchStatus attaches the session token as Bearer metadata and calls Status.
func fetchStatus(client calyxv1.AuthServiceClient, token string) (*calyxv1.StatusResponse, error) {
    ctx, cancel := context.WithTimeout(context.Background(), backendCallTimeout)
    defer cancel()
    ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
    resp, err := client.Status(ctx, &calyxv1.StatusRequest{})
    if err != nil {
        return nil, fmt.Errorf("backend status check failed: %w", err)
    }
    return resp, nil
}
```

Add the pure renderer (no I/O, returns a string; the caller `Println`s it):

```go
// formatStatus renders a StatusResponse as plain text for `calyx auth status`.
func formatStatus(resp *calyxv1.StatusResponse) string {
    if !resp.GetAuthenticated() {
        return fmt.Sprintf(
            "Status:  not authenticated (%s)\nRun `calyx auth login` to authenticate.",
            resp.GetMessage())
    }
    s := resp.GetSession()
    var b strings.Builder
    fmt.Fprintln(&b, "Status:      authenticated")
    fmt.Fprintf(&b, "Name:        %s\n", s.GetName())
    if s.GetEmail() != "" {
        fmt.Fprintf(&b, "Email:       %s\n", s.GetEmail())
    }
    fmt.Fprintf(&b, "Role:        %s\n", s.GetRole())
    fmt.Fprintf(&b, "Permissions: %s\n", strings.Join(s.GetPermissions(), ", "))
    fmt.Fprintf(&b, "Expires:     %s", s.GetExpiresAt().AsTime().UTC().Format(time.RFC3339))
    return b.String()
}
```

The expiry is the last line and intentionally has **no** trailing newline, since
`runAuthStatus` prints with `fmt.Println`. `GetExpiresAt().AsTime()` is safe on a
nil timestamp (returns the zero time), but the authenticated branch only runs when
the backend populated `session`.

New imports in `auth.go`: `strings`, `google.golang.org/grpc/metadata` (`context`,
`fmt`, `os`, `time`, and `calyxv1` are already imported).

### 2. `apps/cli/main.go` — help text

Add one line to the `flag.Usage` Commands block (after the `auth login` line,
`apps/cli/main.go:37`):

```go
fmt.Fprintf(os.Stderr, "  auth status           show the current session status\n")
```

No change to `dialBackend` / `dispatch`.

### 3. `apps/cli/auth_test.go` (new file, package `main`)

Add a bufconn `AuthService.Status` stub mirroring `recordingSampleServer` /
`newTestSampleClient` from `main_test.go` (record incoming metadata; return a
configurable `*calyxv1.StatusResponse`). Suggested shape:

```go
type recordingAuthServer struct {
    calyxv1.UnimplementedAuthServiceServer
    gotMD metadata.MD
    resp  *calyxv1.StatusResponse
}
func (s *recordingAuthServer) Status(ctx context.Context, _ *calyxv1.StatusRequest) (*calyxv1.StatusResponse, error) {
    s.gotMD, _ = metadata.FromIncomingContext(ctx)
    return s.resp, nil
}
// newTestAuthClient(t, resp) → (calyxv1.AuthServiceClient, *recordingAuthServer)
```

Test cases (use `t.Setenv("CALYX_CONFIG_DIR", t.TempDir())` to isolate storage):

- **Authenticated render** — stub returns `authenticated:true, message:"session is valid",
  session:{Name, Email, Role:"admin", Permissions:["*"], ExpiresAt: timestamppb.New(...)}`.
  Call `fetchStatus(client, jwt)`; assert the stub recorded `authorization: Bearer <jwt>`
  (same assertion style as `TestSayHello_AttachesBearer`), then assert `formatStatus(resp)`
  contains the name, `Role:`, the joined permissions, and the RFC3339 expiry string.
- **Not-authenticated render** — `formatStatus(&calyxv1.StatusResponse{Authenticated:false,
  Message:"session token is invalid or expired"})` contains `"not authenticated"`, the
  message, and `"calyx auth login"`. (Pure function; no network.)
- **No-token short-circuit** — temp config dir, no token saved; `runAuthStatus(nil)`
  returns `nil`. (The early `ErrNoToken` return happens before `dialBackend`, so no backend
  is needed; rendering of this path is already covered by the not-authenticated render test
  on a synthesized `Message:"not logged in"` response.)
- **Misconfigured store hard-fail** — `t.Setenv("CALYX_TOKEN_STORE", "bogus")`;
  `runAuthStatus(nil)` returns a non-nil error (mirror `TestSayHello_BadStoreConfig`).
- **Argument validation** — `runAuthStatus([]string{"extra"})` returns `errUsage`
  (`errors.Is`).

Imports for the new test file mirror `main_test.go` (`context`, `net`, `testing`,
the grpc/bufconn/metadata packages, `timestamppb`, `time`, `errors`, `strings`, and
`calyxv1`).

## Output format (reference)

Authenticated:
```
Status:      authenticated
Name:        Alice Example
Email:       alice@example.com
Role:        admin
Permissions: *
Expires:     2026-06-07T12:34:56Z
```
(Email line omitted when empty.)

Not authenticated (backend-reported or local no-token):
```
Status:  not authenticated (session token is invalid or expired)
Run `calyx auth login` to authenticate.
```

## Exit-code semantics (unchanged CLI error model)

| Situation | Backend called | Exit |
| --- | --- | --- |
| Valid token / backend says invalid-or-expired / no local token | yes / yes / no | `0` |
| Misconfigured store, token load error, backend unreachable, gRPC `Internal` | no / no / attempted | `1` (`Error: ...` on stderr) |
| Extra arguments | no | `1` (usage on stderr, via `errUsage`) |

## Verification

1. **Build**: `just build` → `bin/calyx` and `bin/backend` compile.
2. **Tests**: `just test` (`go test ./...`) → all pass, including the new
   `apps/cli/auth_test.go` cases.
3. **Manual smoke (optional)** — backend up (`just run`), token obtained via
   `calyx auth login`:
   - `./bin/calyx auth status` → `authenticated` with name/role/permissions/expiry; exit `0`.
   - `CALYX_CONFIG_DIR="$(mktemp -d)" ./bin/calyx auth status` → `not authenticated`
     ("not logged in") + re-login hint; exit `0`.
   - Cross-check the backend directly:
     `grpcurl -plaintext -H "authorization: Bearer <jwt>" localhost:50051 mitsuhitofujita.calyx.v1.AuthService/Status`.

## Out of scope (future issues, per ISSUE-009)

Backend verification interceptor for other RPCs; automatic deletion of an
expired/invalid token + re-login prompting; `--json` output and a distinct
not-authenticated exit code; the keyring storage backend; real role/permission
resolution (still placeholder `admin` / `["*"]`).
