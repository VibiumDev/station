#!/bin/bash
# Build Vibium for Linux from a checkout and put it in deploy/vibium/, for
# an image built with --build-arg VIBIUM_LOCAL=1.
#
#   scripts/vibium-local.sh [VIBIUM_DIR]     default: ../vibium
#
# ARCHES picks the architectures, "amd64 arm64" by default; Fly.io needs
# amd64. The version is Vibium's own, plus the commit, and "dirty" when the
# checkout has changes that are not committed.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SRC=$(cd "${1:-$ROOT/../vibium}" && pwd)
OUT=$ROOT/deploy/vibium
[ -f "$SRC/clicker/go.mod" ] || { echo "$SRC is not a Vibium checkout" >&2; exit 1; }
VERSION="$(cat "$SRC/VERSION")-local.$(git -C "$SRC" rev-parse --short HEAD)"
git -C "$SRC" diff --quiet HEAD -- . ':!demo.wav' || VERSION="$VERSION.dirty"
make -s -C "$SRC" embeds
for arch in ${ARCHES:-amd64 arm64}; do
  (cd "$SRC/clicker" && GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
    -ldflags="-s -w -X main.version=$VERSION -X github.com/vibium/clicker/internal/api.Version=$VERSION" \
    -o "$OUT/vibium-linux-$arch" ./cmd/clicker)
  echo "$OUT/vibium-linux-$arch: $(sha256sum "$OUT/vibium-linux-$arch" | cut -c1-16)…"
done
echo "Vibium $VERSION from $(git -C "$SRC" rev-parse --abbrev-ref HEAD). Deploy with --build-arg VIBIUM_LOCAL=1."
