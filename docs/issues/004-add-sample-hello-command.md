# ISSUE-004: Add Sample Hello Command to CLI

## Status
- **Status**: Open / Ready for Development
- **Priority**: High (Essential for verifying gRPC communication from the CLI)
- **Assignee**: AI Agent / Developer

## Objective
Implement the `calyx sample hello <name>` command inside the CLI application (`apps/cli`). This command must call the existing backend gRPC service `mitsuhitofujita.calyx.v1.SampleService.Hello` using the provided `<name>` as the request payload and print the returned greeting message.

## Background
According to "intent.md" and "spec.md", the CLI serves as a client that interacts with the backend server via gRPC. 
The next development item is adding a command to the sample CLI `calyx`:
- Create the `calyx sample hello {$name}` command and use the existing gRPC request.
- Output the `message` from the gRPC response.
- Note: Escaping/sanitizing the gRPC response or other security aspects are deferred for future issues.

## Technical Specifications

### CLI Syntax
```bash
calyx sample hello <name>
```

### Constraints & Edge Cases
1. **Argument Check**: 
   The `hello` subcommand requires exactly one argument representing the `<name>`. If the name is missing or if extra arguments are supplied, the command must output usage instructions to `stderr` and terminate with a non-zero exit code (e.g., `1`).
2. **Backend Server Address**:
   - The CLI must look up the backend server address using the environment variable `CALYX_BACKEND_ADDR`.
   - If `CALYX_BACKEND_ADDR` is not defined or is empty, it must default to `localhost:50051`.
3. **gRPC Connection & Protocol**:
   - Establish the connection using insecure credentials (`grpc.WithTransportCredentials(insecure.NewCredentials())`).
   - Call the `Hello` RPC method defined in "sample.proto".
   - Use a context with a timeout (e.g., 5 seconds) to ensure that command execution does not hang indefinitely if the backend is unreachable.
4. **Command Output**:
   - On success, output the `message` field from the `HelloResponse` directly to `stdout` with a trailing newline.
   - For example, if `<name>` is `Alice`, the output should be:
     ```
     Hello, Alice.
     ```
   - On failure (e.g., network error, backend unavailable, or RPC failure), output a clear error message to `stderr` (e.g., `Error: failed to connect to backend: <error_details>` or `Error: gRPC call failed: <error_details>`) and exit with a non-zero exit code.
5. **Security Defers**:
   - Escaping or sanitizing the response message from the server is not required in this phase, as per the specification.

## Directory and File Mapping
- "apps/cli/main.go" (Modify)

## Implementation Steps

### Step 1: Parse Subcommands in CLI
Update `apps/cli/main.go` to handle subcommand structures:
1. Parse global flags (e.g., `--version`).
2. Read positional arguments using `flag.Args()`.
3. Dispatch to `sample` subcommand handling if the first argument is `"sample"`.
4. Inside the `sample` subcommand, ensure the next argument is `"hello"`. If not, show an appropriate usage message.
5. Inside the `hello` subcommand, parse the `<name>` positional argument.

### Step 2: Establish gRPC Connection
1. Retrieve the backend address from the `CALYX_BACKEND_ADDR` environment variable (default: `localhost:50051`).
2. Initialize a connection using `grpc.Dial` (or `grpc.NewClient`) with `grpc.WithTransportCredentials(insecure.NewCredentials())`.

### Step 3: Invoke the gRPC Service and Print Results
1. Create a `SampleServiceClient` client using the connection.
2. Formulate a `HelloRequest` struct with the retrieved `<name>`.
3. Call `Hello` method with a timeout context.
4. Print the response's `Message` field to `stdout`.
5. Ensure resource cleanup (e.g. closing the gRPC connection) is handled correctly.

## Verification and Testing Plan

Perform the following steps to verify the implementation:

### 1. Start the Backend Server
Run the gRPC backend using `just`:
```bash
just run
```
Verify that the output indicates `backend: listening on :50051`.

### 2. Compile the CLI
In a separate terminal, compile the CLI and backend:
```bash
just build
```
Verify that `bin/calyx` is successfully generated.

### 3. Verify Success Flow
Run the CLI with a name argument:
```bash
./bin/calyx sample hello Alice
```
**Expected Output**:
```
Hello, Alice.
```
**Exit Code**: `0`

### 4. Verify Argument Constraints
Run the subcommand without a name:
```bash
./bin/calyx sample hello
```
**Expected Output**: An appropriate usage message showing that `<name>` is required printed to `stderr`.
**Exit Code**: `1` (or non-zero).

Run the subcommand with too many arguments:
```bash
./bin/calyx sample hello Alice Bob
```
**Expected Output**: Usage message or argument error printed to `stderr`.
**Exit Code**: `1` (or non-zero).

### 5. Verify Unreachable Backend Flow
Terminate the backend server (stop `just run`) and invoke the CLI command:
```bash
./bin/calyx sample hello Alice
```
**Expected Output**: A clear error message indicating a connection or timeout error printed to `stderr`.
**Exit Code**: `1` (or non-zero).
