#!/bin/sh
# Downloads the pinned zashboard build that release binaries embed (internal/webui/assets).
# Bump ZASHBOARD_VERSION and ZASHBOARD_SHA256 together.
set -eu

ZASHBOARD_VERSION=v3.30.0
ZASHBOARD_SHA256=ec794e7c4daf4c839ecbc0a1292fdaf7bc7a6e3e4a4ce284db2b32379145c278
# The no-fonts build uses system fonts: 2.8 MB instead of 9.4 MB, and no font requests.
ASSET=dist-no-fonts.zip

dir="$(cd "$(dirname "$0")/.." && pwd)/internal/webui/assets"
base="${mirror:-}https://github.com/Zephyruso/zashboard"

if [ -f "$dir/zashboard.zip" ] && [ "$(cat "$dir/zashboard.version" 2>/dev/null)" = "$ZASHBOARD_VERSION" ]; then
	exit 0
fi
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
curl -fsSL --retry 2 -o "$tmp" "$base/releases/download/$ZASHBOARD_VERSION/$ASSET"
got="$(sha256sum "$tmp" | cut -d' ' -f1)"
if [ "$got" != "$ZASHBOARD_SHA256" ]; then
	echo "zashboard checksum mismatch: $got" >&2
	exit 1
fi
curl -fsSL --retry 2 -o "$dir/LICENSE-zashboard" "https://raw.githubusercontent.com/Zephyruso/zashboard/$ZASHBOARD_VERSION/LICENSE"
mv "$tmp" "$dir/zashboard.zip"
printf '%s' "$ZASHBOARD_VERSION" > "$dir/zashboard.version"
echo "embedded zashboard $ZASHBOARD_VERSION"
