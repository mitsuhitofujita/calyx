FROM golang:1.26.3-trixie

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        curl \
        git \
        just \
        ripgrep
RUN apt-get autoremove -y && \
    apt-get clean && \
	rm -rf /var/lib/apt/lists/*

ARG VSCODE_UID=1000
ARG VSCODE_USER=vscode
ARG VSCODE_GID=1000
ARG VSCODE_GROUP=vscode
ARG VSCODE_HOME=/home/vscode
ARG WORKSPACE_DIR=/workspace
RUN groupadd -g ${VSCODE_GID} ${VSCODE_GROUP} && \
    useradd -g ${VSCODE_GROUP} -u ${VSCODE_UID} -d ${VSCODE_HOME} -m ${VSCODE_USER} && \
    mkdir -p ${WORKSPACE_DIR}

WORKDIR ${WORKSPACE_DIR}

USER ${VSCODE_USER}

# Proto / gRPC toolchain (binaries land in /go/bin, which is on PATH).
# Run these as the non-root user so the Go module cache under /go/pkg/mod is
# owned by (and writable by) the runtime user. If installed as root, `go build`
# / `just build` cannot fetch new modules at runtime (permission denied on the
# module cache). Keep these versions in sync with the justfile.
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2 && \
    go install github.com/bufbuild/buf/cmd/buf@v1.70.0 && \
    go install github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.9.3

RUN curl -fsSL https://claude.ai/install.sh | bash
