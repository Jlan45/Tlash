#!/bin/sh
# Downloads a pinned, checksum-verified UPX for compressing lite builds; prints its path.
set -eu

UPX_VERSION=5.0.2
UPX_SHA256=ddc2654063fe4dc80d95b420788494e4db078ebb01a650692d623b5a9906e61e

dir="${TMPDIR:-/tmp}/tlash-upx-$UPX_VERSION"
bin="$dir/upx-$UPX_VERSION-amd64_linux/upx"
if [ ! -x "$bin" ]; then
	mkdir -p "$dir"
	curl -fsSL --retry 2 -o "$dir/upx.tar.xz" "https://github.com/upx/upx/releases/download/v$UPX_VERSION/upx-$UPX_VERSION-amd64_linux.tar.xz"
	got="$(sha256sum "$dir/upx.tar.xz" | cut -d' ' -f1)"
	if [ "$got" != "$UPX_SHA256" ]; then
		echo "upx checksum mismatch: $got" >&2
		exit 1
	fi
	tar -C "$dir" -xf "$dir/upx.tar.xz"
fi
echo "$bin"
