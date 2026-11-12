#!/bin/sh
# Runs `tlash version` from every release tarball in dist/, under qemu-user for foreign
# architectures, so a broken cross build or UPX-compressed binary fails CI.
set -eu

qemu_for() {
	case "$1" in
	amd64 | 386) echo "" ;;
	arm64) echo qemu-aarch64-static ;;
	armv5 | armv6 | armv7) echo qemu-arm-static ;;
	mips) echo qemu-mips-static ;;
	mipsle) echo qemu-mipsel-static ;;
	riscv64) echo qemu-riscv64-static ;;
	*) echo "unknown arch $1" >&2; exit 1 ;;
	esac
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
for tarball in dist/tlash-*.tar.gz; do
	arch="${tarball##*-linux-}"
	arch="${arch%.tar.gz}"
	tar -C "$tmp" -xzf "$tarball" tlash
	runner="$(qemu_for "$arch")"
	printf '%-40s %6s KB  ' "$(basename "$tarball")" "$(($(wc -c <"$tmp/tlash") / 1024))"
	if [ -n "$runner" ]; then
		"$runner" "$tmp/tlash" version
	else
		"$tmp/tlash" version
	fi
	rm -f "$tmp/tlash"
done
