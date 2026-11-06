#!/bin/bash
# Build the Vibium Record Player's custom element and put it under
# viewer/player, where the station embeds and serves it.
#
#   scripts/update-player.sh [REF]      default: main
#
# Needs git, node and npm. Clones the player into a temporary directory,
# builds its element bundle, copies the output over viewer/player (keeping
# README.md) and writes the commit to viewer/player/VERSION.

set -euo pipefail
REF=${1:-main}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
DEST=$ROOT/viewer/player
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

git clone -q https://github.com/VibiumDev/record-player.git "$TMP/player"
git -C "$TMP/player" checkout -q "$REF"
COMMIT=$(git -C "$TMP/player" rev-parse HEAD)
(cd "$TMP/player" && npm ci --no-audit --no-fund && npm run build:element)

find "$DEST" -mindepth 1 ! -name README.md -delete
cp -R "$TMP/player/dist-element/." "$DEST/"
cp "$TMP/player/LICENSE" "$DEST/LICENSE"
echo "$COMMIT" > "$DEST/VERSION"
echo "Record Player $COMMIT built into $DEST:"
du -sh "$DEST"
