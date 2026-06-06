# ISSUE-005: Add Google Authentication to the CLI (`calyx auth login`)

## Status
- **Status**: Open / In Development
- **Priority**: High (First step of the authentication flow described in spec.md)
- **Assignee**: AI Agent / Developer

## Objective
Implement a new `calyx auth login` command in the CLI application (`apps/cli`).
Running the command must start a browser-based Google authentication flow, obtain a
Google-issued token, and — **for this development phase only** — print that token
directly to standard output.

This issue covers the **CLI side only**. Sending the obtained token to the backend
and exchanging it for a system-issued session JWT (the `AuthService.Login` RPC) is a
separate, future issue and is explicitly out of scope here.

## Background
According to "intent.md" and "spec.md", Calyx authenticates users through Google
Authentication. The full target flow is:

1. The CLI asks the user to authenticate with Google via a web browser.
2. The CLI sends the Google-issued authorization code (or ID token) to the backend.
3. The backend verifies the Google token and issues a short-lived session JWT.
4. The CLI stores that session JWT in the OS credential store.

This issue implements **step 1** and the act of obtaining the Google token. Steps 2–4
are deferred. To keep the change small and verifiable, the obtained Google token is
printed to stdout instead of being forwarded to the backend or persisted. Printing the
token is a **temporary measure for the current phase** and MUST be removed once the
backend exchange (`AuthService.Login`) and credential-store persistence are implemented.

## Resolved Decisions
The two design questions raised during planning have been decided:

1. **Client ID injection — use a `.env` file.** The Google OAuth client ID and client
   secret are injected from a git-ignored `.env` file at the repository root. A
   committed `.env.example` documents the required keys.
2. **OAuth client type — "Desktop app", with a fixed loopback port.** A **Desktop app**
   (installed-app) OAuth client is used. This is Google's recommended type for native/CLI
   loopback sign-in (RFC 8252): a Desktop-app client accepts loopback redirects
   (`http://127.0.0.1:<port>/...`) **without registering any redirect URI** in the Google
   Cloud console, and Google does not pin the port — so there is no `redirect_uri_mismatch`
   to manage. A **fixed loopback port** is still used (rather than a random free port) only
   so the callback can be forwarded host→container in the devcontainer.

   > Note: an earlier revision chose a **Web application** client on the assumption that the
   > Desktop-app type "does not expose the loopback redirect configuration needed here". That
   > was incorrect — the Web-application type requires each redirect URI (port and path
   > included) to be registered exactly, which is what produced `redirect_uri_mismatch`
   > during testing. The Desktop-app type needs no such registration and is the right fit.

## Scope

### In Scope
- New command `calyx auth login`.
- Browser-based Google OAuth 2.0 sign-in initiated from the CLI.
- Obtaining a Google token and printing it to stdout.
- Loading the Google OAuth client ID/secret from a `.env` file.

### Out of Scope (Future Issues)
- The backend `AuthService.Login` RPC and Google token verification.
- Issuing or persisting the system session JWT.
- Storing tokens in the OS credential store (Windows Credential Manager / Linux Secret
  Service).
- Automatic token refresh and re-authentication-on-expiry handling.

## Technical Specifications

### CLI Syntax
```bash
calyx auth login
```
- Takes no positional arguments in this phase.
- On success: prints the obtained Google token to `stdout` and exits with code `0`.
- On failure (user cancels, timeout, network/OAuth error, missing configuration):
  prints a clear error message to `stderr` and exits with a non-zero code (e.g. `1`).

### OAuth Flow
Use the **OAuth 2.0 Authorization Code flow** with a **fixed loopback redirect**:

1. The CLI starts a temporary local HTTP server bound to the configured fixed loopback
   address to receive the OAuth redirect.
2. The CLI builds the Google authorization URL (including a generated `state` value) and
   opens it in the user's default browser. If a browser cannot be opened automatically,
   the CLI prints the URL to `stderr` and instructs the user to open it manually (this
   fallback also covers headless/container environments).
3. The user signs in and grants consent in the browser.
4. Google redirects the browser to the fixed loopback redirect URI with an authorization
   `code` (and the `state`).
5. The local HTTP server captures the `code`, validates `state`, and responds to the
   browser with a simple "You may close this window" page.
6. The CLI exchanges the `code` at Google's token endpoint using the client ID and
   client secret.
7. The CLI prints the resulting Google token to `stdout`.

Recommended (defense in depth): also generate a PKCE `code_verifier`/`code_challenge`
pair and validate `state` to mitigate authorization-code interception and CSRF.

Recommended implementation building blocks:
- `golang.org/x/oauth2` and `golang.org/x/oauth2/google` for the OAuth client,
  endpoints, and token exchange.
- `github.com/joho/godotenv` (or equivalent) to load the `.env` file at startup.
- The Go standard library `net/http` for the loopback callback server.
- `context` with a timeout so the command does not hang indefinitely if the user never
  completes the browser flow.

### Which Token to Print
The future backend RPC accepts "the Google-issued authorization code **or** ID token".
For this phase, after the token exchange the CLI should print the **Google ID token**
(`id_token` from the token response), since that aligns most directly with the planned
backend verification. The exact token type to forward to the backend will be finalized
in the backend issue; whichever is chosen, this phase only prints it.

### Configuration / Client ID Injection (`.env`)
The Google OAuth client credentials are injected from a **`.env` file at the repository
root**, which MUST be git-ignored. The CLI loads `.env` at startup (existing process
environment variables take precedence over `.env` values).

Required keys:
- `CALYX_GOOGLE_CLIENT_ID` (required)
- `CALYX_GOOGLE_CLIENT_SECRET` (required — issued together with the Desktop app
  client and used in the authorization-code exchange; Desktop-app clients are public
  clients, but Google still issues and accepts this secret in the token exchange)

Optional key:
- `CALYX_OAUTH_REDIRECT_ADDR` (optional; the fixed loopback `host:port` for the callback
  server, default below). For a Desktop-app client this need not be registered anywhere;
  any free loopback port works. A fixed value is used only for devcontainer port forwarding.

If any required value is missing or empty, the command must fail fast with an actionable
error naming the missing key.

A committed **`.env.example`** must document these keys with placeholder values. Real
secrets must never be committed.

### Callback (Redirect) URL
- OAuth client type: **Desktop app** (installed app).
- Fixed redirect URI (default): `http://127.0.0.1:8765/callback`.
- The loopback callback server binds to the same fixed `host:port`
  (default `127.0.0.1:8765`, overridable via `CALYX_OAUTH_REDIRECT_ADDR`).
- A Desktop-app client accepts loopback redirects **without any "Authorized redirect URIs"
  registration**, so there is nothing to configure in the Google Cloud console and no
  `redirect_uri_mismatch` to manage. `127.0.0.1` is Google's documented loopback form
  (`localhost` also works but can trip some firewalls).

**Devcontainer note (the "does it work inside the container?" concern):** when the CLI
runs inside a container, Google redirects the **host's** browser to the loopback
`host:port`, which is the host loopback, not the container loopback. For the flow to
complete with the CLI running inside the container, that fixed port must be forwarded
from host to container (e.g. via the devcontainer `forwardPorts` setting). Using a fixed
port (rather than an ephemeral one) is what makes this static forwarding possible. If
forwarding is not configured, the fallback is to run `calyx auth login` on the host. The
definitive container strategy will be decided after a first working implementation.

## Directory and File Mapping
- `apps/cli/main.go` (Modify): register the new `auth` command group and `login`
  subcommand in the existing dispatch logic.
- `apps/cli/` (Add, suggested): a new file such as `auth.go` containing the OAuth /
  login implementation, to keep `main.go` focused on dispatch.
- `.env.example` (Add, repository root): document the required configuration keys with
  placeholder values.
- `.gitignore` (Modify): add `.env` so real credentials are never committed.
- `go.mod` / `go.sum` (Modify): add the `golang.org/x/oauth2` and `.env`-loader
  dependencies.

## Implementation Steps

### Step 1: Wire Up the Command
1. Extend the dispatcher in `apps/cli/main.go` to recognize an `auth` command group.
2. Within `auth`, dispatch the `login` subcommand to a new handler (e.g. `runAuthLogin`).
3. Update the top-level usage text to list `auth login`.
4. For any unknown `auth` subcommand or extra arguments, print a usage message to
   `stderr` and return the existing usage-error sentinel.

### Step 2: Load Configuration from `.env`
1. Load the `.env` file at the repository root (do not fail if it is absent; existing
   environment variables still apply).
2. Read `CALYX_GOOGLE_CLIENT_ID`, `CALYX_GOOGLE_CLIENT_SECRET`, and the optional
   `CALYX_OAUTH_REDIRECT_ADDR` (default `127.0.0.1:8765`).
3. If a required value is missing or empty, return a clear, actionable error.

### Step 3: Implement the OAuth Loopback Flow
1. Generate a random `state` (and, recommended, a PKCE verifier/challenge pair).
2. Start a loopback HTTP server on the fixed redirect address.
3. Build the Google authorization URL and attempt to open it in the default browser;
   on failure, print the URL to `stderr` as a manual fallback.
4. Wait (with a context timeout) for the redirect; validate `state`; capture the `code`.
5. Respond to the browser with a minimal "authentication complete, you may close this
   window" page.

### Step 4: Exchange the Code and Output the Token
1. Exchange the authorization `code` at Google's token endpoint using the client ID and
   client secret.
2. Print the obtained Google token (recommended: the `id_token`) to `stdout` with a
   trailing newline.
3. Shut down the loopback server and release resources cleanly.

### Step 5: Documentation
1. Add `.env.example` with the required configuration keys.
2. Add `.env` to `.gitignore`.
3. Note in the CLI usage/help that `auth login` currently prints the token (temporary,
   development-only behavior).

## Verification and Testing Plan

### 1. Build the CLI
```bash
just build
```
Verify that `bin/calyx` is generated successfully.

### 2. Missing Configuration
Run the command with no `.env` and no client ID configured:
```bash
./bin/calyx auth login
```
**Expected:** an error on `stderr` naming the missing configuration (e.g.
`CALYX_GOOGLE_CLIENT_ID`); exit code non-zero.

### 3. Successful Login
With a valid `.env` (Desktop app client ID/secret) — no redirect-URI registration is
required for a Desktop-app client — run:
```bash
./bin/calyx auth login
```
**Expected:**
- The default browser opens the Google consent screen (or the URL is printed to
  `stderr` as a fallback).
- After consent, the browser shows a "you may close this window" page.
- The Google token is printed to `stdout`.
- Exit code `0`.

### 4. Usage / Unknown Subcommand
```bash
./bin/calyx auth
./bin/calyx auth bogus
```
**Expected:** a usage message on `stderr`; non-zero exit code.

### 5. Timeout / Cancellation
Start the flow and do not complete the browser sign-in within the timeout window.
**Expected:** the command times out with a clear `stderr` message; non-zero exit code;
no orphaned loopback server process remains.

## Security & Future Work Notes
- Printing the token to stdout is **development-only** and must be removed once the
  backend exchange and credential-store persistence are implemented.
- Validate the OAuth `state` parameter (and, recommended, use PKCE) to mitigate
  authorization-code interception and CSRF.
- Never commit real client credentials; keep `.env` git-ignored and rely on the
  `.env.example` template for documentation. Build-time embedding of the client ID
  (e.g. via `-ldflags`) may be revisited later as a hardening step but is not used now.
- A follow-up issue will forward the Google authorization code / ID token to the backend
  `AuthService.Login` RPC, exchange it for a session JWT, and store that JWT in the OS
  credential store as described in spec.md.
