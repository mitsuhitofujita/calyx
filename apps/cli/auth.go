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
	// defaultRedirectAddr is the fixed loopback host:port for the OAuth callback
	// server. A "Desktop app" OAuth client accepts any loopback redirect without
	// pre-registering it in the Google Cloud console (per RFC 8252 / Google's
	// installed-app flow), so we keep a fixed port purely so it can be forwarded
	// host->container in the devcontainer. 127.0.0.1 is Google's documented
	// loopback form (localhost can trip some firewalls).
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

// runAuthLogin runs the browser-based Google OAuth flow and prints the obtained
// Google ID token to stdout.
//
// DEVELOPMENT-ONLY: printing the token is a temporary, verification-only measure
// for this phase. It MUST be removed once the backend AuthService.Login exchange
// and credential-store persistence land (see docs/issues/005-add-cli-google-auth-login.md).
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
	// AuthService.Login exchange and credential-store persistence land.
	fmt.Println(idToken)
	return nil
}

// callbackResult carries the outcome of the OAuth redirect from the loopback
// HTTP handler back to the main flow.
type callbackResult struct {
	code string
	err  error
}

// authorize runs the loopback OAuth Authorization Code flow (with PKCE + state)
// and returns the Google id_token.
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
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

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

// randomState returns a URL-safe random string used as the OAuth state value.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// openBrowser opens url in the user's default browser, choosing the launcher by OS.
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
