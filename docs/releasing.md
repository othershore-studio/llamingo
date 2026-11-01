# Releasing llaminGO

Releases are built and published by GitHub Actions on a **version tag** — nothing
runs on ordinary pushes. One tag produces a GitHub Release with both downloads
attached:

- **Windows desktop** (Wails) — `llamingo-windows-amd64.zip` (the `.exe`). Built by
  [`windows.yml`](../.github/workflows/windows.yml). **Unsigned** — SmartScreen will
  say "unknown publisher"; an Authenticode step can slot in later once a
  code-signing certificate exists.
- **Android** — `llamingo-android.apk` (arm64, sideloadable). Built by
  [`android.yml`](../.github/workflows/android.yml), **signed** with the
  annoyances.studio upload key.

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

2. **Repo → Settings → Environments → New environment → `android-release`**, and add
   yourself under **Required reviewers**. Every Android release run then pauses for
   your one-click approval before the keystore is decoded — so a sneaky workflow edit
   can't exfiltrate the key on a tag push.

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
