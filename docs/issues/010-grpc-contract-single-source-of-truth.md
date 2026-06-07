# ISSUE-010: Keep the gRPC Contract as a Single Source of Truth in `shared/proto` (No Separate Reference Doc)

## Status
- **Status**: Resolved (decision recorded; changes applied to the working tree)
- **Priority**: Medium (documentation strategy for AI-agent consumers)
- **Assignee**: AI Agent / Developer
- **Supersedes**: the earlier plan to author a hand-maintained `docs/grpc-api.md` reference.
- **Related**: ISSUE-011 (the equivalent decision for the CLI: a generated metadata command
  instead of a hand-written CLI reference).

## Decision
**Do not create a separate, hand-maintained gRPC reference document.** The
`.proto` files under `shared/proto/mitsuhitofujita/calyx/v1/` are the single source of truth
for the gRPC contract; AI agents read them directly. A parallel Markdown reference would be
dual-maintenance and would inevitably drift from the proto.

The one class of information that was *not* expressible by the proto alone — the gRPC **status
codes** each RPC returns, and the **body-vs-error** semantics of `AuthService.Status` — has been
folded into the per-RPC doc comments in the `.proto` files, so reading `shared/proto` is now
genuinely sufficient.

## Rationale
- **The `.proto` IDL is already the machine-readable contract** (the protobuf analog of OpenAPI
  for REST). It is small (2 services, 3 RPCs), well-commented, and structured.
- **For AI agents, proto > prose.** It is parseable, and the live surface is additionally
  introspectable at runtime via gRPC server reflection (`grpcurl -plaintext <addr> list` /
  `... describe`).
- **A second document means dual-maintenance and drift** — exactly the cost to avoid. This is
  documentation *for AI agents*; there is no separate human-only audience that needs a curated
  prose reference.

## Changes Applied (this issue)
> These edits are in the working tree (regenerated, built, and tested). Review the diff before
> committing.

1. **`shared/proto/.../auth.proto`** — enriched RPC doc comments:
   - `AuthService.Login`: documented status codes — `OK`, `INVALID_ARGUMENT` (no credential /
     empty `id_token`), `UNIMPLEMENTED` (`auth_code` variant), `UNAUTHENTICATED` (Google
     id_token verification failed), `INTERNAL` (failed to sign the session token).
   - `AuthService.Status`: documented that auth outcomes are reported in the response body
     (`authenticated = false` with the reason in `message`), **not** as a gRPC error — `Status`
     returns `OK` even for a missing/invalid/expired token; a gRPC error means a server fault.
2. **`shared/proto/.../sample.proto`** — `SampleService.Hello`: documented `OK` on success and
   that authentication is **not enforced** (the CLI attaches the Bearer token when present, but
   the RPC neither requires nor verifies it).
3. **Regenerated** the Go code (`just generate`) so the generated `*_grpc.pb.go` doc comments
   carry the same status-code / behavior notes — single source, no drift.
4. **`docs/spec.md`** — strengthened the `shared/proto` entry in *Directory Structure* to state
   that the `.proto` files are the single source of truth for the gRPC contract (with status
   codes / behavior in the RPC comments), that there is intentionally **no** separate reference
   doc, and that server reflection allows runtime introspection. This is the agent-facing
   pointer ("for the gRPC contract, read `shared/proto` directly").

## Maintenance Rule (going forward)
- Keep all contract semantics (fields, status codes, behavioral notes) in the `.proto` files'
  doc comments. After editing a `.proto`, run `just generate` so the generated Go stays in
  sync. Never start a parallel hand-written reference.

## Future Work (optional, not required)
- If a *rendered* reference is ever wanted (e.g. for an external audience), **generate** it from
  the proto with `protoc-gen-doc` via a `buf.gen.yaml` plugin and a `just` recipe — never
  hand-write it. For AI-agent-only consumption this is unnecessary.
- Extend the proto comments as the surface grows (e.g. the future verification interceptor,
  real role/permission resolution, the `auth_code` exchange once implemented).

## Verification
- `just generate` (buf lint + generate + go mod tidy): succeeded.
- `just build`: both binaries compile.
- `just test`: all packages pass.
- Confirmed the new status-code / behavior comments are present in the generated
  `auth_grpc.pb.go` / `sample_grpc.pb.go`.
