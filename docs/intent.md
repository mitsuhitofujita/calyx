# CLI Development for AI Agent Collaboration

This document outlines the intent and background of the development of this repository.
The items marked within `<issue></issue>` tags represent undecided or unresolved matters at this stage.

## Background of Development

To collaborate with AI agents, we are developing a CLI that supports updating remote storage and executing various tools. This will enable a wide range of tasks using AI agents.
Since we expect to develop multiple CLIs tailored to different work units, the goal of this project is to implement a complete set of required features in a sample CLI, which will then serve as a GitHub template repository.

## Required Features

### Authentication Storage and Verification

In integration with Google Authentication, the CLI stores a "short-lived session token (JWT)" issued by the backend in the local OS credential store (keystore).
This session token is attached as metadata to gRPC headers for subsequent command executions and is verified by the backend (via gRPC interceptors).

During the initial authentication flow, the CLI requests Google Authentication via a web browser, sends the obtained authentication information to the backend, and retrieves the unique session token.
If the session token expires, the CLI does not perform automatic renewal using a refresh token. Upon detecting an authentication error, the CLI deletes the expired token from the credential store, displays a message prompting the user to re-authenticate (by launching a browser), and terminates.

Tokens are stored using the native credential stores provided by the operating system:
- Windows 11: Credential Manager
- Linux: Secret Service (via libsecret over D-Bus)
Note that macOS is currently out of scope.
Environments where a credential store is unavailable are not supported. Proprietary file encryption is not adopted (since storing the encryption key on the same filesystem does not improve security).

### Autonomous Auto-Update

Since the CLI is deployed on users' local PCs, version discrepancies can occur. To mitigate this, the CLI will automatically update itself to the latest version.
Older versions will be restricted to running only a subset of commands.

The update binaries will be distributed from an in-house managed server.
The private key used for signing will be managed in CI/CD Secrets and will not be stored on the distribution server. The public key will be embedded into the CLI binary at build time. If the signature verification fails before applying an update, the update process is aborted, and an error is returned.

The commands permitted on older versions are hardcoded on the client side, limited to the following three:
- Version check
- Help
- Auto-update

Auto-updates must be triggered explicitly by executing a command. Similar to authentication, it is assumed that users manually run this beforehand. Auto-updates will not interrupt an AI agent while it is running a command.

### Structured Metadata Output for Autonomous Learning

To enable AI agents to understand tool specifications accurately in advance, the CLI will provide a command to output the command hierarchy and expected arguments in a format like JSON Schema.

<issue>
While "a format like JSON Schema" is mentioned, we need to narrow down the target format. Depending on the purpose of integration, the appropriate format differs (e.g., OpenAPI, MCP (Model Context Protocol) tool schema, or structured man page/help outputs for CLIs).
It is also unclear whether the metadata output applies to all commands or only a subset. Additionally, the versioning policy when schemas change between versions is currently undefined.
</issue>

### Timeout and Orphan Process Prevention

Manage the maximum execution time for each process and terminate upon timeout.
To prevent child processes spawned in the background from remaining as zombies or running indefinitely on the system, lifecycle management must ensure all child processes are terminated when the parent process exits.

<issue>
It is undefined whether the timeout value is fixed per command or configurable via arguments or configuration files. If AI agents run long-running tasks, they need to be able to dynamically specify appropriate timeout values.
The exact method for terminating child processes (whether to send SIGTERM for graceful termination or SIGKILL for forced termination) and the implementation policy for signal propagation (e.g., process group management, cgroups) are unresolved.
</issue>

### Error Handling and Usage Telemetry/Visibility

To prevent accidental double-updates or other conflicts caused by AI retries, the design should accept a request ID or idempotency key as an argument.
At the same time, to monitor command usage, the CLI will include logging and telemetry transmission capabilities.

<issue>
The destination server (e.g., in-house server, analytics service) and the data schema (e.g., command name, arguments, execution duration) for the usage telemetry transmission are undefined. A privacy design is required if sensitive information (e.g., argument values, file paths) is transmitted. The ability for users to opt out of telemetry must also be clarified.
It is unclear whether the idempotency key is generated by the CLI or by the caller (AI agent). The retention period for the idempotency key on the server side also needs to be defined.
The logging output destination (stderr, files, structured logs, etc.) and format (text, JSON) are undefined. If the AI agent is expected to parse the logs, a machine-readable JSON format is preferable.
</issue>
