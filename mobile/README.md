# llaminGO on Android (Option A — in-process Go)

The Android app is the **same Go core and Svelte UI as the desktop**, with a thin
native shell. A normal Android app can't `exec` a shipped binary, so the core runs
*in the app's process* via a gomobile-bound library: it serves the API **and** the
embedded UI on a loopback port, and a `WebView` loads that port. Anything that
changes in the shared web UI shows up here — only the shell is mobile-specific.

## Layout
- **`mobile/mobile.go`** — the gomobile entrypoint. `Start(dataDir, llamaURL)`
  serves the core API plus the `//go:embed`'d SPA on `127.0.0.1:<port>` and returns
  the port; it's idempotent (safe when the Activity is recreated).
- **`mobile/android/`** — the shell: a one-screen `MainActivity` (WebView → the
  port) and its Gradle project. Application id `studio.annoyances.llamingo`.

## Build
Prerequisites: Go, the Android SDK + NDK (`ANDROID_HOME` / `ANDROID_NDK_HOME`),
Java 17, and gomobile (`go install golang.org/x/mobile/cmd/gomobile@latest`).

    ./mobile/build-apk.sh

produces `mobile/android/app/build/outputs/apk/debug/app-debug.apk` (debug-signed).
Install by sideloading the file — no Developer Options needed. On first launch, set
your `llama-server` URL in Settings to a LAN-reachable address (not `localhost`,
which on the phone is the phone).

## Notes
- **`targetSdk` is 34 on purpose** — it sidesteps Android 15/16 forced edge-to-edge,
  so classic insets + `adjustResize` handle the status bar and keyboard. Before
  shipping to Play (which wants 35+), handle edge-to-edge insets properly **and test
  on a device** — a `WebView` ignored the inset listeners during the spike.
- **iOS** rides the same path: `gomobile bind -target=ios` → an `.xcframework` with
  the same `Start` entrypoint, behind a `WKWebView`. Builds on macOS + Xcode only;
  iOS forbids subprocesses, so stdio-MCP is out there (moot on a phone — HTTP MCP works).
