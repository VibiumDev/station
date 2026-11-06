#!/bin/bash
# Build the Vibium Record Player and put it under viewer/player/app, where
# the station embeds and serves it.
#
#   scripts/update-player.sh [REF]      default: main
#
# Needs git, node and npm. Clones the player into a temporary directory,
# builds it for the station's path, copies the output over viewer/player
# (keeping README.md) and writes the commit to viewer/player/VERSION.

set -euo pipefail
REF=${1:-main}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
DEST=$ROOT/viewer/player
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

git clone -q https://github.com/VibiumDev/record-player.git "$TMP/player"
git -C "$TMP/player" checkout -q "$REF"
COMMIT=$(git -C "$TMP/player" rev-parse HEAD)
# The app's router assumes the site root; under the station's path it
# needs the build's base as its basename, or every page is its 404.
sed -i 's|<BrowserRouter>|<BrowserRouter basename={import.meta.env.BASE_URL}>|' "$TMP/player/src/App.tsx"
(cd "$TMP/player" && npm ci --no-audit --no-fund && npx vite build --base=/viewer/player/app/ --outDir dist-app)

find "$DEST" -mindepth 1 ! -name README.md -delete
mkdir -p "$DEST/app"
# The hosted site's demo recordings and sound are not needed here.
(cd "$TMP/player/dist-app" && find . -type f ! -name '*.zip' ! -name '*.mp3' -exec cp --parents {} "$DEST/app/" \;)
cp "$TMP/player/LICENSE" "$DEST/LICENSE"
echo "$COMMIT" > "$DEST/VERSION"
echo "Record Player $COMMIT built into $DEST:"
du -sh "$DEST"
