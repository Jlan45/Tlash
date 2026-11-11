#!/bin/sh
# Cross-compiles static tlash binaries into dist/ with a sha256 checksum file.
# Usage: scripts/build-release.sh <version>
set -eu

version="${1:?usage: $0 <version>}"
commit="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
pkg="github.com/Jlan45/Tlash/internal/buildinfo"
ldflags="-s -w -X ${pkg}.Version=${version} -X ${pkg}.Commit=${commit} -X ${pkg}.Date=${date}"

# os/arch[/arm-version] → asset suffix
targets="
linux/amd64
linux/arm64
linux/arm/7
linux/386
"

rm -rf dist && mkdir -p dist
for t in $targets; do
	os="${t%%/*}"; rest="${t#*/}"; arch="${rest%%/*}"
	goarm=""; name="${os}-${arch}"
	if [ "$arch" = arm ]; then goarm="${rest#*/}"; name="${os}-armv${goarm}"; fi
	echo "building ${name}"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOARM="$goarm" \
		go build -trimpath -ldflags "$ldflags" -o "dist/tlash" ./cmd/tlash
	tar -C dist -czf "dist/tlash-${name}.tar.gz" tlash
	rm dist/tlash
done

(cd dist && sha256sum tlash-*.tar.gz > checksums.txt)
cat dist/checksums.txt
