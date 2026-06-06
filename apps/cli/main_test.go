package main

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

// recordingSampleServer is a SampleService stub that records the incoming request
// metadata and returns a fixed greeting.
type recordingSampleServer struct {
	calyxv1.UnimplementedSampleServiceServer
	gotMD metadata.MD
}

func (s *recordingSampleServer) Hello(ctx context.Context, _ *calyxv1.HelloRequest) (*calyxv1.HelloResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.gotMD = md
	return &calyxv1.HelloResponse{Message: "Hello, Alice."}, nil
}

// newTestSampleClient spins up the recording stub on an in-memory bufconn listener
// and returns a connected client plus the stub (for metadata assertions). Cleanup
// is registered on the test.
func newTestSampleClient(t *testing.T) (calyxv1.SampleServiceClient, *recordingSampleServer) {
	t.Helper()

	stub := &recordingSampleServer{}
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	calyxv1.RegisterSampleServiceServer(srv, stub)

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

	return calyxv1.NewSampleServiceClient(conn), stub
}

func TestSayHello_AttachesBearer(t *testing.T) {
	t.Setenv("CALYX_CONFIG_DIR", t.TempDir())
	client, stub := newTestSampleClient(t)

	store, err := NewTokenStore()
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	const jwt = "header.payload.signature"
	if err := store.Save(SessionToken{Token: jwt}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	msg, err := sayHello(client, "Alice")
	if err != nil {
		t.Fatalf("sayHello: %v", err)
	}
	if msg != "Hello, Alice." {
		t.Errorf("message = %q, want %q", msg, "Hello, Alice.")
	}

	got := stub.gotMD.Get("authorization")
	if len(got) != 1 || got[0] != "Bearer "+jwt {
		t.Errorf("authorization metadata = %v, want [%q]", got, "Bearer "+jwt)
	}
}

func TestSayHello_NoToken(t *testing.T) {
	t.Setenv("CALYX_CONFIG_DIR", t.TempDir())
	client, stub := newTestSampleClient(t)

	msg, err := sayHello(client, "Alice")
	if err != nil {
		t.Fatalf("sayHello: %v", err)
	}
	if msg != "Hello, Alice." {
		t.Errorf("message = %q, want %q", msg, "Hello, Alice.")
	}

	if got := stub.gotMD.Get("authorization"); len(got) != 0 {
		t.Errorf("authorization metadata = %v, want none (unauthenticated path)", got)
	}
}

func TestSayHello_BadStoreConfig(t *testing.T) {
	t.Setenv("CALYX_CONFIG_DIR", t.TempDir())
	t.Setenv("CALYX_TOKEN_STORE", "bogus")
	client, _ := newTestSampleClient(t)

	if _, err := sayHello(client, "Alice"); err == nil {
		t.Fatal("sayHello succeeded with a misconfigured store, want hard-fail error")
	}
}
