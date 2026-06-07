// Command calyx is the Calyx sample CLI: a thin gRPC client of the backend.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

const (
	version            = "v0.0.0"
	defaultBackendAddr = "localhost:50051"
	dialTimeout        = 5 * time.Second
)

// errUsage signals that a handler has already written its usage message to
// stderr. main exits non-zero without printing it again.
var errUsage = errors.New("usage error")

func main() {
	versionFlag := flag.Bool("version", false, "print version information")

	// Derive help text from the registry so it cannot drift from what dispatch
	// runs or from `calyx schema`.
	flag.Usage = func() { commandRegistry().writeUsage(os.Stderr, nil) }

	flag.Parse()

	if *versionFlag {
		fmt.Println(version)
		os.Exit(0)
	}

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(0)
	}

	if err := dispatch(args); err != nil {
		if !errors.Is(err, errUsage) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

// dispatch routes args to a command via the shared registry and runs it. Routing
// (group walking, unknown/missing-subcommand usage) lives in resolve; the leaf
// handler validates its own arguments.
func dispatch(args []string) error {
	cmd, rest, err := commandRegistry().resolve(args)
	if err != nil {
		return err
	}
	return cmd.Run(rest)
}

func runHello(args []string) error {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: calyx sample hello <name>")
		return errUsage
	}
	name := args[0]

	conn, err := dialBackend()
	if err != nil {
		return err
	}
	defer conn.Close()

	msg, err := sayHello(calyxv1.NewSampleServiceClient(conn), name)
	if err != nil {
		return err
	}

	fmt.Println(msg)
	return nil
}

// dialBackend opens an insecure (dev) gRPC client conn to the backend; the caller
// is responsible for closing it.
func dialBackend() (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(backendAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to backend: %w", err)
	}
	return conn, nil
}

// withAuth attaches "authorization: Bearer <jwt>" when a session token is stored.
// A missing token (ErrNoToken) returns ctx unchanged plus a login hint; a
// misconfigured store or a load failure returns an error so the caller can
// hard-fail.
func withAuth(ctx context.Context) (context.Context, error) {
	store, err := NewTokenStore()
	if err != nil {
		return nil, err
	}
	tok, err := store.Load()
	if errors.Is(err, ErrNoToken) {
		fmt.Fprintln(os.Stderr, "hint: not logged in; run `calyx auth login` to authenticate.")
		return ctx, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not load session token: %w", err)
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok.Token), nil
}

// sayHello is the testable RPC unit: it applies withAuth, then calls Hello.
func sayHello(client calyxv1.SampleServiceClient, name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	ctx, err := withAuth(ctx)
	if err != nil {
		return "", err
	}

	resp, err := client.Hello(ctx, &calyxv1.HelloRequest{Name: name})
	if err != nil {
		return "", fmt.Errorf("gRPC call failed: %w", err)
	}
	return resp.GetMessage(), nil
}

// backendAddr returns CALYX_BACKEND_ADDR, or defaultBackendAddr when unset/empty.
func backendAddr() string {
	if addr := os.Getenv("CALYX_BACKEND_ADDR"); addr != "" {
		return addr
	}
	return defaultBackendAddr
}
