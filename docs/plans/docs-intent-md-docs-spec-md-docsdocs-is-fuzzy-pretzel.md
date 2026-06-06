# Plan: ISSUE-006 — Backend `AuthService.Login` RPC (Google token → Calyx session JWT)

## Context

`calyx` is a template repo for Go CLIs that collaborate with AI agents
(`docs/intent.md`). Authentication is split across two halves: the CLI obtains a
Google credential via a browser flow (ISSUE-005, **done** — see `apps/cli/auth.go`),
and the backend exchanges that credential for a short-lived **Calyx session JWT**
(`docs/spec.md` §"Authentication Flow", steps 3–5).

This plan implements the **backend half only**: a new `AuthService.Login` RPC in
`apps/backend` that verifies a Google **ID token** against Google's public keys and
mints an HS256-signed session JWT (with fixed placeholder `role`/`permissions`),
returning the token and its expiry. Persisting/attaching the token in the CLI is
ISSUE-007 (out of scope here). Enforcing the token on other RPCs via an interceptor
is also a later issue.

## Current state (verified)

- gRPC pipeline works end-to-end: `shared/proto/.../sample.proto` →
  `just generate` (buf lint + `buf generate` + `go mod tidy`) → committed `*.pb.go` →
  `SampleServer` in `apps/backend/internal/server/sample.go`, registered in
  `apps/backend/main.go` with `reflection.Register`.
- Tests use in-memory `bufconn` (`apps/backend/internal/server/sample_test.go`) — the
  pattern `auth_test.go` will follow.
- `apps/cli/auth.go` already produces a Google **ID token** (scopes `openid email
  profile`) and reads `.env` via `godotenv.Load()`. The backend `main.go` does **not**
  yet load `.env`; this plan adds that for config parity with the CLI.
- `go.mod` has grpc/protobuf/oauth2/godotenv. Two new deps are needed:
  `github.com/golang-jwt/jwt/v5` and `google.golang.org/api/idtoken`.
- Proto package is `mitsuhitofujita.calyx.v1`; `go_package` ends in `;calyxv1`. buf
  STANDARD lint is satisfied by the `Login`/`LoginRequest`/`LoginResponse` naming.

## Design decisions (locked from the issue, plus two minor local choices)

1. **Verifier injected behind an interface** so unit tests never hit Google's network.
   Production impl wraps `idtoken.Validate(ctx, rawIDToken, clientID)`.
2. **Session JWT = HS256** signed with `CALYX_JWT_SIGNING_KEY`; same backend issues and
   verifies → symmetric key satisfies spec's stateless verification.
3. **Only the `id_token` branch is implemented.** `auth_code` → `codes.Unimplemented`.
   *(Local choice)* **empty request** (neither variant) → `codes.InvalidArgument`.
4. **Fixed placeholders**: every authenticated user gets `role="admin"`,
   `permissions=["*"]`.
5. `LoginResponse.expires_at` MUST equal the JWT `exp` claim — both derived from one
   `now+TTL` value at second precision.

## Files to change

| File | Action | Purpose |
| --- | --- | --- |
| `shared/proto/mitsuhitofujita/calyx/v1/auth.proto` | **Add** | Service + message defs (verbatim from issue). |
| `shared/proto/.../auth.pb.go`, `auth_grpc.pb.go` | **Generated** | Produced by `just generate`; committed. |
| `apps/backend/internal/server/auth.go` | **Add** | `AuthServer`, `AuthConfig`, verifier interface, JWT issuance. |
| `apps/backend/internal/server/auth_test.go` | **Add** | bufconn + stub-verifier tests. |
| `apps/backend/main.go` | **Modify** | Load `.env`, read/validate new config, register `AuthServer`. |
| `.env.example` | **Modify** | Document the new JWT/session keys. |
| `go.mod` / `go.sum` | **Modify** | Add `golang-jwt/jwt/v5`, `api/idtoken` (via `go mod tidy`). |

## Implementation steps

### 1. Proto definition + generation
- Create `shared/proto/mitsuhitofujita/calyx/v1/auth.proto` exactly as specified in the
  issue: `package mitsuhitofujita.calyx.v1`, `import "google/protobuf/timestamp.proto"`,
  `go_package … ;calyxv1`, `service AuthService { rpc Login(LoginRequest) returns
  (LoginResponse); }`, `LoginRequest` with `oneof google_credential { string id_token =
  1; string auth_code = 2; }`, `LoginResponse { string session_token = 1;
  google.protobuf.Timestamp expires_at = 2; }`.
- Run `just generate` → produces `auth.pb.go` / `auth_grpc.pb.go` (buf bundles the
  timestamp WKT; no extra wiring), then `go mod tidy`.

### 2. `AuthServer` — `apps/backend/internal/server/auth.go`
Mirror the `SampleServer` style. Sketch:

```go
type AuthConfig struct {
    GoogleClientID string        // expected audience for the Google ID token
    SigningKey     []byte        // HMAC secret for the session JWT
    Issuer         string        // iss claim (default "calyx-backend")
    Audience       string        // aud claim (default "calyx-cli")
    TTL            time.Duration // session lifetime (default 1h)
}

// googleUser holds the verified fields extracted from a Google ID token.
type googleUser struct{ Subject, Email, Name string }

// googleIDTokenVerifier is injected so tests don't call Google's endpoints.
type googleIDTokenVerifier interface {
    Verify(ctx context.Context, rawIDToken string) (googleUser, error)
}

type AuthServer struct {
    calyxv1.UnimplementedAuthServiceServer
    cfg      AuthConfig
    verifier googleIDTokenVerifier
    now      func() time.Time // injectable clock; defaults to time.Now
}

// NewAuthServer wires the production Google verifier (audience = cfg.GoogleClientID).
func NewAuthServer(cfg AuthConfig) *AuthServer {
    return newAuthServer(cfg, googleVerifier{audience: cfg.GoogleClientID})
}
// newAuthServer is the test-friendly constructor (stub verifier / clock).
func newAuthServer(cfg AuthConfig, v googleIDTokenVerifier) *AuthServer { … }
```

- Custom JWT claims struct embeds `jwt.RegisteredClaims` (covers `iss`/`aud`/`sub`/
  `iat`/`exp`) plus `Email`, `Name`, `Role`, `Permissions []string`.
- `Login(ctx, req)`:
  - `switch req.GetGoogleCredential().(type)` (or check `GetAuthCode()`/`GetIdToken()`):
    - `*calyxv1.LoginRequest_AuthCode` → `status.Error(codes.Unimplemented, …)`.
    - empty → `status.Error(codes.InvalidArgument, "no google credential provided")`.
    - `*…_IdToken` → `verifier.Verify(ctx, idToken)`; on error
      `status.Error(codes.Unauthenticated, …)` (do **not** leak the token in the message).
  - Compute `now := s.now()`, `exp := now.Add(s.cfg.TTL)`; build claims with fixed
    `role="admin"`, `permissions=["*"]`; sign via
    `jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.cfg.SigningKey)`.
  - Return `&calyxv1.LoginResponse{SessionToken: signed, ExpiresAt: timestamppb.New(exp)}`.
- Production verifier:

```go
type googleVerifier struct{ audience string }
func (v googleVerifier) Verify(ctx context.Context, raw string) (googleUser, error) {
    p, err := idtoken.Validate(ctx, raw, v.audience) // checks sig/iss/aud/exp + caches keys
    if err != nil { return googleUser{}, err }
    email, _ := p.Claims["email"].(string)
    name, _ := p.Claims["name"].(string)
    return googleUser{Subject: p.Subject, Email: email, Name: name}, nil
}
```

### 3. Config + registration — `apps/backend/main.go`
- Add `_ = godotenv.Load()` at the top of `main` (parity with the CLI; existing process
  env wins).
- Add `loadAuthConfig() (server.AuthConfig, error)`:
  - required `CALYX_GOOGLE_CLIENT_ID`, `CALYX_JWT_SIGNING_KEY` → fail fast with an
    actionable error if empty.
  - `CALYX_JWT_ISSUER` default `calyx-backend`; `CALYX_JWT_AUDIENCE` default `calyx-cli`.
  - `CALYX_SESSION_TTL` default `1h` via `time.ParseDuration` (error on bad value).
- After `RegisterSampleServiceServer`, add
  `calyxv1.RegisterAuthServiceServer(grpcServer, server.NewAuthServer(cfg))`.
- On config error: `log.Fatalf` before listening (don't start a half-configured server).

### 4. `.env.example`
Append the new keys with placeholders and short comments:
`CALYX_JWT_SIGNING_KEY=change-me-dev-only`, and optional
`CALYX_JWT_ISSUER=calyx-backend`, `CALYX_JWT_AUDIENCE=calyx-cli`,
`CALYX_SESSION_TTL=1h`. (`CALYX_GOOGLE_CLIENT_ID` already exists from ISSUE-005 and is
reused as the verification audience.)

### 5. Tests — `apps/backend/internal/server/auth_test.go`
Use a `bufconn` client (copy the `newTestClient` helper, register `AuthServer` built
via `newAuthServer` with a **stub verifier** and a fixed clock). Cover:
- **Happy path**: stub returns known `sub`/`email`/`name` → non-empty `session_token`;
  `expires_at ≈ now+TTL`; parse JWT with the signing key → `role="admin"`,
  `permissions=["*"]`, matching `email`/`sub`.
- **Invalid Google token**: stub returns an error → `codes.Unauthenticated`, no token.
- **Auth-code branch**: `auth_code` set → `codes.Unimplemented`.
- **Empty request**: neither variant → `codes.InvalidArgument`.
- **Expiry consistency**: parsed JWT `exp` Unix == `resp.ExpiresAt.AsTime().Unix()`.
Assert codes with `status.Code(err)`.

## Verification (end-to-end)

```bash
just generate   # auth.pb.go / auth_grpc.pb.go appear; buf lint passes; go mod tidy adds deps
just build      # bin/backend and bin/calyx compile
just test       # auth_test.go passes (all five cases) alongside sample_test.go
```

Optional manual smoke test (needs real OAuth `.env` + `CALYX_JWT_SIGNING_KEY`):
```bash
just run   # in one shell
# grab a Google ID token (ISSUE-005 dev flow) and:
grpcurl -plaintext -d '{"id_token":"<google-id-token>"}' \
  localhost:50051 mitsuhitofujita.calyx.v1.AuthService/Login
# Expected: JSON with sessionToken + expiresAt. auth_code variant → Unimplemented.
```

## Notes / guardrails
- Never log the Google token or the session JWT; keep `CALYX_JWT_SIGNING_KEY` out of VCS
  (`.env` stays git-ignored — only `.env.example` is committed).
- `role`/`permissions` are placeholders; the verification interceptor and real
  role resolution are deferred to later issues. ISSUE-007 consumes this RPC.
