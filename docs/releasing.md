# Releasing llaminGO

Releases are built and published by GitHub Actions on a **version tag** — nothing
runs on ordinary pushes. One tag produces a GitHub Release with its downloads
attached:

- **Windows desktop** (Wails) — `llamingo-desktop-windows-amd64.zip` (the `.exe`). Built by
  [`windows.yml`](../.github/workflows/windows.yml). **Unsigned** — SmartScreen will
  say "unknown publisher"; an Authenticode step can slot in later once a
  code-signing certificate exists.
- **Android** — `llamingo-android.apk` (arm64, sideloadable). Built by
  [`android.yml`](../.github/workflows/android.yml), **signed** with the
  annoyances.studio upload key.
- **Terminal client (CLI/TUI)** — `llamingo-<os>-<arch>.zip` for windows/amd64, linux
  amd64 + arm64, and darwin amd64 + arm64 (each a static binary plus a short README; the
  linux-arm64 build also runs under Termux). Built by
  [`cli.yml`](../.github/workflows/cli.yml) — one Linux runner cross-compiles every target,
  CGO-free. **Unsigned**, and it needs none; the windows build carries the embedded icon.

The per-push desktop/web build-check stays in [`ci.yml`](../.github/workflows/ci.yml);
local `wails build` and `./mobile/build-apk.sh` remain for day-to-day testing.

## One-time setup (Android signing)

The Android job signs with your permanent upload key, supplied through GitHub
Secrets (never the repo) and gated behind a protected Environment so the key is only
ever decoded on a run you explicitly approve.

1. **Repo → Settings → Secrets and variables → Actions → New repository secret:**

   | Secret | Value |
   |---|---|
   | `ANDROID_KEYSTORE_BASE64` | base64 of `llamingo-release.jks` (command below) |
   | `ANDROID_KEYSTORE_PASSWORD` | the store password (1Password) |
   | `ANDROID_KEY_PASSWORD` | the key password (1Password) |
   | `ANDROID_KEY_ALIAS` | `llamingo` |

   Base64 the keystore on the dev machine (PowerShell):

   ```powershell
   [Convert]::ToBase64String([IO.File]::ReadAllBytes("C:\Code\.keys\llamingo-release.jks"))
   ```

2. **Repo → Settings → Environments → New environment → `android-release`.** Then add
   yourself under **Required reviewers** so every Android release run pauses for your
   one-click approval before the keystore is decoded — a sneaky workflow edit then
   can't exfiltrate the key on a tag push.

   **Plan limitation:** environment *protection rules* (required reviewers) are only
   available on **public** repos, or private repos on GitHub Pro/Team/Enterprise. On a
   free **private** repo you can create the environment but not the required-reviewer
   rule, so the job runs **ungated**. That's acceptable while private — only
   collaborators can push a tag to trigger it, and fork PRs never receive secrets —
   so **turn the required reviewer on as part of making the repo public**, which is
   exactly when the gate starts to matter. (Or GitHub Pro to gate it while private.)

Windows needs no secrets.

## Cutting a release

1. Bump the version:
   - Android: `versionName` / `versionCode` in
     [`mobile/android/app/build.gradle.kts`](../mobile/android/app/build.gradle.kts)
     (`versionCode` must increase for every Android release).
   - (The desktop build carries no version string yet.)
2. Commit, then tag and push:

   ```bash
   git tag v0.1.0
   git push origin v0.1.0
   ```

3. In the **Actions** tab, approve the **Android release** run when it pauses for
   review. Windows builds without a gate.
4. When both finish, the **Release** for the tag has both files attached — edit its
   notes and publish.

To rehearse without cutting a tag, use **Run workflow** (`workflow_dispatch`) on
either one; it builds and uploads the artifact to the run, with no Release attached.

## Notes

- **Android is arm64-only** (the gomobile bind target) — fine for phones, but it
  won't install on x86 emulators or 32-bit devices. Add targets to the `-target=`
  list in `android.yml` (and `mobile/build-apk.sh`) if needed.
- The signed APK satisfies Android's upcoming sideload **developer-verification**
  requirement because the upload key is registered with Google — see
  `C:\Code\.keys\llamingo-android-signing.md`.
- Release assets download publicly only once the repo is public; on a private repo
  they are collaborator-only.
