// Package server contains the gRPC service implementations for the Calyx backend.
package server

import (
	"context"

	calyxv1 "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1"
)

// SampleServer implements calyxv1.SampleServiceServer. It is a proof-of-concept
// service used to validate the gRPC pipeline before the real services are built.
type SampleServer struct {
	calyxv1.UnimplementedSampleServiceServer
}

// NewSampleServer returns a ready-to-register SampleServer.
func NewSampleServer() *SampleServer {
	return &SampleServer{}
}

// Hello returns a greeting formatted as "Hello, {name}." for the requested name.
func (s *SampleServer) Hello(_ context.Context, req *calyxv1.HelloRequest) (*calyxv1.HelloResponse, error) {
	return &calyxv1.HelloResponse{
		Message: "Hello, " + req.GetName() + ".",
	}, nil
}
