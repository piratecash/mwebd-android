#!/usr/bin/env bash
set -euo pipefail

ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-${HOME}/android-sdk}}"
CMDLINE_TOOLS_VERSION="11076708"
NDK_VERSION="27.0.12077973"
CMDLINE_TOOLS_ZIP="/tmp/android-commandline-tools.zip"
CMDLINE_TOOLS_DIR="${ANDROID_SDK_ROOT}/cmdline-tools/latest"

if [[ ! -x "${CMDLINE_TOOLS_DIR}/bin/sdkmanager" ]]; then
  mkdir -p "${ANDROID_SDK_ROOT}/cmdline-tools"
  curl -sSL "https://dl.google.com/android/repository/commandlinetools-linux-${CMDLINE_TOOLS_VERSION}_latest.zip" -o "${CMDLINE_TOOLS_ZIP}"
  rm -rf "${CMDLINE_TOOLS_DIR}" "${ANDROID_SDK_ROOT}/cmdline-tools/cmdline-tools"
  unzip -q "${CMDLINE_TOOLS_ZIP}" -d "${ANDROID_SDK_ROOT}/cmdline-tools"
  mv "${ANDROID_SDK_ROOT}/cmdline-tools/cmdline-tools" "${CMDLINE_TOOLS_DIR}"
fi

yes | "${CMDLINE_TOOLS_DIR}/bin/sdkmanager" --sdk_root="${ANDROID_SDK_ROOT}" --licenses >/dev/null || true
"${CMDLINE_TOOLS_DIR}/bin/sdkmanager" --sdk_root="${ANDROID_SDK_ROOT}" \
  "platform-tools" \
  "platforms;android-24" \
  "ndk;${NDK_VERSION}"

echo "Android SDK installed at ${ANDROID_SDK_ROOT}"
echo "Android NDK installed at ${ANDROID_SDK_ROOT}/ndk/${NDK_VERSION}"
