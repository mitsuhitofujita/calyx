// Command backend starts the Calyx gRPC backend server.
package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/joho/godotenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/mitsuhitofujita/calyx/apps/backend/internal/server"
	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

// defaultAddr is the listen address used when CALYX_BACKEND_ADDR is unset.
const defaultAddr = ":50051"

// Defaults for the optional session-JWT configuration.
const (
	defaultJWTIssuer   = "calyx-backend"
	defaultJWTAudience = "calyx-cli"
	defaultSessionTTL  = time.Hour
)

func main() {
	// Load .env from the working directory (repo root) for config parity with
	// the CLI. Absence is fine; existing process env vars take precedence
	// (godotenv.Load never overrides them).
	_ = godotenv.Load()

	addr := os.Getenv("CALYX_BACKEND_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	authCfg, err := loadAuthConfig()
	if err != nil {
		log.Fatalf("backend: %v", err)
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("backend: failed to listen on %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer()
	calyxv1.RegisterSampleServiceServer(grpcServer, server.NewSampleServer())
	calyxv1.RegisterAuthServiceServer(grpcServer, server.NewAuthServer(authCfg))

	// Enable server reflection so tools like grpcurl can discover services
	// without a local copy of the .proto files.
	reflection.Register(grpcServer)

	log.Printf("backend: listening on %s", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("backend: failed to serve: %v", err)
	}
}

// loadAuthConfig reads the AuthService configuration from the environment,
// failing fast with an actionable error on a missing required value.
func loadAuthConfig() (server.AuthConfig, error) {
	clientID := os.Getenv("CALYX_GOOGLE_CLIENT_ID")
	if clientID == "" {
		return server.AuthConfig{}, fmt.Errorf("missing required configuration: CALYX_GOOGLE_CLIENT_ID (set it in .env)")
	}

	signingKey := os.Getenv("CALYX_JWT_SIGNING_KEY")
	if signingKey == "" {
		return server.AuthConfig{}, fmt.Errorf("missing required configuration: CALYX_JWT_SIGNING_KEY (set it in .env)")
	}

	issuer := os.Getenv("CALYX_JWT_ISSUER")
	if issuer == "" {
		issuer = defaultJWTIssuer
	}

	audience := os.Getenv("CALYX_JWT_AUDIENCE")
	if audience == "" {
		audience = defaultJWTAudience
	}

	ttl := defaultSessionTTL
	if raw := os.Getenv("CALYX_SESSION_TTL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return server.AuthConfig{}, fmt.Errorf("invalid CALYX_SESSION_TTL %q: %w", raw, err)
		}
		ttl = parsed
	}

	return server.AuthConfig{
		GoogleClientID: clientID,
		SigningKey:     []byte(signingKey),
		Issuer:         issuer,
		Audience:       audience,
		TTL:            ttl,
	}, nil
}
