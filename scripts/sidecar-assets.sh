#!/usr/bin/env bash

sidecar_platforms() {
  printf '%s\n' linux-x64 windows-x64 macos-arm64
}

sidecar_filename() {
  case "$1" in
    linux-x64) printf '%s\n' mwebd-sidecar-linux-x64 ;;
    windows-x64) printf '%s\n' mwebd-sidecar-windows-x64.exe ;;
    macos-arm64) printf '%s\n' mwebd-sidecar-macos-arm64 ;;
    *) return 1 ;;
  esac
}

sidecar_goos() {
  case "$1" in
    linux-x64) printf '%s\n' linux ;;
    windows-x64) printf '%s\n' windows ;;
    macos-arm64) printf '%s\n' darwin ;;
    *) return 1 ;;
  esac
}

sidecar_goarch() {
  case "$1" in
    linux-x64 | windows-x64) printf '%s\n' amd64 ;;
    macos-arm64) printf '%s\n' arm64 ;;
    *) return 1 ;;
  esac
}

sidecar_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  else
    shasum -a 256 "$1" | awk '{ print $1 }'
  fi
}

sidecar_size() {
  wc -c < "$1" | tr -d '[:space:]'
}
