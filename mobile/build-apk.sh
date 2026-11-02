#!/usr/bin/env bash
# Build the llaminGO Android debug APK (Option A: the Go core runs in-process via
# a gomobile-bound .aar, behind a WebView that loads the embedded Svelte UI on a
# loopback port). Run it from anywhere; it cd's to the repo root.
#
# Prerequisites:
#   - Go (the module toolchain)
#   - Android SDK + NDK, with ANDROID_HOME / ANDROID_NDK_HOME set
#   - Java 17
#   - gomobile on PATH:  go install golang.org/x/mobile/cmd/gomobile@latest
set -euo pipefail
cd "$(dirname "$0")/.."   # repo root

echo "==> Building the web UI (vite clears dist/assets itself; keeps dist/.gitignore)"
npm --prefix web run build

echo "==> Copying the SPA into the embed dir (go:embed can't reach ../../web/dist)"
find mobile/web/dist -mindepth 1 ! -name .gitignore -delete
cp -r web/dist/* mobile/web/dist/

echo "==> gomobile bind -> mobile/android/app/libs/llamingo.aar"
gomobile bind -target=android/arm64 -androidapi 21 \
  -javapkg=studio.annoyances.llamingo \
  -o mobile/android/app/libs/llamingo.aar ./mobile

echo "==> Assembling the debug APK"
( cd mobile/android && ./gradlew assembleDebug )

echo "==> APK: mobile/android/app/build/outputs/apk/debug/app-debug.apk"
