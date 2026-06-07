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
// metadata and returns a configurable StatusResponse.
type recordingAuthServer struct {
	calyxv1.UnimplementedAuthServiceServer
	gotMD metadata.MD
	resp  *calyxv1.StatusResponse
}

func (s *recordingAuthServer) Status(ctx context.Context, _ *calyxv1.StatusRequest) (*calyxv1.StatusResponse, error) {
	s.gotMD, _ = metadata.FromIncomingContext(ctx)
	return s.resp, nil
}

// newTestAuthClient spins up the recording stub on an in-memory bufconn listener
// and returns a connected client plus the stub (for metadata assertions). Cleanup
// is registered on the test.
func newTestAuthClient(t *testing.T, resp *calyxv1.StatusResponse) (calyxv1.AuthServiceClient, *recordingAuthServer) {
	t.Helper()

	stub := &recordingAuthServer{resp: resp}
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

	return calyxv1.NewAuthServiceClient(conn), stub
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
