# Build the taita gateway with the web UI embedded (Windows / PowerShell).
# //go:embed cannot reach ..\..\web\dist, so stage the built SPA into
# cmd\taita\web\dist first — the same trick mobile/build-apk.sh uses. The bash
# equivalent is cmd/taita/build.sh. Run from the repo root:
#   powershell -ExecutionPolicy Bypass -File cmd\taita\build.ps1
$ErrorActionPreference = 'Stop'

Write-Host '==> Building the web UI'
npm --prefix web run build
if ($LASTEXITCODE -ne 0) { throw "web build failed ($LASTEXITCODE)" }

Write-Host '==> Staging the SPA into the embed dir'
$dest = 'cmd/taita/web/dist'
Get-ChildItem -Path "$dest/*" -Exclude '.gitignore' -Force | Remove-Item -Recurse -Force
Get-ChildItem -Path 'web/dist/*' -Exclude '.gitignore' -Force | Copy-Item -Destination $dest -Recurse -Force

Write-Host '==> Building taita.exe'
go build -o taita.exe ./cmd/taita
if ($LASTEXITCODE -ne 0) { throw "go build failed ($LASTEXITCODE)" }
Write-Host '==> Done: .\taita.exe (run with -fake for a no-model demo)'
