package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/grpc/metadata"

	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
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

	// backendCallTimeout bounds the dial+RPC to exchange the Google ID token for a
	// session token. It is independent of loginTimeout (which covers the interactive
	// browser sign-in).
	backendCallTimeout = 10 * time.Second
)

// runAuth dispatches `calyx auth` subcommands.
func runAuth(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: calyx auth <login|status>")
		return errUsage
	}
	switch args[0] {
	case "login":
		return runAuthLogin(args[1:])
	case "status":
		return runAuthStatus(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "calyx auth: unknown command %q\n", args[0])
		return errUsage
	}
}

// runAuthLogin runs the browser-based Google OAuth flow, exchanges the obtained
// Google ID token for a Calyx session token via AuthService.Login, persists the
// session token via the configured TokenStore, and prints a non-secret
// confirmation. The session JWT is never written to stdout/stderr.
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

	// Resolve the store before opening a browser so a misconfigured
	// CALYX_TOKEN_STORE fails fast without an interactive sign-in.
	store, err := NewTokenStore()
	if err != nil {
		return err
	}

	idToken, err := authorize(conf, redirectAddr)
	if err != nil {
		return err
	}

	sess, err := exchangeIDToken(idToken)
	if err != nil {
		return err
	}

	if err := store.Save(sess); err != nil {
		return fmt.Errorf("failed to save session token: %w", err)
	}

	fmt.Printf("Logged in. Session valid until %s.\n", sess.ExpiresAt.UTC().Format(time.RFC3339))
	return nil
}

// exchangeIDToken dials the backend and exchanges a Google ID token for a Calyx
// session token via AuthService.Login.
func exchangeIDToken(idToken string) (SessionToken, error) {
	conn, err := dialBackend()
	if err != nil {
		return SessionToken{}, err
	}
	defer conn.Close()

	client := calyxv1.NewAuthServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), backendCallTimeout)
	defer cancel()

	resp, err := client.Login(ctx, &calyxv1.LoginRequest{
		GoogleCredential: &calyxv1.LoginRequest_IdToken{IdToken: idToken},
	})
	if err != nil {
		return SessionToken{}, fmt.Errorf("backend login failed: %w", err)
	}
	return SessionToken{
		Token:     resp.GetSessionToken(),
		ExpiresAt: resp.GetExpiresAt().AsTime(),
	}, nil
}

// runAuthStatus reports whether the locally stored session token is valid. It
// loads the token, asks the backend to verify it (AuthService.Status), and prints
// the result. A missing local token is reported directly without a backend call.
// Both authenticated and not-authenticated are successful reports (exit 0); only
// operational failures (misconfigured store, load error, unreachable backend)
// return an error.
func runAuthStatus(args []string) error {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: calyx auth status")
		return errUsage
	}

	// Resolve the store before any network call so a misconfigured
	// CALYX_TOKEN_STORE fails fast.
	store, err := NewTokenStore()
	if err != nil {
		return err
	}

	tok, err := store.Load()
	if errors.Is(err, ErrNoToken) {
		// No local token: certainly not authenticated, so skip the backend.
		fmt.Println(formatStatus(&calyxv1.StatusResponse{Authenticated: false, Message: "not logged in"}))
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not load session token: %w", err)
	}

	conn, err := dialBackend()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := fetchStatus(calyxv1.NewAuthServiceClient(conn), tok.Token)
	if err != nil {
		return err
	}

	fmt.Println(formatStatus(resp))
	return nil
}

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

// formatStatus renders a StatusResponse as plain text for `calyx auth status`. It
// is a pure function (no I/O); the caller prints the returned string. The final
// line carries no trailing newline since the caller uses fmt.Println.
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
