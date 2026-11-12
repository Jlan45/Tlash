#!/bin/sh
# Cross-compiles static tlash binaries into dist/ with a sha256 checksum file.
# Usage: [UPX_BIN=/path/to/upx] scripts/build-release.sh <version>
# Without UPX_BIN the lite builds are left uncompressed (fine for local testing).
set -eu

version="${1:?usage: $0 <version>}"
commit="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
pkg="github.com/Jlan45/Tlash/internal/buildinfo"
ldflags="-s -w -X ${pkg}.Version=${version} -X ${pkg}.Commit=${commit} -X ${pkg}.Date=${date}"

# os/arch[/variant] → asset name. Routers need the MIPS (soft-float, which runs on every
# MIPS CPU) and older ARM builds.
targets="
linux/amd64
linux/arm64
linux/arm/7
linux/arm/6
linux/arm/5
linux/386
linux/mipsle/softfloat
linux/mips/softfloat
linux/riscv64
"

# Standard builds embed zashboard; lite builds (-tags lite, for small router flash) don't.
"$(dirname "$0")/fetch-webui.sh"

rm -rf dist && mkdir -p dist
for t in $targets; do
	os="${t%%/*}"; rest="${t#*/}"; arch="${rest%%/*}"; variant=""
	[ "$rest" != "$arch" ] && variant="${rest#*/}"
	goarm=""; gomips=""; name="${os}-${arch}"
	case "$arch" in
	arm) goarm="$variant"; name="${os}-armv${goarm}" ;;
	mips | mipsle) gomips="$variant" ;;
	esac
	for variant_tag in "" lite; do
		prefix=tlash gcflags=""
		if [ -n "$variant_tag" ]; then
			# Lite builds target small router flash: no inlining (~9% smaller) and UPX below.
			prefix="tlash-${variant_tag}" gcflags="all=-l"
		fi
		echo "building ${prefix}-${name}"
		CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOARM="$goarm" GOMIPS="$gomips" \
			go build -trimpath -tags "$variant_tag" -gcflags "$gcflags" -ldflags "$ldflags" -o "dist/tlash" ./cmd/tlash
		if [ -n "$variant_tag" ] && [ -n "${UPX_BIN:-}" ] && [ "$arch" != riscv64 ]; then
			# ~10 MB → ~2.5 MB; UPX has no riscv64 support.
			"$UPX_BIN" -q --lzma dist/tlash >/dev/null
		fi
		tar -C dist -czf "dist/${prefix}-${name}.tar.gz" tlash
		rm dist/tlash
	done
done

# The embedded dashboard's MIT license ships alongside the binaries.
cp "$(dirname "$0")/../internal/webui/assets/LICENSE-zashboard" dist/
(cd dist && sha256sum tlash-*.tar.gz > checksums.txt)
cat dist/checksums.txt
