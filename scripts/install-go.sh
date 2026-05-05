#!/usr/bin/env bash
set -euo pipefail

GO_VERSION="1.24.1"
GO_ROOT="${HOME}/.go"
GO_TARBALL="/tmp/go${GO_VERSION}.linux-amd64.tar.gz"
XMOBILE_VERSION="v0.0.0-20250210185054-b38b8813d607"

if [[ -x "${GO_ROOT}/bin/go" ]] && "${GO_ROOT}/bin/go" version | grep -q "go${GO_VERSION}"; then
  exit 0
fi

curl -sSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o "${GO_TARBALL}"
rm -rf "${GO_ROOT}"
mkdir -p "${GO_ROOT}"
tar -C "${GO_ROOT}" --strip-components=1 -xzf "${GO_TARBALL}"

export PATH="${GO_ROOT}/bin:${HOME}/go/bin:${PATH}"
go version
go install "golang.org/x/mobile/cmd/gomobile@${XMOBILE_VERSION}"
go install "golang.org/x/mobile/cmd/gobind@${XMOBILE_VERSION}"
