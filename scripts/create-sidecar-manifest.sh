#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "Usage: $0 VERSION COMMIT ASSET_DIRECTORY OUTPUT" >&2
  exit 2
fi

VERSION="$1"
COMMIT="$2"
ASSET_DIRECTORY="$3"
OUTPUT="$4"
if [[ ! "${VERSION}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ||
  ! "${COMMIT}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Invalid release identity" >&2
  exit 2
fi

SCRIPT_DIRECTORY="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIRECTORY}/sidecar-assets.sh"

{
  echo mwebd-sidecars-v1
  echo "version|${VERSION}"
  echo "commit|${COMMIT}"
  while IFS= read -r platform; do
    filename="$(sidecar_filename "${platform}")"
    file="${ASSET_DIRECTORY}/${filename}"
    if [[ ! -f "${file}" ]]; then
      echo "Missing sidecar asset: ${file}" >&2
      exit 1
    fi
    echo "asset|${platform}|${filename}|$(sidecar_sha256 "${file}")|$(sidecar_size "${file}")"
  done < <(sidecar_platforms)
} > "${OUTPUT}"
