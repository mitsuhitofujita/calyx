# ISSUE-001: Implement Sample Hello RPC in Backend

## Status
- **Status**: Open / Ready for Development
- **Priority**: High (Blocking initial environment validation)
- **Assignee**: AI Agent / Developer

## Objective
Implement a sample gRPC service and RPC endpoint in the backend (`apps/backend`) using Go. This serves as a Proof of Concept (PoC) to verify the gRPC code generation setup, network listener configuration, and message serialization/deserialization before implementing the main authentication service.

## Background
According to [intent.md](file:///home/mitsuhito/repos/github/calyx/docs/intent.md) and [spec.md](file:///home/mitsuhito/repos/github/calyx/docs/spec.md), Calyx communicates between the client CLI (`apps/cli`) and the remote server backend (`apps/backend`) using the gRPC protocol. Before implementing complex Google OAuth2 integrations and token credential storage workflows, we need to ensure the basic gRPC pipeline works.

## Technical Specifications

### Service Details
- **gRPC Package**: `mitsuhitofujita.calyx.v1`
- **Service Name**: `SampleService`
- **RPC Method**: `Hello`
- **Full gRPC Path**: `/mitsuhitofujita.calyx.v1.SampleService/Hello`

### Message Schema

#### 1. Request Message (`HelloRequest`)
- `name` (type: `string`): The name of the user or caller.

```protobuf
message HelloRequest {
  string name = 1;
}
```

#### 2. Response Message (`HelloResponse`)
- `message` (type: `string`): The greeting response message.
- **Format**: Must be exactly `"Hello, {$Name}."` where `{$Name}` is the value of the `name` field from the request.

```protobuf
message HelloResponse {
  string message = 1;
}
```

## Directory and File Mapping
The implementation involves files in the following directories:
- `shared/proto/`: Protocol Buffer definitions.
  - Recommended path: `shared/proto/mitsuhitofujita/calyx/v1/sample.proto`
- `apps/backend/`: Server-side application implementation.
  - Recommended path for implementation: `apps/backend/internal/server/sample.go` (or inline inside the main server setup)
  - Recommended path for entry point: `apps/backend/main.go`

## Implementation Steps

### Step 1: Define Protocol Buffers
Create the `.proto` file under `shared/proto/mitsuhitofujita/calyx/v1/sample.proto` with the following structure:
```protobuf
syntax = "proto3";

package mitsuhitofujita.calyx.v1;

option go_package = "github.com/mitsuhitofujita/calyx/shared/proto/mitsuhitofujita/calyx/v1;calyxv1";

service SampleService {
  rpc Hello (HelloRequest) returns (HelloResponse);
}

message HelloRequest {
  string name = 1;
}

message HelloResponse {
  string message = 1;
}
```
*(Note: Adjust the `go_package` option path depending on the actual Go module name defined in `go.mod` if present).*

### Step 2: gRPC Code Generation
Configure code generation using `protoc` or `buf` to compile `.proto` files into Go code inside `shared/proto/mitsuhitofujita/calyx/v1/`.
The generated output should include:
- `sample.pb.go`
- `sample_grpc.pb.go`

### Step 3: Implement `SampleServiceServer` in the Backend
1. Create/extend the backend codebase to implement the gRPC server interface generated in Step 2.
2. The `Hello` method implementation should retrieve `HelloRequest.Name`, format the string as `"Hello, " + req.Name + "."`, and return it in a `HelloResponse` struct.

### Step 4: Setup Server Entrypoint
1. Set up a TCP listener on a standard development port (e.g., `:50051`).
2. Initialize the gRPC server, register the implemented `SampleService`, and start listening.

## Verification and Testing Plan

To verify this issue is successfully implemented, the following tests must pass:

### 1. Manual Verification with `grpcurl`
Start the backend server on `localhost:50051`, and run the following command:
```bash
grpcurl -plaintext -d '{"name": "World"}' localhost:50051 mitsuhitofujita.calyx.v1.SampleService/Hello
```

**Expected Output**:
```json
{
  "message": "Hello, World."
}
```

### 2. Unit/Integration Tests
Provide a unit test in Go (e.g., `sample_test.go`) that starts an in-memory gRPC server, connects via a gRPC client, calls `Hello` with various inputs, and asserts the response format.
- **Test Case 1**: Input `""` (Empty string) -> Expected response `"Hello, ."` (or custom error handling if empty names are disallowed).
- **Test Case 2**: Input `"Alice"` -> Expected response `"Hello, Alice."`.
