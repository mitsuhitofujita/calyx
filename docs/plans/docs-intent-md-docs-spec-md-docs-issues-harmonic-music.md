# Plan: ISSUE-005 — Add Google Authentication to the CLI (`calyx auth login`)

## Context

Per `docs/intent.md` and `docs/spec.md`, Calyx authenticates users through Google
Authentication. The full target flow is: (1) the CLI asks the user to sign in with Google
via a browser, (2) the CLI sends the Google token to the backend, (3) the backend issues a
short-lived session JWT, (4) the CLI stores that JWT in the OS credential store.

**ISSUE-005 implements step 1 only.** It adds a new `calyx auth login` command that runs the
browser-based Google OAuth flow and — **for this development phase only** — prints the
obtained Google **ID token** to stdout. Sending the token to the backend
(`AuthService.Login`), issuing the session JWT, and persisting it in the credential store are
explicitly out of scope and deferred to future issues. The stdout print is a temporary,
verification-only measure and MUST be removed once the backend exchange lands.

Two design questions were already resolved in the issue:
1. **Client ID injection → `.env` file** (git-ignored), documented by a committed `.env.example`.
2. **OAuth client type → "Desktop app" with a fixed loopback port.** A Desktop-app
   (installed-app) client accepts loopback redirects without registering any redirect URI
   in the Google Cloud console (RFC 8252), avoiding `redirect_uri_mismatch`; the fixed port
   exists only so the callback can be forwarded host→container in the devcontainer. (An
   earlier revision chose "Web application", which requires exact redirect-URI registration
   — that mismatch is what surfaced `redirect_uri_mismatch` in testing.)

## Goal & Scope

**In scope**
- New `auth` command group with a `login` subcommand: `calyx auth login`.
- Browser-based Google OAuth 2.0 Authorization Code flow with a fixed loopback redirect.
- Loading `CALYX_GOOGLE_CLIENT_ID` / `CALYX_GOOGLE_CLIENT_SECRET` (+ optional redirect addr)
  from a `.env` file.
- Printing the Google `id_token` to stdout (exit 0); clear `stderr` errors + non-zero exit on
  failure.

**Out of scope (future issues)**
- Backend `AuthService.Login` RPC and Google token verification.
- Issuing/persisting the system session JWT, OS credential store, refresh/re-auth-on-expiry.

## Design Decisions (made for this plan)

These are not in the issue but are chosen to keep the implementation minimal and aligned with
the issue's recommended building blocks:

- **Browser opening:** a small `runtime.GOOS`-based `openBrowser` helper using `os/exec`
  (`rundll32` on Windows, `xdg-open` on Linux, `open` on macOS) — no extra dependency, matching
  the issue's stdlib-first building-block list. On failure, fall back to printing the URL to
  stderr (also covers headless/container environments).
- **PKCE + state:** included (the issue recommends it as defense in depth). Use
  `oauth2.GenerateVerifier()` / `oauth2.S256ChallengeOption` / `oauth2.VerifierOption`.
- **Scopes:** `openid`, `email`, `profile` — `openid` is required for Google to return an
  `id_token`.
- **Timeout:** a `context` timeout of 3 minutes so the command never hangs if the user abandons
  the browser flow; the loopback server is shut down via `defer` so no orphan process remains.
- **Devcontainer port forwarding:** NOT changed now. The issue explicitly defers the container
  strategy ("decided after a first working implementation"); fallback is running on the host.
  Documented as a note only.

## Files to Modify / Add

| File | Action | Purpose |
|---|---|---|
| `apps/cli/auth.go` | **Add** | OAuth login implementation (keeps `main.go` focused on dispatch). |
| `apps/cli/main.go` | Modify | Register the `auth` group + `login` subcommand; update usage text. |
| `.env.example` | **Add** (repo root) | Document required config keys with placeholders. |
| `.gitignore` | Modify | Add `.env` so real credentials are never committed. |
| `go.mod` / `go.sum` | Modify | Add `golang.org/x/oauth2` and `github.com/joho/godotenv`. |

## Implementation

### Step 1 — Wire up the command (`apps/cli/main.go`)

Reuse the existing dispatch pattern and the `errUsage` sentinel (`apps/cli/main.go:26`).

- In `dispatch` (`apps/cli/main.go:59`), add a case:
  ```go
  case "auth":
      return runAuth(args[1:])
  ```
- Add a line to `flag.Usage` (`apps/cli/main.go:31`) under `Commands:`:
  ```go
  fmt.Fprintf(os.Stderr, "  auth login            sign in with Google (prints the Google ID token; development-only)\n")
  ```
- `runAuth` / `runAuthLogin` live in the new `auth.go` (Step 2). Unknown `auth` subcommands or
  extra args print a usage message to stderr and return `errUsage`, exactly like `runSample`
  (`apps/cli/main.go:69`).

### Step 2 — New file `apps/cli/auth.go`

Package `main` (same package as `main.go`). Outline:

```go
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	defaultRedirectAddr = "127.0.0.1:8765"
	callbackPath        = "/callback"
	loginTimeout        = 3 * time.Minute
)

// runAuth dispatches `calyx auth` subcommands.
func runAuth(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: calyx auth login")
		return errUsage
	}
	switch args[0] {
	case "login":
		return runAuthLogin(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "calyx auth: unknown command %q\n", args[0])
		return errUsage
	}
}

func runAuthLogin(args []string) error {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: calyx auth login")
		return errUsage
	}

	// Load .env from the working directory (repo root). Absence is fine; existing
	// process env vars take precedence (godotenv.Load never overrides them).
	_ = godotenv.Load()

	clientID := os.Getenv("CALYX_GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("CALYX_GOOGLE_CLIENT_SECRET")
	if clientID == "" {
		return fmt.Errorf("missing required configuration: CALYX_GOOGLE_CLIENT_ID (set it in .env)")
	}
	if clientSecret == "" {
		return fmt.Errorf("missing required configuration: CALYX_GOOGLE_CLIENT_SECRET (set it in .env)")
	}

	redirectAddr := os.Getenv("CALYX_OAUTH_REDIRECT_ADDR")
	if redirectAddr == "" {
		redirectAddr = defaultRedirectAddr
	}

	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  fmt.Sprintf("http://%s%s", redirectAddr, callbackPath),
		Scopes:       []string{"openid", "email", "profile"},
	}

	idToken, err := authorize(conf, redirectAddr)
	if err != nil {
		return err
	}

	// DEVELOPMENT-ONLY: print the Google ID token. Remove once the backend
	// AuthService.Login exchange and credential-store persistence land (ISSUE follow-up).
	fmt.Println(idToken)
	return nil
}
```

The OAuth loopback flow (`authorize`) + helpers:

```go
type callbackResult struct {
	code string
	err  error
}

// authorize runs the loopback OAuth flow and returns the Google id_token.
func authorize(conf *oauth2.Config, addr string) (string, error) {
	state, err := randomState()
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}
	verifier := oauth2.GenerateVerifier()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("failed to bind loopback callback server on %s: %w", addr, err)
	}

	resultCh := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			resultCh <- callbackResult{err: fmt.Errorf("authorization denied: %s", e)}
			http.Error(w, "Authentication failed. You may close this window.", http.StatusBadRequest)
			return
		}
		if q.Get("state") != state {
			resultCh <- callbackResult{err: fmt.Errorf("state mismatch (possible CSRF)")}
			http.Error(w, "Invalid state. You may close this window.", http.StatusBadRequest)
			return
		}
		resultCh <- callbackResult{code: q.Get("code")}
		fmt.Fprintln(w, "Calyx authentication complete. You may close this window.")
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()

	authURL := conf.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier))
	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open a browser automatically. Open this URL to continue:\n\n%s\n\n", authURL)
	} else {
		fmt.Fprintln(os.Stderr, "Your browser has been opened to complete Google sign-in...")
	}

	var code string
	select {
	case res := <-resultCh:
		if res.err != nil {
			return "", res.err
		}
		code = res.code
	case <-ctx.Done():
		return "", fmt.Errorf("timed out waiting for Google sign-in: %w", ctx.Err())
	}

	token, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", fmt.Errorf("no id_token in token response (ensure the 'openid' scope is granted)")
	}
	return rawIDToken, nil
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
```

### Step 3 — Documentation & config files

- **`.env.example`** (repo root, committed):
  ```dotenv
  # Google OAuth 2.0 "Desktop app" client credentials for `calyx auth login`.
  # Create a "Desktop app" OAuth client, then copy this file to `.env` (git-ignored) and
  # fill in real values. Desktop-app clients accept loopback redirects without registering
  # a redirect URI, so no "Authorized redirect URI" setup is required.
  CALYX_GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
  CALYX_GOOGLE_CLIENT_SECRET=your-client-secret

  # Optional: fixed loopback host:port for the OAuth callback server (default 127.0.0.1:8765).
  # A fixed port is used only for devcontainer port forwarding; any free loopback port works.
  CALYX_OAUTH_REDIRECT_ADDR=127.0.0.1:8765
  ```
- **`.gitignore`**: append a line `.env` (keep existing `.local/`, `.antigravitycli/`, `bin/`).

### Step 4 — Dependencies

From the repo root:
```bash
go get golang.org/x/oauth2@latest
go get github.com/joho/godotenv@latest
go mod tidy
```
This adds `golang.org/x/oauth2` and `github.com/joho/godotenv` to `go.mod`/`go.sum` (plus any
transitive indirects pulled by `go mod tidy`).

## Verification

1. **Build** — `just build`; confirm `bin/calyx` is (re)generated without errors.
2. **Missing config** — with no `.env` and the vars unset:
   ```bash
   env -u CALYX_GOOGLE_CLIENT_ID -u CALYX_GOOGLE_CLIENT_SECRET ./bin/calyx auth login
   ```
   Expect a `stderr` error naming `CALYX_GOOGLE_CLIENT_ID`; non-zero exit.
3. **Usage / unknown subcommand**:
   ```bash
   ./bin/calyx auth          # usage to stderr, non-zero exit
   ./bin/calyx auth bogus    # "unknown command", non-zero exit
   ./bin/calyx               # top-level usage now lists `auth login`
   ```
4. **Successful login** (needs a real Desktop-app OAuth client — no redirect-URI
   registration required — and a populated `.env`):
   ```bash
   ./bin/calyx auth login
   ```
   Expect: the browser opens the Google consent screen (or the URL is printed to stderr),
   a "you may close this window" page after consent, the Google ID token printed to stdout,
   exit `0`. *(Inside the devcontainer this requires forwarding port 8765 host→container;
   otherwise run on the host — see note below.)*
5. **Timeout** — start the flow and do not complete sign-in within 3 minutes. Expect a clear
   timeout error on stderr, non-zero exit, and no lingering loopback server (the `defer
   srv.Shutdown` releases the port; re-running `auth login` must not hit "address in use").

## Security & Future Work Notes

- Printing the token to stdout is **development-only** and must be removed once the backend
  exchange + credential-store persistence are implemented.
- `state` is validated and PKCE is used to mitigate authorization-code interception and CSRF.
- Never commit real credentials: `.env` stays git-ignored; `.env.example` documents the keys.
- **Devcontainer:** Google redirects the *host* browser to `127.0.0.1:8765`. To complete the
  flow with the CLI running in the container, forward port 8765 host→container (e.g.
  `forwardPorts` in `.devcontainer/devcontainer.json`); otherwise run `calyx auth login` on
  the host. Deferred per the issue until a first working implementation exists.
- A follow-up issue will forward the Google token to the backend `AuthService.Login` RPC,
  exchange it for a session JWT, and store that JWT in the OS credential store.
