package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

// recordingAuthServer is an AuthService stub that records the incoming request
// metadata and returns a configurable StatusResponse (and optional error, to
// exercise the client's RPC-error mapping).
type recordingAuthServer struct {
	calyxv1.UnimplementedAuthServiceServer
	gotMD metadata.MD
	resp  *calyxv1.StatusResponse
	err   error
}

func (s *recordingAuthServer) Status(ctx context.Context, _ *calyxv1.StatusRequest) (*calyxv1.StatusResponse, error) {
	s.gotMD, _ = metadata.FromIncomingContext(ctx)
	return s.resp, s.err
}

// registerAuthStub serves the given stub on an in-memory bufconn listener and
// returns a connected AuthService client. Cleanup is registered on the test.
func registerAuthStub(t *testing.T, stub *recordingAuthServer) calyxv1.AuthServiceClient {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	calyxv1.RegisterAuthServiceServer(srv, stub)

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

// newTestAuthClient spins up a recording stub returning resp and returns a
// connected client plus the stub (for metadata assertions).
func newTestAuthClient(t *testing.T, resp *calyxv1.StatusResponse) (calyxv1.AuthServiceClient, *recordingAuthServer) {
	t.Helper()

	stub := &recordingAuthServer{resp: resp}
	return registerAuthStub(t, stub), stub
}

func TestFetchStatus_AttachesBearerAndRenders(t *testing.T) {
	expires := time.Date(2026, 6, 7, 12, 34, 56, 0, time.UTC)
	client, stub := newTestAuthClient(t, &calyxv1.StatusResponse{
		Authenticated: true,
		Message:       "session is valid",
		Session: &calyxv1.SessionInfo{
			Name:        "Alice Example",
			Email:       "alice@example.com",
			Role:        "admin",
			Permissions: []string{"*"},
			ExpiresAt:   timestamppb.New(expires),
		},
	})

	const jwt = "header.payload.signature"
	resp, err := fetchStatus(client, jwt)
	if err != nil {
		t.Fatalf("fetchStatus: %v", err)
	}

	if got := stub.gotMD.Get("authorization"); len(got) != 1 || got[0] != "Bearer "+jwt {
		t.Errorf("authorization metadata = %v, want [%q]", got, "Bearer "+jwt)
	}

	out := formatStatus(resp)
	for _, want := range []string{
		"authenticated",
		"Alice Example",
		"alice@example.com",
		"Role:        admin",
		"Permissions: *",
		"Expires:     " + expires.Format(time.RFC3339),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatStatus output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestFormatStatus_NotAuthenticated(t *testing.T) {
	out := formatStatus(&calyxv1.StatusResponse{
		Authenticated: false,
		Message:       "session token is invalid or expired",
	})
	for _, want := range []string{
		"not authenticated",
		"session token is invalid or expired",
		"calyx auth login",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatStatus output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestFormatStatus_OmitsEmptyEmail(t *testing.T) {
	out := formatStatus(&calyxv1.StatusResponse{
		Authenticated: true,
		Session: &calyxv1.SessionInfo{
			Name:        "Alice Example",
			Role:        "admin",
			Permissions: []string{"*"},
			ExpiresAt:   timestamppb.New(time.Now()),
		},
	})
	if strings.Contains(out, "Email:") {
		t.Errorf("formatStatus rendered an Email line for an empty email\ngot:\n%s", out)
	}
}

func TestRunAuthStatus_NoTokenShortCircuit(t *testing.T) {
	t.Setenv("CALYX_CONFIG_DIR", t.TempDir())

	// No token saved: runAuthStatus must take the ErrNoToken path and return nil
	// without contacting a backend (none is running in this test).
	if err := runAuthStatus(nil); err != nil {
		t.Fatalf("runAuthStatus with no token = %v, want nil", err)
	}
}

func TestRunAuthStatus_BadStoreConfig(t *testing.T) {
	t.Setenv("CALYX_CONFIG_DIR", t.TempDir())
	t.Setenv("CALYX_TOKEN_STORE", "bogus")

	if err := runAuthStatus(nil); err == nil {
		t.Fatal("runAuthStatus succeeded with a misconfigured store, want hard-fail error")
	}
}

func TestRunAuthStatus_RejectsExtraArgs(t *testing.T) {
	if err := runAuthStatus([]string{"extra"}); !errors.Is(err, errUsage) {
		t.Fatalf("runAuthStatus with extra args = %v, want errUsage", err)
	}
}

// TestRunAuthLogin_RejectsExtraArgs covers the runAuthLogin arity guard: extra
// args return errUsage before any OAuth/browser interaction.
func TestRunAuthLogin_RejectsExtraArgs(t *testing.T) {
	if err := runAuthLogin([]string{"extra"}); !errors.Is(err, errUsage) {
		t.Fatalf("runAuthLogin with extra args = %v, want errUsage", err)
	}
}

// TestRunAuthLogin_MissingConfig covers the config-validation branches: with the
// arity check passed, missing CALYX_GOOGLE_CLIENT_ID / CALYX_GOOGLE_CLIENT_SECRET
// produce an actionable error before any store resolution or browser launch.
// The vars are set explicitly (godotenv never overrides an already-set var), so
// the result is deterministic regardless of any .env on disk.
func TestRunAuthLogin_MissingConfig(t *testing.T) {
	cases := []struct {
		name         string
		clientID     string
		clientSecret string
		wantContains string
	}{
		{"missing client id", "", "", "CALYX_GOOGLE_CLIENT_ID"},
		{"missing client secret", "x", "", "CALYX_GOOGLE_CLIENT_SECRET"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CALYX_GOOGLE_CLIENT_ID", tc.clientID)
			t.Setenv("CALYX_GOOGLE_CLIENT_SECRET", tc.clientSecret)

			err := runAuthLogin(nil)
			if err == nil {
				t.Fatalf("runAuthLogin = nil, want error containing %q", tc.wantContains)
			}
			if !strings.Contains(err.Error(), tc.wantContains) {
				t.Errorf("runAuthLogin error = %q, want substring %q", err.Error(), tc.wantContains)
			}
		})
	}
}

// TestFetchStatus_RPCError covers the Status RPC-error mapping: a transport/RPC
// failure is wrapped with the "backend status check failed" context.
func TestFetchStatus_RPCError(t *testing.T) {
	client := registerAuthStub(t, &recordingAuthServer{err: errors.New("backend down")})

	_, err := fetchStatus(client, "header.payload.signature")
	if err == nil {
		t.Fatal("fetchStatus succeeded, want error from the failing Status RPC")
	}
	if !strings.Contains(err.Error(), "backend status check failed") {
		t.Errorf("fetchStatus error = %q, want substring %q", err.Error(), "backend status check failed")
	}
}
