#!/usr/bin/env bash
# Build the taita gateway with the web UI embedded. //go:embed cannot reach
# ../../web/dist, so stage the built SPA into cmd/taita/web/dist first — the same
# trick mobile/build-apk.sh uses. Run from the repo root.
set -euo pipefail

echo "==> Building the web UI"
npm --prefix web run build

echo "==> Staging the SPA into the embed dir (go:embed can't reach ../../web/dist)"
find cmd/taita/web/dist -mindepth 1 ! -name .gitignore -delete
cp -r web/dist/* cmd/taita/web/dist/

echo "==> Building taita"
go build -o taita ./cmd/taita
echo "==> Done: ./taita (run with -fake for a no-model demo)"
