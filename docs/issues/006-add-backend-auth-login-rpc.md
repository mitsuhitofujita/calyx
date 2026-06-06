# ISSUE-006: Implement Backend `AuthService.Login` RPC (Exchange a Google Token for a Session JWT)

## Status
- **Status**: Open / In Development
- **Priority**: High (Backend half of the authentication flow in spec.md)
- **Assignee**: AI Agent / Developer
- **Depends on**: ISSUE-001 (gRPC pipeline), ISSUE-005 (CLI obtains the Google token)
- **Blocks**: ISSUE-007 (CLI persists and uses the session token)

## Objective
Implement a new `AuthService.Login` RPC in the backend (`apps/backend`). The RPC
receives a Google-issued credential (ID token or authorization code) from the CLI,
verifies it, and returns a **Calyx-issued short-lived session token (JWT)** together
with its expiration time.

This issue covers the **backend side only**. Persisting the returned session token in
the CLI and attaching it to subsequent requests is handled separately in ISSUE-007.

## Background
According to `intent.md` and `spec.md`, Calyx authenticates users through Google
Authentication. The full target flow is:

1. The CLI asks the user to authenticate with Google via a web browser. *(ISSUE-005, done)*
2. The CLI sends the Google-issued authorization code (or ID token) to the backend. *(ISSUE-007)*
3. **The backend verifies the Google token using Google's public keys.** *(this issue)*
4. **The backend generates a new system-specific short-lived session token (JWT).** *(this issue)*
5. **The backend returns the session token and its expiration to the CLI.** *(this issue)*
6. The CLI stores the session token and uses it on subsequent requests. *(ISSUE-007)*

ISSUE-005 already produces a Google **ID token** on the CLI side. This issue implements
steps 3–5: verify that token and mint the Calyx session JWT.

## Design Decisions
These decisions keep the implementation small and self-contained for the current phase.

1. **Roles and authorizations are fixed placeholders.** Every successfully authenticated
   user receives `role = "admin"` and `permissions = ["*"]` ("all"). Real
   role/permission resolution (e.g. from a user store) is future work.
2. **Session JWT signing: HMAC (HS256) with a secret from configuration.** The same
   backend both issues and verifies the session token, so a symmetric key is the simplest
   correct choice and satisfies the spec's "stateless verification without external
   storage" requirement. The signing secret is injected via an environment variable and
   must never be committed. Moving to asymmetric signing (RS256/EdDSA) is a possible future
   hardening step but is **not** used now.
3. **Credential accepted from the CLI: the Google ID token (primary).** The request type
   also reserves an authorization-code field so the contract matches the
   "auth code **or** ID token" wording, but **only the ID-token branch is implemented in
   this phase** (it matches what ISSUE-005 already obtains). The auth-code branch returns
   `UNIMPLEMENTED` for now.

## Scope

### In Scope
- A new `AuthService` gRPC service with a `Login` RPC.
- Protobuf definitions for `AuthService`, `LoginRequest`, and `LoginResponse`.
- Verification of the Google **ID token** against Google's public keys (signature, issuer,
  audience, expiry).
- Issuing a Calyx session JWT containing basic user info plus the fixed role/permissions.
- Returning the session token and its expiration to the caller.
- Registering `AuthService` on the existing gRPC server alongside `SampleService`.
- Unit/integration tests for token issuance and the validation/error paths.

### Out of Scope (Future Issues)
- The CLI changes that call this RPC, store the token, and attach it to later requests
  (ISSUE-007).
- A backend gRPC interceptor that verifies the session token on *subsequent* requests and
  injects roles/permissions into handlers (`spec.md` "Request Handling"). This issue only
  *issues* the token; enforcing it on other RPCs is a later issue.
- Real (non-fixed) role/permission resolution.
- The authorization-code exchange branch (reserved in the proto, returns `UNIMPLEMENTED`).
- Refresh tokens / automatic renewal (explicitly excluded by `intent.md`).
- Including client-terminal identification info in the request (`spec.md` mentions it; it is
  deferred and noted under Future Work).

## Technical Specifications

### Service Details
- **gRPC Package**: `mitsuhitofujita.calyx.v1`
- **Service Name**: `AuthService`
- **RPC Method**: `Login`
- **Full gRPC Path**: `/mitsuhitofujita.calyx.v1.AuthService/Login`

### Proto Schema
Recommended new file: `shared/proto/mitsuhitofujita/calyx/v1/auth.proto`.

```protobuf
syntax = "proto3";

package mitsuhitofujita.calyx.v1;

import "google/protobuf/timestamp.proto";

option go_package = "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1;calyxv1";

// AuthService exchanges a Google-issued credential for a Calyx session token.
service AuthService {
  // Login verifies a Google credential and returns a short-lived session JWT.
  rpc Login(LoginRequest) returns (LoginResponse);
}

message LoginRequest {
  // The Google-issued credential obtained by the CLI during browser sign-in.
  // Exactly one variant is expected.
  oneof google_credential {
    // Google OIDC ID token (JWT). Implemented in this phase.
    string id_token = 1;
    // Google OAuth authorization code. Reserved; returns UNIMPLEMENTED for now.
    string auth_code = 2;
  }
}

message LoginResponse {
  // The Calyx-issued session token (JWT) to be sent on subsequent requests.
  string session_token = 1;
  // When the session token expires.
  google.protobuf.Timestamp expires_at = 2;
}
```

> `google/protobuf/timestamp.proto` is a well-known type bundled with `buf`; no extra
> module wiring is required beyond the import.

### Google ID Token Verification
- Validate the incoming `id_token` using Google's published public keys (do **not** decode
  it without verification).
- Recommended library: `google.golang.org/api/idtoken`
  (`idtoken.Validate(ctx, rawIDToken, audience)`), which checks the signature, issuer
  (`https://accounts.google.com` / `accounts.google.com`), audience, and expiry, fetching
  and caching Google's keys for you.
- The expected **audience** is the Google OAuth client ID. The backend reads it from
  `CALYX_GOOGLE_CLIENT_ID` (the same value the CLI uses in ISSUE-005). If verification
  fails, return gRPC `UNAUTHENTICATED` with a clear message; do not issue a token.
- From the verified token's claims, extract the user fields used below
  (`sub`, `email`, `name`; `email` / `name` may be absent depending on granted scopes).

### Session JWT Specification
- **Format**: JWT signed with **HS256**.
- **Lifetime**: short-lived; default **1 hour** (configurable, see Configuration).
- **Claims**:

  | Claim | Source | Example | Notes |
  | --- | --- | --- | --- |
  | `iss` | config | `calyx-backend` | Token issuer. |
  | `aud` | config | `calyx-cli` | Intended audience (the CLI). |
  | `sub` | Google `sub` | `1098...4321` | Stable Google user id. |
  | `email` | Google `email` | `user@example.com` | Basic user info. |
  | `name` | Google `name` | `Mitsuhito Fujita` | Basic user info (optional). |
  | `role` | **fixed** | `admin` | Placeholder for this phase. |
  | `permissions` | **fixed** | `["*"]` | "All"; placeholder for this phase. |
  | `iat` | now | — | Issued-at. |
  | `exp` | now + TTL | — | Expiry; mirrors `expires_at` in the response. |

- The `expires_at` field in `LoginResponse` MUST equal the JWT `exp` claim.
- Recommended library: `github.com/golang-jwt/jwt/v5`.

### Configuration (Environment Variables)
The backend loads these at startup (a git-ignored `.env` at the repo root is acceptable for
development, consistent with ISSUE-005; existing process env vars take precedence):

- `CALYX_GOOGLE_CLIENT_ID` (required): expected audience for Google ID-token verification.
- `CALYX_JWT_SIGNING_KEY` (required): HMAC secret used to sign/verify the session JWT.
  Fail fast with an actionable error if missing or empty.
- `CALYX_JWT_ISSUER` (optional, default `calyx-backend`): the `iss` claim.
- `CALYX_JWT_AUDIENCE` (optional, default `calyx-cli`): the `aud` claim.
- `CALYX_SESSION_TTL` (optional, default `1h`): session lifetime, parsed as a Go duration.

Add any new keys to the committed `.env.example` with placeholder values.

## Directory and File Mapping
- `shared/proto/mitsuhitofujita/calyx/v1/auth.proto` (Add): the service/message definitions.
- `shared/proto/mitsuhitofujita/calyx/v1/auth.pb.go`,
  `shared/proto/mitsuhitofujita/calyx/v1/auth_grpc.pb.go` (Generated via `just generate`).
- `apps/backend/internal/server/auth.go` (Add): `AuthServer` implementing the generated
  `AuthServiceServer` (Google verification + JWT issuance).
- `apps/backend/internal/server/auth_test.go` (Add): unit/integration tests.
- `apps/backend/main.go` (Modify): construct and register `AuthServer` next to
  `SampleServer`; read the new configuration.
- `.env.example` (Modify): document `CALYX_JWT_SIGNING_KEY` and the optional JWT/session keys.
- `go.mod` / `go.sum` (Modify): add `github.com/golang-jwt/jwt/v5` and
  `google.golang.org/api/idtoken` (run `just generate` / `just tidy`).

## Implementation Steps

### Step 1: Define and Generate the Proto
1. Add `auth.proto` as specified above.
2. Run `just generate` to lint and produce `auth.pb.go` / `auth_grpc.pb.go`.

### Step 2: Implement `AuthServer`
1. Add `AuthServer` in `apps/backend/internal/server/auth.go`, embedding
   `calyxv1.UnimplementedAuthServiceServer`.
2. Give it the configuration it needs (expected audience, signing key, issuer, audience,
   TTL) via a constructor, e.g. `NewAuthServer(cfg AuthConfig)`.
3. In `Login`:
   - If the request carries `auth_code`, return `codes.Unimplemented`.
   - If it carries `id_token`, verify it (audience = `CALYX_GOOGLE_CLIENT_ID`). On failure
     return `codes.Unauthenticated`.
   - Build the claims (verified user fields + fixed `role`/`permissions` + `iss`/`aud`/
     `iat`/`exp`), sign with HS256, and return the token and `expires_at`.

### Step 3: Wire Configuration and Registration
1. In `apps/backend/main.go`, read the new environment variables; fail fast on a missing
   required value.
2. Construct `AuthServer` and register it with
   `calyxv1.RegisterAuthServiceServer(grpcServer, authServer)` alongside `SampleService`.

### Step 4: Documentation
1. Update `.env.example` with the new keys and placeholders.

## Verification and Testing Plan

### 1. Build
```bash
just generate
just build
```
Verify `auth.pb.go` / `auth_grpc.pb.go` are generated and `bin/backend` builds.

### 2. Unit / Integration Tests
Add `auth_test.go` covering:
- **Issuance (happy path)**: with a stubbed/injected Google-token verifier returning known
  claims, `Login` returns a non-empty `session_token` and an `expires_at` roughly `TTL`
  in the future. Parsing the JWT with the signing key yields `role = "admin"`,
  `permissions = ["*"]`, and the expected `email`/`sub`.
- **Invalid Google token**: the verifier rejects the token → `Login` returns
  `codes.Unauthenticated` and no token.
- **Auth-code branch**: a request with `auth_code` set → `codes.Unimplemented`.
- **Empty request**: neither variant set → an `InvalidArgument`/`Unauthenticated` error.
- **Expiry consistency**: the JWT `exp` claim equals `LoginResponse.expires_at`.

> Tip: inject the Google-token verifier behind a small interface so tests don't call
> Google's network endpoints.

### 3. Manual Smoke Test (optional)
With the backend running (`just run`) and a real Google ID token (e.g. printed by
`calyx auth login` from ISSUE-005), call the RPC and confirm a session JWT is returned:
```bash
grpcurl -plaintext -d '{"id_token":"<google-id-token>"}' \
  localhost:50051 mitsuhitofujita.calyx.v1.AuthService/Login
```
**Expected**: a JSON response containing `sessionToken` and `expiresAt`.

## Security & Future Work Notes
- Never log the Google token or the issued session JWT. Keep `CALYX_JWT_SIGNING_KEY` and
  the Google client secret out of version control (`.env` stays git-ignored).
- `role`/`permissions` are fixed placeholders; replace with real resolution later.
- A follow-up will add the backend gRPC **verification interceptor** that validates the
  session token on subsequent requests and injects roles/permissions into handlers
  (`spec.md` "Request Handling").
- Asymmetric signing (RS256/EdDSA), client-terminal identification in the request, and the
  authorization-code exchange branch are deferred.
- ISSUE-007 consumes this RPC: it replaces the CLI's development-only token printing with a
  call to `AuthService.Login`, then stores the returned session token.
