# Plan: ISSUE-008 — Backend `AuthService.Status` RPC

## Context

`spec.md` defines a two-phase auth flow: the backend issues a short-lived session JWT
(`AuthService.Login`, ISSUE-006), the CLI stores it and attaches it as
`authorization: Bearer <jwt>` gRPC metadata on every call (ISSUE-007), and the backend
verifies it statelessly. So far the backend only *issues* tokens; it never *verifies* one it
previously issued.

This change adds a single `AuthService.Status` RPC — a `whoami` endpoint — that is the first
place the backend verifies a presented session token. It reports auth state **in the response
body** (it does not fail with `UNAUTHENTICATED` for a missing/invalid token), so a future
`calyx auth status` CLI command can print a clear message. The verification logic is extracted
into a reusable helper (`verifySessionToken`) so the future gRPC interceptor (out of scope
here) can share one implementation.

**Backend only.** No CLI command, no interceptor, no real role/permission resolution, no
refresh tokens.

## Existing code to reuse (do not reinvent)

- `apps/backend/internal/server/auth.go`
  - `AuthServer` struct with `cfg AuthConfig` and injectable `now func() time.Time`.
  - `AuthConfig.SigningKey` / `.Issuer` / `.Audience` — the same secret/claims used to sign in
    `Login`; reuse them to verify.
  - `sessionClaims` struct (`jwt.RegisteredClaims` + `Email`, `Name`, `Role`, `Permissions`).
  - `Login`/`loginWithIDToken` show the HS256 signing pattern to mirror for verification, and
    the `exp.Truncate(time.Second)` / `timestamppb.New` mapping to mirror for `expires_at`.
- `apps/backend/internal/server/auth_test.go`
  - bufconn harness `newTestAuthClient(t, cfg, v)` (sets server clock to `testNow`).
  - `testSigningKey`, `testNow` (`2026-06-06 12:00 UTC`), `testUser`, `testAuthConfig()`.
  - `parseSessionClaims` shows the jwt parse + keyfunc pattern.
- `apps/cli/main.go:137` — confirms the wire contract: metadata key `authorization`, value
  `Bearer <jwt>`. The backend must read exactly this key and strip `Bearer ` case-insensitively.
- Tooling: `just generate` (`buf lint` + `buf generate` + `go mod tidy`), `just build`,
  `just test`. buf lint = STANDARD; `StatusRequest`/`StatusResponse` satisfy the standard
  request/response naming rule.
- Deps already present: `github.com/golang-jwt/jwt/v5 v5.3.1`, `google.golang.org/grpc`
  (provides `.../grpc/metadata`). No new modules.

## Files to change

1. `shared/proto/mitsuhitofujita/calyx/v1/auth.proto` (modify) — add RPC + messages.
2. `shared/proto/.../auth.pb.go`, `.../auth_grpc.pb.go` (regenerated — never hand-edit).
3. `apps/backend/internal/server/auth.go` (modify) — `verifySessionToken` helper + `Status` handler.
4. `apps/backend/internal/server/auth_test.go` (modify) — `Status` tests.
5. `apps/backend/main.go` — **no change**; `AuthService` is already registered, so `Status`
   is served automatically.

## Step 1 — Extend the proto and regenerate

In `auth.proto`, add the `Status` RPC to `AuthService` (keep `Login` unchanged) and append the
new messages. `google/protobuf/timestamp.proto` is already imported.

```protobuf
service AuthService {
  rpc Login(LoginRequest) returns (LoginResponse);

  // Status verifies the session token presented in the request metadata and
  // reports whether the session is valid, along with its details when it is.
  rpc Status(StatusRequest) returns (StatusResponse);
}

// StatusRequest is intentionally empty: the session token is read from the
// "authorization: Bearer <session_token>" gRPC metadata header.
message StatusRequest {}

message StatusResponse {
  bool authenticated = 1;          // valid signed/unexpired/scoped token presented?
  string message = 2;              // confirmation on success; reason on failure
  SessionInfo session = 3;         // populated only when authenticated == true
}

// SessionInfo carries the verified session's basic details.
message SessionInfo {
  string name = 1;                          // JWT "name"
  string email = 2;                         // JWT "email"
  string role = 3;                          // JWT "role" (fixed "admin" for now)
  repeated string permissions = 4;          // JWT "permissions" (fixed ["*"] for now)
  google.protobuf.Timestamp expires_at = 5; // mirrors JWT "exp"
}
```

Run `just generate`. Confirm `auth.pb.go`/`auth_grpc.pb.go` regenerate with
`StatusRequest`/`StatusResponse`/`SessionInfo` types and a `Status` method on the server
interface; the embedded `UnimplementedAuthServiceServer` keeps things compiling until the
handler is added.

## Step 2 — Add the reusable verification helper

In `auth.go`, add a method on `AuthServer`. Mirror `Login`'s HS256 choice; validate
method/signature/issuer/audience/expiry against the existing `cfg`. Evaluate `exp` against the
**injectable clock** (`s.now`) via `jwt.WithTimeFunc` so verification stays consistent with
`Login` and tests remain deterministic.

```go
// verifySessionToken parses and validates a Calyx session JWT (HS256 signature,
// issuer, audience, and expiry) using the same AuthConfig that Login signs with.
// It returns the parsed claims on success. Intended for reuse by the future
// verification interceptor.
func (s *AuthServer) verifySessionToken(raw string) (*sessionClaims, error) {
	var claims sessionClaims
	_, err := jwt.ParseWithClaims(raw, &claims,
		func(*jwt.Token) (any, error) { return s.cfg.SigningKey, nil },
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(s.cfg.Issuer),
		jwt.WithAudience(s.cfg.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil {
		return nil, err
	}
	return &claims, nil
}
```

Notes:
- `jwt.WithValidMethods([]string{"HS256"})` prevents algorithm-confusion attacks.
- `jwt.WithExpirationRequired()` guarantees `claims.ExpiresAt != nil` on success, so the
  handler's `expires_at` mapping is safe.
- The keyfunc returns the secret directly because `WithValidMethods` already enforces the
  algorithm (so the manual `*jwt.SigningMethodHMAC` check in `parseSessionClaims` isn't needed
  here).

## Step 3 — Implement the `Status` handler

Add to `auth.go`. New imports needed: `strings` and `google.golang.org/grpc/metadata`
(`codes`, `status`, `jwt`, `timestamppb`, `time` are already imported).

```go
func (s *AuthServer) Status(ctx context.Context, _ *calyxv1.StatusRequest) (*calyxv1.StatusResponse, error) {
	raw := bearerTokenFromContext(ctx)
	if raw == "" {
		return &calyxv1.StatusResponse{
			Authenticated: false,
			Message:       "no session token provided",
		}, nil
	}

	claims, err := s.verifySessionToken(raw)
	if err != nil {
		// Generic message only: never leak parser internals or the raw token.
		return &calyxv1.StatusResponse{
			Authenticated: false,
			Message:       "session token is invalid or expired",
		}, nil
	}

	return &calyxv1.StatusResponse{
		Authenticated: true,
		Message:       "session is valid",
		Session: &calyxv1.SessionInfo{
			Name:        claims.Name,
			Email:       claims.Email,
			Role:        claims.Role,
			Permissions: claims.Permissions,
			ExpiresAt:   timestamppb.New(claims.ExpiresAt.Time),
		},
	}, nil
}

// bearerTokenFromContext returns the raw session token from the incoming
// "authorization" metadata, stripping a case-insensitive "Bearer " prefix.
// Returns "" when metadata or the header is absent/empty.
func bearerTokenFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("authorization") // metadata keys are lowercased; Get is case-insensitive
	if len(vals) == 0 {
		return ""
	}
	v := strings.TrimSpace(vals[0])
	if len(v) >= 7 && strings.EqualFold(v[:7], "Bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return v // tolerate a bare token without the scheme
}
```

Only genuine server-side faults would return a gRPC error (e.g. `codes.Internal`); none arise
on this path, so all auth outcomes return `OK` with the body set.

## Step 4 — Tests (`auth_test.go`)

Add two small helpers, then the cases. Reuse `newTestAuthClient`, `testAuthConfig`,
`testSigningKey`, `testNow`, `testUser`.

- Helper to mint a session token signed with `testSigningKey`, parameterized so cases can vary
  signing key, issuer, audience, and `exp` (build `sessionClaims` like `Login` does, then
  `jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)`). Default exp =
  `testNow.Add(time.Hour)`, role `"admin"`, permissions `["*"]`, from `testUser`.
- Call pattern: attach the token with
  `ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)`
  then `client.Status(ctx, &calyxv1.StatusRequest{})`.

Cases (all expect a non-error gRPC response; assert on the body):

| Case | Setup | Expect |
| --- | --- | --- |
| Valid session | mint default token (or reuse `Login`'s output), attach | `Authenticated==true`; `Message=="session is valid"`; `Session.Name/Email==testUser`; `Role=="admin"`; `Permissions==["*"]`; `ExpiresAt` Unix == token `exp` |
| No metadata | call with `context.Background()` (no header) | `Authenticated==false`; `Message=="no session token provided"`; `Session==nil`; `err==nil` |
| Malformed token | attach `"not-a-jwt"` | `Authenticated==false`; `Message=="session token is invalid or expired"` |
| Wrong signature | sign with a different key | `Authenticated==false` |
| Expired token | mint with `exp = testNow.Add(-time.Minute)` (server clock is `testNow`) | `Authenticated==false` |
| Wrong issuer/audience | mint with a bad `iss` or `aud` | `Authenticated==false` |

Optionally add an end-to-end case that calls `Login` first and feeds the returned
`SessionToken` straight into `Status` to prove the issue→verify round-trip.

## Verification

```bash
just generate   # buf lint + buf generate + go mod tidy; regenerates auth.pb.go / auth_grpc.pb.go
just build      # builds bin/calyx and bin/backend
just test       # runs go test ./... including the new Status cases
```

Manual smoke test (optional), backend up via `just run` and a token from `calyx auth login`:

```bash
grpcurl -plaintext -H "authorization: Bearer <session-token>" \
  localhost:50051 mitsuhitofujita.calyx.v1.AuthService/Status
# -> { "authenticated": true, "message": "session is valid", "session": { name, email, role, permissions, expiresAt } }

grpcurl -plaintext localhost:50051 mitsuhitofujita.calyx.v1.AuthService/Status
# -> { "authenticated": false, "message": "no session token provided" }
```

## Security notes

- Never log the raw token; failure messages stay generic (`"session token is invalid or
  expired"`) — no parser internals echoed back.
- HS256 pinned via `jwt.WithValidMethods` blocks algorithm-confusion.
- `verifySessionToken` is deliberately standalone for later reuse by the verification
  interceptor (and the CLI re-auth-on-expiry flow), both out of scope here.
```
