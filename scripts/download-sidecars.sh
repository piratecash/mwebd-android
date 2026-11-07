#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 VERSION DESTINATION" >&2
  exit 2
fi

VERSION="$1"
DESTINATION="$2"
if [[ ! "${VERSION}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "VERSION must be exact numeric SemVer" >&2
  exit 2
fi

SCRIPT_DIRECTORY="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIRECTORY}/sidecar-assets.sh"

REPOSITORY_ROOT="$(git -C "${SCRIPT_DIRECTORY}" rev-parse --show-toplevel)"
EXPECTED_COMMIT="$(git -C "${REPOSITORY_ROOT}" rev-parse HEAD)"
RELEASE_URL="https://github.com/piratecash/mwebd-android/releases/download/${VERSION}"
WORK_DIRECTORY="$(mktemp -d)"
trap 'rm -rf "${WORK_DIRECTORY}"' EXIT

download() {
  curl \
    --fail \
    --location \
    --silent \
    --show-error \
    --retry 5 \
    --connect-timeout 15 \
    --max-time 300 \
    "${RELEASE_URL}/$1" \
    --output "$2"
}

MANIFEST="${WORK_DIRECTORY}/mwebd-sidecars.manifest"
download mwebd-sidecars.manifest "${MANIFEST}"

if ! grep -Fxq "mwebd-sidecars-v1" "${MANIFEST}" ||
  ! grep -Fxq "version|${VERSION}" "${MANIFEST}" ||
  ! grep -Fxq "commit|${EXPECTED_COMMIT}" "${MANIFEST}"; then
  echo "Sidecar manifest identity does not match ${VERSION}/${EXPECTED_COMMIT}" >&2
  exit 1
fi

while IFS= read -r platform; do
  filename="$(sidecar_filename "${platform}")"
  manifest_line="$(awk -F '|' -v platform="${platform}" -v filename="${filename}" '
    $1 == "asset" && $2 == platform && $3 == filename { print; matches++ }
    END { if (matches != 1) exit 1 }
  ' "${MANIFEST}")"
  IFS='|' read -r kind manifest_platform manifest_filename expected_hash expected_size <<< "${manifest_line}"
  if [[ "${kind}" != asset || "${manifest_platform}" != "${platform}" ||
    "${manifest_filename}" != "${filename}" || ! "${expected_hash}" =~ ^[0-9a-f]{64}$ ||
    ! "${expected_size}" =~ ^[1-9][0-9]*$ ]]; then
    echo "Invalid manifest entry for ${platform}" >&2
    exit 1
  fi

  downloaded="${WORK_DIRECTORY}/${filename}"
  download "${filename}" "${downloaded}"
  actual_hash="$(sidecar_sha256 "${downloaded}")"
  actual_size="$(sidecar_size "${downloaded}")"
  if [[ "${actual_hash}" != "${expected_hash}" || "${actual_size}" != "${expected_size}" ]]; then
    echo "Sidecar integrity check failed for ${filename}" >&2
    exit 1
  fi

  target_directory="${DESTINATION}/mwebd/${platform}"
  mkdir -p "${target_directory}"
  mv "${downloaded}" "${target_directory}/${filename}"
done < <(sidecar_platforms)
