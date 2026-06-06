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

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of calyx:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nCommands:\n")
		fmt.Fprintf(os.Stderr, "  sample hello <name>   greet <name> via the backend\n")
	}

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

func dispatch(args []string) error {
	switch args[0] {
	case "sample":
		return runSample(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "calyx: unknown command %q\n", args[0])
		return errUsage
	}
}

func runSample(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: calyx sample hello <name>")
		return errUsage
	}
	switch args[0] {
	case "hello":
		return runHello(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "calyx sample: unknown command %q\n", args[0])
		return errUsage
	}
}

func runHello(args []string) error {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: calyx sample hello <name>")
		return errUsage
	}
	name := args[0]

	conn, err := grpc.NewClient(backendAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to backend: %w", err)
	}
	defer conn.Close()

	client := calyxv1.NewSampleServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	resp, err := client.Hello(ctx, &calyxv1.HelloRequest{Name: name})
	if err != nil {
		return fmt.Errorf("gRPC call failed: %w", err)
	}

	fmt.Println(resp.GetMessage())
	return nil
}

// backendAddr returns CALYX_BACKEND_ADDR, or defaultBackendAddr when unset/empty.
func backendAddr() string {
	if addr := os.Getenv("CALYX_BACKEND_ADDR"); addr != "" {
		return addr
	}
	return defaultBackendAddr
}
