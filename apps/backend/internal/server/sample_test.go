package server

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

// newTestClient spins up an in-memory gRPC server backed by bufconn and returns a
// connected SampleService client. Cleanup is registered on the test.
func newTestClient(t *testing.T) calyxv1.SampleServiceClient {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	calyxv1.RegisterSampleServiceServer(srv, NewSampleServer())

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

	return calyxv1.NewSampleServiceClient(conn)
}

func TestSampleServer_Hello(t *testing.T) {
	client := newTestClient(t)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty name", in: "", want: "Hello, ."},
		{name: "regular name", in: "Alice", want: "Hello, Alice."},
		{name: "issue example", in: "World", want: "Hello, World."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Hello(context.Background(), &calyxv1.HelloRequest{Name: tt.in})
			if err != nil {
				t.Fatalf("Hello(%q) returned error: %v", tt.in, err)
			}
			if resp.GetMessage() != tt.want {
				t.Errorf("Hello(%q) = %q, want %q", tt.in, resp.GetMessage(), tt.want)
			}
		})
	}
}
