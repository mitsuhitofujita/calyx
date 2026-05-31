// Command backend starts the Calyx gRPC backend server.
package main

import (
	"log"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/mitsuhitofujita/calyx/apps/backend/internal/server"
	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

// defaultAddr is the listen address used when CALYX_BACKEND_ADDR is unset.
const defaultAddr = ":50051"

func main() {
	addr := os.Getenv("CALYX_BACKEND_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("backend: failed to listen on %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer()
	calyxv1.RegisterSampleServiceServer(grpcServer, server.NewSampleServer())

	// Enable server reflection so tools like grpcurl can discover services
	// without a local copy of the .proto files.
	reflection.Register(grpcServer)

	log.Printf("backend: listening on %s", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("backend: failed to serve: %v", err)
	}
}
