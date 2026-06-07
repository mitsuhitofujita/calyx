package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

// stubVerifier is a googleIDTokenVerifier that returns a fixed result, so tests
// never reach Google's network endpoints.
type stubVerifier struct {
	user googleUser
	err  error
}

func (s stubVerifier) Verify(_ context.Context, _ string) (googleUser, error) {
	return s.user, s.err
}

var (
	testSigningKey = []byte("test-signing-key")
	testNow        = time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	testUser       = googleUser{Subject: "1098xxxxxxx4321", Email: "user@example.com", Name: "Mitsuhito Fujita"}
)

func testAuthConfig() AuthConfig {
	return AuthConfig{
		GoogleClientID: "test-client-id.apps.googleusercontent.com",
		SigningKey:     testSigningKey,
		Issuer:         "calyx-backend",
		Audience:       "calyx-cli",
		TTL:            time.Hour,
	}
}

// newTestAuthClient spins up an in-memory gRPC server backed by bufconn with an
// AuthServer using the supplied (stub) verifier and a fixed clock, and returns a
// connected AuthService client. Cleanup is registered on the test.
func newTestAuthClient(t *testing.T, cfg AuthConfig, v googleIDTokenVerifier) calyxv1.AuthServiceClient {
	t.Helper()

	authServer := newAuthServer(cfg, v)
	authServer.now = func() time.Time { return testNow }

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	calyxv1.RegisterAuthServiceServer(srv, authServer)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Errorf("test server exited: %v", err)
		}
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial bufconn: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})

	return calyxv1.NewAuthServiceClient(conn)
}

// parseSessionClaims parses and verifies a session token signed with the test key.
func parseSessionClaims(t *testing.T, token string) sessionClaims {
	t.Helper()

	var claims sessionClaims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return testSigningKey, nil
	}, jwt.WithTimeFunc(func() time.Time { return testNow })) // pin expiry to the fixture clock
	if err != nil {
		t.Fatalf("failed to parse session token: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("parsed session token is not valid")
	}
	return claims
}

func TestAuthServer_Login_HappyPath(t *testing.T) {
	cfg := testAuthConfig()
	client := newTestAuthClient(t, cfg, stubVerifier{user: testUser})

	resp, err := client.Login(context.Background(), &calyxv1.LoginRequest{
		GoogleCredential: &calyxv1.LoginRequest_IdToken{IdToken: "valid-google-id-token"},
	})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if resp.GetSessionToken() == "" {
		t.Fatal("Login returned empty session_token")
	}

	wantExp := testNow.Add(cfg.TTL)
	if got := resp.GetExpiresAt().AsTime(); !got.Equal(wantExp) {
		t.Errorf("expires_at = %v, want %v", got, wantExp)
	}

	claims := parseSessionClaims(t, resp.GetSessionToken())
	if claims.Role != "admin" {
		t.Errorf("role = %q, want %q", claims.Role, "admin")
	}
	if len(claims.Permissions) != 1 || claims.Permissions[0] != "*" {
		t.Errorf("permissions = %v, want [*]", claims.Permissions)
	}
	if claims.Email != testUser.Email {
		t.Errorf("email = %q, want %q", claims.Email, testUser.Email)
	}
	if claims.Name != testUser.Name {
		t.Errorf("name = %q, want %q", claims.Name, testUser.Name)
	}
	if claims.Subject != testUser.Subject {
		t.Errorf("sub = %q, want %q", claims.Subject, testUser.Subject)
	}
	if claims.Issuer != cfg.Issuer {
		t.Errorf("iss = %q, want %q", claims.Issuer, cfg.Issuer)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != cfg.Audience {
		t.Errorf("aud = %v, want [%s]", claims.Audience, cfg.Audience)
	}
}

func TestAuthServer_Login_InvalidGoogleToken(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{err: errors.New("invalid token")})

	resp, err := client.Login(context.Background(), &calyxv1.LoginRequest{
		GoogleCredential: &calyxv1.LoginRequest_IdToken{IdToken: "bad-google-id-token"},
	})
	if err == nil {
		t.Fatalf("Login succeeded unexpectedly, resp = %v", resp)
	}
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Errorf("status code = %v, want %v", got, codes.Unauthenticated)
	}
	if resp.GetSessionToken() != "" {
		t.Errorf("expected no session_token, got %q", resp.GetSessionToken())
	}
}

func TestAuthServer_Login_AuthCodeUnimplemented(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	_, err := client.Login(context.Background(), &calyxv1.LoginRequest{
		GoogleCredential: &calyxv1.LoginRequest_AuthCode{AuthCode: "some-auth-code"},
	})
	if err == nil {
		t.Fatal("Login succeeded unexpectedly for auth_code request")
	}
	if got := status.Code(err); got != codes.Unimplemented {
		t.Errorf("status code = %v, want %v", got, codes.Unimplemented)
	}
}

func TestAuthServer_Login_EmptyRequest(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	_, err := client.Login(context.Background(), &calyxv1.LoginRequest{})
	if err == nil {
		t.Fatal("Login succeeded unexpectedly for empty request")
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("status code = %v, want %v", got, codes.InvalidArgument)
	}
}

func TestAuthServer_Login_ExpiryConsistency(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	resp, err := client.Login(context.Background(), &calyxv1.LoginRequest{
		GoogleCredential: &calyxv1.LoginRequest_IdToken{IdToken: "valid-google-id-token"},
	})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	claims := parseSessionClaims(t, resp.GetSessionToken())
	if claims.ExpiresAt == nil {
		t.Fatal("session token has no exp claim")
	}
	if gotExp, respExp := claims.ExpiresAt.Unix(), resp.GetExpiresAt().AsTime().Unix(); gotExp != respExp {
		t.Errorf("JWT exp (%d) != LoginResponse.expires_at (%d)", gotExp, respExp)
	}
}

// sessionTokenOpts parameterizes mintSessionToken so cases can vary the fields
// that verification checks. The zero value yields a valid default token.
type sessionTokenOpts struct {
	signingKey []byte    // defaults to testSigningKey
	issuer     string    // defaults to "calyx-backend"
	audience   string    // defaults to "calyx-cli"
	expiresAt  time.Time // defaults to testNow.Add(time.Hour)
}

// mintSessionToken signs a session JWT the way Login does, applying any
// overrides in opts. It is used to construct the Status test inputs.
func mintSessionToken(t *testing.T, opts sessionTokenOpts) string {
	t.Helper()

	key := opts.signingKey
	if key == nil {
		key = testSigningKey
	}
	issuer := opts.issuer
	if issuer == "" {
		issuer = "calyx-backend"
	}
	audience := opts.audience
	if audience == "" {
		audience = "calyx-cli"
	}
	exp := opts.expiresAt
	if exp.IsZero() {
		exp = testNow.Add(time.Hour)
	}

	claims := sessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			Subject:   testUser.Subject,
			IssuedAt:  jwt.NewNumericDate(testNow),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		Email:       testUser.Email,
		Name:        testUser.Name,
		Role:        placeholderRole,
		Permissions: placeholderPermissions,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign session token: %v", err)
	}
	return signed
}

// statusContext returns an outgoing context carrying the session token as
// "authorization: Bearer <token>" metadata, mirroring what the CLI attaches.
func statusContext(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

func TestAuthServer_Status_ValidSession(t *testing.T) {
	cfg := testAuthConfig()
	client := newTestAuthClient(t, cfg, stubVerifier{user: testUser})

	exp := testNow.Add(time.Hour)
	token := mintSessionToken(t, sessionTokenOpts{expiresAt: exp})

	resp, err := client.Status(statusContext(token), &calyxv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !resp.GetAuthenticated() {
		t.Fatalf("authenticated = false, want true (message=%q)", resp.GetMessage())
	}
	if resp.GetMessage() != "session is valid" {
		t.Errorf("message = %q, want %q", resp.GetMessage(), "session is valid")
	}

	sess := resp.GetSession()
	if sess == nil {
		t.Fatal("session is nil, want populated SessionInfo")
	}
	if sess.GetName() != testUser.Name {
		t.Errorf("session.name = %q, want %q", sess.GetName(), testUser.Name)
	}
	if sess.GetEmail() != testUser.Email {
		t.Errorf("session.email = %q, want %q", sess.GetEmail(), testUser.Email)
	}
	if sess.GetRole() != "admin" {
		t.Errorf("session.role = %q, want %q", sess.GetRole(), "admin")
	}
	if perms := sess.GetPermissions(); len(perms) != 1 || perms[0] != "*" {
		t.Errorf("session.permissions = %v, want [*]", perms)
	}
	if got, want := sess.GetExpiresAt().AsTime().Unix(), exp.Truncate(time.Second).Unix(); got != want {
		t.Errorf("session.expires_at = %d, want %d", got, want)
	}
}

func TestAuthServer_Status_NoMetadata(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	resp, err := client.Status(context.Background(), &calyxv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if resp.GetAuthenticated() {
		t.Error("authenticated = true, want false")
	}
	if resp.GetMessage() != "no session token provided" {
		t.Errorf("message = %q, want %q", resp.GetMessage(), "no session token provided")
	}
	if resp.GetSession() != nil {
		t.Errorf("session = %v, want nil", resp.GetSession())
	}
}

func TestAuthServer_Status_MalformedToken(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	resp, err := client.Status(statusContext("not-a-jwt"), &calyxv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if resp.GetAuthenticated() {
		t.Error("authenticated = true, want false")
	}
	if resp.GetMessage() != "session token is invalid or expired" {
		t.Errorf("message = %q, want %q", resp.GetMessage(), "session token is invalid or expired")
	}
	if resp.GetSession() != nil {
		t.Errorf("session = %v, want nil", resp.GetSession())
	}
}

func TestAuthServer_Status_WrongSignature(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	token := mintSessionToken(t, sessionTokenOpts{signingKey: []byte("a-different-key")})

	resp, err := client.Status(statusContext(token), &calyxv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if resp.GetAuthenticated() {
		t.Error("authenticated = true, want false")
	}
	if resp.GetMessage() != "session token is invalid or expired" {
		t.Errorf("message = %q, want %q", resp.GetMessage(), "session token is invalid or expired")
	}
}

func TestAuthServer_Status_ExpiredToken(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	// Server clock is fixed at testNow; mint a token that already expired.
	token := mintSessionToken(t, sessionTokenOpts{expiresAt: testNow.Add(-time.Minute)})

	resp, err := client.Status(statusContext(token), &calyxv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if resp.GetAuthenticated() {
		t.Error("authenticated = true, want false")
	}
	if resp.GetMessage() != "session token is invalid or expired" {
		t.Errorf("message = %q, want %q", resp.GetMessage(), "session token is invalid or expired")
	}
}

func TestAuthServer_Status_WrongIssuer(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	token := mintSessionToken(t, sessionTokenOpts{issuer: "evil-issuer"})

	resp, err := client.Status(statusContext(token), &calyxv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if resp.GetAuthenticated() {
		t.Error("authenticated = true, want false")
	}
}

func TestAuthServer_Status_WrongAudience(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	token := mintSessionToken(t, sessionTokenOpts{audience: "someone-else"})

	resp, err := client.Status(statusContext(token), &calyxv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if resp.GetAuthenticated() {
		t.Error("authenticated = true, want false")
	}
}

// TestAuthServer_Status_LoginRoundTrip proves the issue→verify round-trip: a
// token freshly minted by Login is accepted by Status.
func TestAuthServer_Status_LoginRoundTrip(t *testing.T) {
	client := newTestAuthClient(t, testAuthConfig(), stubVerifier{user: testUser})

	login, err := client.Login(context.Background(), &calyxv1.LoginRequest{
		GoogleCredential: &calyxv1.LoginRequest_IdToken{IdToken: "valid-google-id-token"},
	})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	resp, err := client.Status(statusContext(login.GetSessionToken()), &calyxv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !resp.GetAuthenticated() {
		t.Fatalf("authenticated = false, want true (message=%q)", resp.GetMessage())
	}
	if got, want := resp.GetSession().GetExpiresAt().AsTime().Unix(), login.GetExpiresAt().AsTime().Unix(); got != want {
		t.Errorf("Status expires_at (%d) != Login expires_at (%d)", got, want)
	}
}
