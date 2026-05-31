# Technical Specifications

This document outlines the technical specifications for the CLI and backend system, designed for integration with AI agents.

## Application Architecture

- **CLI (Client)**: A command-line tool that runs on the user's local PC, operated by users and AI agents.
- **Backend (Server)**: A remote server that handles authentication, verification, and data persistence.

## Tech Stack & Environment

| Component / Category | Specification |
| --- | --- |
| **Client OS** | Windows 11 (Linux used during development) |
| **Programming Language** | Go (Golang) |
| **Authentication Provider** | Google Authentication |
| **Communication Protocol** | gRPC |

## Authentication Flow

### 1. Initial Authentication (One-time Setup)

1. **CLI**: Requests the user to perform Google Authentication via a web browser.
2. **CLI -> Backend**: Sends the Google-issued authentication code (or ID token) along with the client terminal's identification information to the backend.
3. **Backend**: Verifies the Google token using Google's public keys.
4. **Backend**: Generates a new system-specific "short-lived session token" (JWT).
5. **Backend -> CLI**: Sends the issued session token along with its expiration information back to the CLI.
6. **CLI**: Saves the received session token into the local credential store (e.g., Windows Credential Manager).

### 2. Request Handling (Subsequent Operations)

1. **CLI -> Backend**: For each request, the CLI sets the session token as metadata in the gRPC header.
2. **Backend**: Integrates a gRPC interceptor (middleware) to intercept and verify the session token.
3. **Backend (Middleware)**: Extracts user information (roles, permissions) from the session token and passes it to the request handler.
4. **Backend (Middleware)**: If token verification fails, halts further request processing and returns an authentication error to the CLI.
5. **CLI**: Upon receiving an authentication error, deletes the corresponding session token from the local credential store (Credential Manager on Windows / Secret Service on Linux), prints a message prompting the user to re-authenticate (by launching a browser), and exits.

## Session Token Specification

*   **Format**: JSON Web Token (JWT).
*   **Payload**: Contains expiration information and minimal user data (such as roles and authorizations).
*   **Verification**: The backend performs stateless verification of the token on each request without using external storage.

## Directory Structure

*   [`apps/cli`]: The command-line interface tool (the front-end operated by AI agents).
*   [`apps/backend`]: The remote server handling authentication, verification, and persistence.
*   [`shared/proto`]: Protocol Buffer definition files and the auto-generated Go code.
