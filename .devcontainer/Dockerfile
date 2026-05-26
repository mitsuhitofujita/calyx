FROM golang:1.26.3-trixie

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        curl \
        git \
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
