# ISSUE-008: Implement Backend `AuthService.Status` RPC (Verify a Session Token and Report Its Status)

## Status
- **Status**: Open / In Development
- **Priority**: High (Backend verification side of the authentication flow in spec.md)
- **Assignee**: AI Agent / Developer
- **Depends on**: ISSUE-006 (`AuthService.Login`: session JWT issuance, signing config, `sessionClaims`), ISSUE-007 (CLI persists the session token and attaches it as `authorization` metadata)
- **Blocks**: The future `calyx auth status` CLI command (consumes this RPC)

## Objective
Add a new `Status` RPC to the existing `AuthService` in the backend (`apps/backend`).
The RPC reads the **Calyx session token (JWT)** presented by the caller, verifies it, and
reports the result:

- **If the session is valid**, it returns the session's basic information: the user's
  **name**, **role**, **permissions (authorizations)**, and **session expiry**.
- **If no session token is present, or the token is invalid/expired**, it returns a response
  indicating the caller is not authenticated, together with a human-readable message
  explaining why.

This issue covers the **backend side only**. Adding the `calyx auth status` command that
calls this RPC and renders the result is a separate, future CLI issue.

## Background
According to `intent.md` and `spec.md`, after the backend issues a short-lived session JWT
(ISSUE-006), the CLI saves it (ISSUE-007) and attaches it as gRPC metadata
(`authorization: Bearer <session_token>`) on subsequent requests. The backend then verifies
this token statelessly on each request.

`spec.md` describes a future gRPC **interceptor** that verifies the session token on *every*
protected RPC and injects roles/permissions into handlers. That interceptor is still out of
scope (see ISSUE-006 Out of Scope). This issue introduces a single, explicit "what is my
session status?" endpoint — effectively a `whoami` — which is the first place the backend
actually *verifies* a previously issued session token. The verification logic written here is
intended to be reused later by the interceptor.

## Design Decisions
These decisions keep the implementation small, self-contained, and consistent with the
existing `AuthService`.

1. **The session token is read from gRPC metadata, not a request field.** The caller sends
   it as `authorization: Bearer <session_token>`, matching `spec.md` ("the CLI sets the
   session token as metadata in the gRPC header") and what ISSUE-007 already attaches to
   outgoing requests. `StatusRequest` is therefore empty. This keeps `Status` consistent with
   the future interceptor, which will read the token from the same metadata key.
2. **`Status` reports auth state in the response body; it does not fail with
   `UNAUTHENTICATED` for a missing/invalid token.** Because this endpoint's purpose is to
   *report* whether the caller is authenticated, the "not authenticated" cases are normal
   results, returned as `StatusResponse{ authenticated: false, message: ... }`. This lets the
   future `calyx auth status` command print a clear message instead of treating the absence
   of a session as a transport error. (This is the opposite of the future interceptor, which
   *will* reject protected RPCs with `UNAUTHENTICATED`.) Only genuine server-side faults
   return a gRPC error (e.g. `Internal`).
3. **Verification reuses the existing `AuthConfig` (signing key, issuer, audience).** No new
   configuration or environment variables are required; the same secret that signs the token
   in `Login` verifies it here. The check validates the HS256 signature, the `iss`/`aud`
   claims, and the `exp` (expiry).
4. **Verification logic is extracted into a reusable helper.** Add a method such as
   `verifySessionToken(raw string) (*sessionClaims, error)` on `AuthServer` so that both the
   `Status` handler (now) and the future verification interceptor (later) share one
   implementation.
5. **Fixed role/permissions are surfaced as-is.** The token already carries the placeholder
   `role = "admin"` and `permissions = ["*"]` from ISSUE-006; `Status` simply echoes whatever
   the verified token contains. Real role/permission resolution remains future work.

## Scope

### In Scope
- A new `Status` RPC added to the existing `AuthService`.
- Protobuf definitions for `StatusRequest` (empty) and `StatusResponse` (plus a nested
  `SessionInfo` message for the populated-when-valid fields).
- Reading the session token from the incoming `authorization` metadata (`Bearer` scheme).
- Stateless verification of the session JWT (HS256 signature, `iss`, `aud`, `exp`) using the
  existing `AuthConfig`.
- A reusable `verifySessionToken` helper on `AuthServer`.
- Returning, on success, the user's name, role, permissions, and session expiry; and on
  failure, `authenticated: false` with an explanatory message.
- Unit/integration tests for the valid, missing, malformed, expired, and wrong-signature
  cases.

### Out of Scope (Future Issues)
- The `calyx auth status` CLI command that calls this RPC and renders the result.
- The backend gRPC verification **interceptor** that enforces the session token on *other*
  RPCs and injects roles/permissions into handlers (`spec.md` "Request Handling"). This
  issue verifies the token only inside the `Status` handler; it does not protect other RPCs.
- The re-authentication-on-expiry flow (deleting the stored token and prompting the user to
  re-run `calyx auth login`), which belongs with the interceptor work.
- Real (non-fixed) role/permission resolution.
- Refresh tokens / automatic renewal (explicitly excluded by `intent.md`).

## Technical Specifications

### Service Details
- **gRPC Package**: `mitsuhitofujita.calyx.v1`
- **Service Name**: `AuthService` (existing)
- **RPC Method**: `Status` (new)
- **Full gRPC Path**: `/mitsuhitofujita.calyx.v1.AuthService/Status`

### Proto Schema
Extend the existing `shared/proto/mitsuhitofujita/calyx/v1/auth.proto`: add the `Status` RPC
to `AuthService` and the new messages below (keep the existing `Login` RPC and its messages
unchanged).

```protobuf
// Add to the existing AuthService:
service AuthService {
  // Login verifies a Google credential and returns a short-lived session JWT.
  rpc Login(LoginRequest) returns (LoginResponse);

  // Status verifies the session token presented in the request metadata and
  // reports whether the session is valid, along with its details when it is.
  rpc Status(StatusRequest) returns (StatusResponse);
}

// StatusRequest is intentionally empty: the session token is read from the
// "authorization: Bearer <session_token>" gRPC metadata header.
message StatusRequest {}

message StatusResponse {
  // Whether a valid (signed, unexpired, correctly-scoped) session token was
  // presented in the request metadata.
  bool authenticated = 1;
  // Human-readable detail. On success, a brief confirmation; on failure, the
  // reason (e.g. no token provided, or the session is invalid/expired).
  string message = 2;
  // Populated only when authenticated == true.
  SessionInfo session = 3;
}

// SessionInfo carries the verified session's basic details.
message SessionInfo {
  // User's display name (from the JWT "name" claim).
  string name = 1;
  // User's email (from the JWT "email" claim); supplementary.
  string email = 2;
  // Role from the JWT "role" claim (currently the fixed placeholder "admin").
  string role = 3;
  // Permissions/authorizations from the JWT "permissions" claim
  // (currently the fixed placeholder ["*"]).
  repeated string permissions = 4;
  // When the session expires (mirrors the JWT "exp" claim).
  google.protobuf.Timestamp expires_at = 5;
}
```

> `auth.proto` already imports `google/protobuf/timestamp.proto`; no new import is needed.

### Reading the Session Token (Metadata)
- Extract the incoming metadata with `metadata.FromIncomingContext(ctx)`.
- Read the `authorization` key and strip a leading `Bearer ` prefix (treat the scheme
  case-insensitively).
- If the metadata or the `authorization` value is absent/empty, return
  `StatusResponse{ authenticated: false, message: "no session token provided" }` (not a gRPC
  error).

### Session JWT Verification
- Parse the token with `github.com/golang-jwt/jwt/v5` into the existing `sessionClaims`
  type, supplying the existing `AuthConfig.SigningKey` via the key function.
- Restrict the accepted signing method to **HS256** (`jwt.WithValidMethods([]string{"HS256"})`)
  to avoid algorithm-confusion attacks.
- Validate the registered claims against the existing config: issuer
  (`jwt.WithIssuer(cfg.Issuer)`) and audience (`jwt.WithAudience(cfg.Audience)`); expiry
  (`exp`) is validated automatically by the library.
- On any verification failure (bad signature, wrong method, wrong `iss`/`aud`, expired, or
  unparsable), return
  `StatusResponse{ authenticated: false, message: "session token is invalid or expired" }`.
  Do **not** include parser/internal error details or the raw token in the message.
- On success, build `SessionInfo` from the verified claims (`name`, `email`, `role`,
  `permissions`, and `exp` mapped to `expires_at`) and return
  `StatusResponse{ authenticated: true, message: "session is valid", session: ... }`.

### Response Semantics Summary

| Situation | gRPC status | `authenticated` | `message` (example) | `session` |
| --- | --- | --- | --- | --- |
| Valid session token | `OK` | `true` | `session is valid` | populated |
| No `authorization` metadata | `OK` | `false` | `no session token provided` | empty |
| Malformed / bad-signature / wrong iss-aud / expired token | `OK` | `false` | `session token is invalid or expired` | empty |
| Server-side fault | `Internal` | — | — | — |

### Configuration (Environment Variables)
None new. `Status` reuses the existing backend config loaded in ISSUE-006
(`CALYX_JWT_SIGNING_KEY`, `CALYX_JWT_ISSUER`, `CALYX_JWT_AUDIENCE`). No `.env.example`
changes are required.

## Directory and File Mapping
- `shared/proto/mitsuhitofujita/calyx/v1/auth.proto` (Modify): add the `Status` RPC and the
  `StatusRequest` / `StatusResponse` / `SessionInfo` messages.
- `shared/proto/mitsuhitofujita/calyx/v1/auth.pb.go`,
  `shared/proto/mitsuhitofujita/calyx/v1/auth_grpc.pb.go` (Regenerated via `just generate`).
- `apps/backend/internal/server/auth.go` (Modify): add the `Status` handler and the reusable
  `verifySessionToken` helper on `AuthServer`.
- `apps/backend/internal/server/auth_test.go` (Modify): add `Status` tests.
- `apps/backend/main.go` (No change expected): `AuthService` is already registered, so the
  new RPC is served automatically.

## Implementation Steps

### Step 1: Extend and Generate the Proto
1. Add the `Status` RPC and the new messages to `auth.proto` as specified above.
2. Run `just generate` to lint and regenerate `auth.pb.go` / `auth_grpc.pb.go`.

### Step 2: Add the Verification Helper
1. In `apps/backend/internal/server/auth.go`, add
   `verifySessionToken(raw string) (*sessionClaims, error)` on `AuthServer`, parsing into the
   existing `sessionClaims` and validating method/signature/issuer/audience/expiry as above.

### Step 3: Implement the `Status` Handler
1. Implement `Status(ctx, *StatusRequest) (*StatusResponse, error)`:
   - Read the token from `authorization` metadata; if absent, return the
     `authenticated: false` "no session token provided" response.
   - Otherwise call `verifySessionToken`; on error return the `authenticated: false`
     "invalid or expired" response.
   - On success, return `authenticated: true` with a populated `SessionInfo`.

### Step 4: Tests
1. Add the test cases listed below to `auth_test.go`, reusing the existing bufconn harness,
   `testSigningKey`, fixed clock, and `testUser`.

## Verification and Testing Plan

### 1. Build
```bash
just generate
just build
```
Verify `auth.pb.go` / `auth_grpc.pb.go` regenerate and `bin/backend` builds.

### 2. Unit / Integration Tests
```bash
just test
```
Extend `auth_test.go` (the existing in-memory bufconn harness can be reused; send the token
by attaching `authorization: Bearer <jwt>` via `metadata.AppendToOutgoingContext`). Cover:
- **Valid session**: mint a token via `Login` (or sign one with `testSigningKey`), call
  `Status` with it attached → `authenticated == true`; `session.name`, `session.role`
  (`"admin"`), `session.permissions` (`["*"]`), and `session.expires_at` match the token.
- **No metadata / no `authorization` header** → `authenticated == false` with the
  "no session token provided" message and `OK` status (no gRPC error).
- **Malformed token** (not a JWT) → `authenticated == false`, "invalid or expired".
- **Wrong signature** (token signed with a different key) → `authenticated == false`.
- **Expired token** (advance the injectable clock past `exp`, or craft an already-expired
  `exp`) → `authenticated == false`.
- **Wrong issuer/audience** → `authenticated == false`.

### 3. Manual Smoke Test (optional)
With the backend running (`just run`) and a session token obtained via `calyx auth login`
(ISSUE-007), confirm the status:
```bash
grpcurl -plaintext -H "authorization: Bearer <session-token>" \
  localhost:50051 mitsuhitofujita.calyx.v1.AuthService/Status
```
**Expected (valid token)**: a JSON response with `authenticated: true` and a `session`
object containing `name`, `role`, `permissions`, and `expiresAt`.

```bash
# Without the header:
grpcurl -plaintext localhost:50051 mitsuhitofujita.calyx.v1.AuthService/Status
```
**Expected**: `authenticated: false` with the "no session token provided" message.

## Security & Future Work Notes
- Never log the raw session token, and never echo parser/internal error details back to the
  caller; the response message must stay generic for failures.
- Restricting accepted algorithms to HS256 prevents algorithm-confusion attacks.
- The `verifySessionToken` helper is intended for reuse by the future gRPC verification
  interceptor that will enforce the session token on protected RPCs (and drive the
  re-authentication-on-expiry flow on the CLI side).
- `role`/`permissions` are fixed placeholders echoed from the token; replace with real
  resolution later.
- A follow-up CLI issue will add `calyx auth status`, which calls this RPC (attaching the
  stored token from ISSUE-007) and renders the user name, role, permissions, and expiry —
  or the not-authenticated message.
