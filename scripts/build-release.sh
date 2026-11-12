#!/bin/sh
# Cross-compiles static tlash binaries into dist/ with a sha256 checksum file.
# Usage: scripts/build-release.sh <version>
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

rm -rf dist && mkdir -p dist
for t in $targets; do
	os="${t%%/*}"; rest="${t#*/}"; arch="${rest%%/*}"; variant=""
	[ "$rest" != "$arch" ] && variant="${rest#*/}"
	goarm=""; gomips=""; name="${os}-${arch}"
	case "$arch" in
	arm) goarm="$variant"; name="${os}-armv${goarm}" ;;
	mips | mipsle) gomips="$variant" ;;
	esac
	echo "building ${name}"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOARM="$goarm" GOMIPS="$gomips" \
		go build -trimpath -ldflags "$ldflags" -o "dist/tlash" ./cmd/tlash
	tar -C dist -czf "dist/tlash-${name}.tar.gz" tlash
	rm dist/tlash
done

(cd dist && sha256sum tlash-*.tar.gz > checksums.txt)
cat dist/checksums.txt
