package server

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/api/idtoken"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

// Fixed placeholder authorization for this phase. Every successfully
// authenticated user receives admin role with full ("*") permissions. Real
// role/permission resolution from a user store is future work.
const (
	placeholderRole = "admin"
)

var placeholderPermissions = []string{"*"}

// AuthConfig holds the configuration AuthServer needs to verify Google ID
// tokens and mint Calyx session JWTs.
type AuthConfig struct {
	GoogleClientID string        // expected audience for the Google ID token
	SigningKey     []byte        // HMAC secret for the session JWT
	Issuer         string        // iss claim (default "calyx-backend")
	Audience       string        // aud claim (default "calyx-cli")
	TTL            time.Duration // session lifetime (default 1h)
}

// googleUser holds the verified fields extracted from a Google ID token.
type googleUser struct {
	Subject string
	Email   string
	Name    string
}

// googleIDTokenVerifier verifies a raw Google ID token and extracts the user
// fields. It is injected so tests don't call Google's network endpoints.
type googleIDTokenVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (googleUser, error)
}

// sessionClaims is the Calyx session JWT payload: standard registered claims
// (iss/aud/sub/iat/exp) plus basic user info and the fixed authorization.
type sessionClaims struct {
	jwt.RegisteredClaims
	Email       string   `json:"email,omitempty"`
	Name        string   `json:"name,omitempty"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

// AuthServer implements calyxv1.AuthServiceServer. It verifies a Google ID
// token and exchanges it for a short-lived HS256-signed Calyx session JWT.
type AuthServer struct {
	calyxv1.UnimplementedAuthServiceServer
	cfg      AuthConfig
	verifier googleIDTokenVerifier
	now      func() time.Time // injectable clock; defaults to time.Now
}

// NewAuthServer returns a ready-to-register AuthServer wired with the
// production Google ID-token verifier (audience = cfg.GoogleClientID).
func NewAuthServer(cfg AuthConfig) *AuthServer {
	return newAuthServer(cfg, googleVerifier{audience: cfg.GoogleClientID})
}

// newAuthServer is the test-friendly constructor that accepts a stub verifier.
func newAuthServer(cfg AuthConfig, v googleIDTokenVerifier) *AuthServer {
	return &AuthServer{cfg: cfg, verifier: v, now: time.Now}
}

// Login verifies the supplied Google credential and returns a Calyx session
// JWT together with its expiration.
func (s *AuthServer) Login(ctx context.Context, req *calyxv1.LoginRequest) (*calyxv1.LoginResponse, error) {
	switch cred := req.GetGoogleCredential().(type) {
	case *calyxv1.LoginRequest_IdToken:
		return s.loginWithIDToken(ctx, cred.IdToken)
	case *calyxv1.LoginRequest_AuthCode:
		return nil, status.Error(codes.Unimplemented, "authorization-code exchange is not implemented; send a Google id_token")
	default:
		return nil, status.Error(codes.InvalidArgument, "no google credential provided")
	}
}

// loginWithIDToken verifies a Google ID token and mints the session JWT.
func (s *AuthServer) loginWithIDToken(ctx context.Context, idToken string) (*calyxv1.LoginResponse, error) {
	if idToken == "" {
		return nil, status.Error(codes.InvalidArgument, "no google credential provided")
	}

	// Do not include the raw token in the error message; it must never be logged.
	user, err := s.verifier.Verify(ctx, idToken)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "google id token verification failed")
	}

	now := s.now()
	exp := now.Add(s.cfg.TTL)

	claims := sessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Audience:  jwt.ClaimStrings{s.cfg.Audience},
			Subject:   user.Subject,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		Email:       user.Email,
		Name:        user.Name,
		Role:        placeholderRole,
		Permissions: placeholderPermissions,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.cfg.SigningKey)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to sign session token")
	}

	// expires_at MUST equal the JWT exp claim. jwt.NewNumericDate truncates to
	// whole seconds, so mirror that here for an exact match.
	return &calyxv1.LoginResponse{
		SessionToken: signed,
		ExpiresAt:    timestamppb.New(exp.Truncate(time.Second)),
	}, nil
}

// googleVerifier is the production googleIDTokenVerifier. It validates the
// token's signature, issuer, audience, and expiry using Google's public keys
// (fetched and cached by the idtoken package).
type googleVerifier struct {
	audience string
}

func (v googleVerifier) Verify(ctx context.Context, raw string) (googleUser, error) {
	payload, err := idtoken.Validate(ctx, raw, v.audience)
	if err != nil {
		return googleUser{}, err
	}
	email, _ := payload.Claims["email"].(string)
	name, _ := payload.Claims["name"].(string)
	return googleUser{Subject: payload.Subject, Email: email, Name: name}, nil
}
