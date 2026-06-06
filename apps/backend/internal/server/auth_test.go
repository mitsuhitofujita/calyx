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
	})
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
