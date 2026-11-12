#!/bin/sh
# Tlash installer. Usage:
#   curl -fsSL https://raw.githubusercontent.com/Jlan45/Tlash/main/scripts/install.sh | sh
#   curl -fsSL <url> | mirror=https://ghfast.top/ sh      # download via a GitHub mirror
#   curl -fsSL <url> | version=v0.1.0 sh                  # pin a release
#   curl -fsSL <url> | variant=lite sh                    # no built-in web dashboard (small flash)
#   curl -fsSL <url> | sh -s uninstall                    # remove Tlash, keep data
#   curl -fsSL <url> | sh -s purge                        # remove Tlash and its data
#
# Env: mirror, version, variant (auto|full|lite), bindir (install location of the tlash
# binary), base_url (testing).
set -eu

REPO="Jlan45/Tlash"
mirror="${mirror:-}"
version="${version:-}"
variant="${variant:-auto}"

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33m警告:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m错误:\033[0m %s\n' "$*" >&2; exit 1; }

is_root() { [ "$(id -u)" -eq 0 ]; }
is_openwrt() { [ -f /etc/openwrt_release ]; }

if is_root && is_openwrt; then
	default_bindir=/usr/bin # /usr/local/bin isn't on OpenWrt's PATH
	datadir=/etc/tlash
elif is_root; then
	default_bindir=/usr/local/bin
	datadir=/etc/tlash
else
	default_bindir="$HOME/.local/bin"
	datadir="${XDG_DATA_HOME:-$HOME/.local/share}/tlash"
fi
bindir="${bindir:-$default_bindir}"

mirrored() {
	if [ -n "$mirror" ]; then
		printf '%s/%s' "${mirror%/}" "$1"
	else
		printf '%s' "$1"
	fi
}

# fetch URL FILE
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 2 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "需要 curl 或 wget"
	fi
}

# little_endian reads EI_DATA from an ELF header (1 = little endian); uname -m says "mips"
# for both endiannesses, and most routers (MT7621 etc.) are little endian.
little_endian() {
	for f in /bin/busybox /bin/sh; do
		[ -f "$f" ] || continue
		if command -v hexdump >/dev/null 2>&1; then
			[ "$(hexdump -s 5 -n 1 -e '1/1 "%d"' "$f" 2>/dev/null)" = 2 ] && return 1
			return 0
		fi
		if command -v od >/dev/null 2>&1; then
			[ "$(od -An -tu1 -j5 -N1 "$f" 2>/dev/null | tr -d ' ')" = 2 ] && return 1
			return 0
		fi
	done
	return 0
}

detect_arch() {
	[ "$(uname -s)" = Linux ] || die "Tlash 只支持 Linux"
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	armv7* | armv8l) echo armv7 ;;
	armv6*) echo armv6 ;;
	armv5*) echo armv5 ;;
	i386 | i686) echo 386 ;;
	mips | mipsel | mipsle)
		if little_endian; then echo mipsle; else echo mips; fi ;;
	riscv64) echo riscv64 ;;
	*) die "不支持的架构: $(uname -m)" ;;
	esac
}

# Newest published release including pre-releases (GitHub's /releases/latest skips those).
# The list API isn't ordered by publish time (alpha.9 sorts before alpha.10), so sort by
# published_at ourselves. Drafts have "published_at": null and are skipped.
resolve_version() {
	[ -n "$version" ] && return
	tmp="$workdir/releases.json"
	if fetch "https://api.github.com/repos/$REPO/releases?per_page=20" "$tmp" 2>/dev/null; then
		version=$(awk -F'"' '
			/"tag_name":/ { tag = $4 }
			/"published_at": "/ { print $4, tag }
		' "$tmp" | sort -r | head -n 1 | cut -d' ' -f2)
	fi
	[ -n "$version" ] || die "无法从 GitHub API 获取最新版本（服务器无法访问 api.github.com 时请用 version=vX.Y.Z 指定）"
}

verify_checksum() {
	file="$1" sums="$2"
	if command -v sha256sum >/dev/null 2>&1; then
		want=$(grep " $(basename "$file")\$" "$sums" | awk '{print $1}')
		got=$(sha256sum "$file" | awk '{print $1}')
		[ -n "$want" ] || die "checksums.txt 中没有 $(basename "$file")"
		[ "$want" = "$got" ] || die "sha256 校验失败：$got，期望 $want"
		info "sha256 校验通过"
	else
		warn "没有 sha256sum，跳过校验"
	fi
}

# free_kb is the free space where Tlash will live (the overlay on OpenWrt).
free_kb() {
	for d in "$datadir" /overlay /; do
		[ -d "$d" ] || continue
		df -k "$d" 2>/dev/null | awk 'NR==2 {print $4; exit}'
		return
	done
}

# choose_variant picks lite (no built-in dashboard, ~3 MB smaller) for routers short on flash.
choose_variant() {
	case "$variant" in
	full | lite) return ;;
	auto) ;;
	*) die "variant 只能是 auto / full / lite" ;;
	esac
	variant=full
	if is_openwrt; then
		avail=$(free_kb)
		if [ -n "$avail" ] && [ "$avail" -lt 32768 ]; then
			variant=lite
			info "闪存剩余 $((avail / 1024)) MB，安装精简版（不含内置 Web 面板，可在线下载）"
		fi
	fi
}

install_tlash() {
	arch=$(detect_arch)
	choose_variant
	workdir=$(mktemp -d)
	trap 'rm -rf "$workdir"' EXIT INT TERM
	resolve_version
	asset="tlash-linux-$arch.tar.gz"
	[ "$variant" = lite ] && asset="tlash-lite-linux-$arch.tar.gz"
	base="${base_url:-$(mirrored "https://github.com/$REPO/releases/download/$version")}"

	info "下载 Tlash $version ($arch, $variant)"
	fetch "$base/$asset" "$workdir/$asset" || die "下载失败: $base/$asset"
	fetch "$base/checksums.txt" "$workdir/checksums.txt" || die "下载校验文件失败"
	verify_checksum "$workdir/$asset" "$workdir/checksums.txt"

	tar -xzf "$workdir/$asset" -C "$workdir" tlash
	mkdir -p "$bindir"
	# Install via rename so a running tlash isn't overwritten in place.
	cp "$workdir/tlash" "$bindir/.tlash.new"
	chmod 755 "$bindir/.tlash.new"
	mv -f "$bindir/.tlash.new" "$bindir/tlash"
	info "已安装到 $bindir/tlash（$("$bindir/tlash" version)）"

	case ":$PATH:" in
	*":$bindir:"*) cmd=tlash ;;
	*)
		cmd="$bindir/tlash"
		warn "$bindir 不在 PATH 中，可在 shell 配置中加入：export PATH=\"$bindir:\$PATH\""
		;;
	esac

	if is_openwrt; then
		openwrt_hints
	fi

	echo
	info "数据目录：$datadir（已有配置会保留）"
	if [ -n "$mirror" ]; then
		info "首次运行后在「内核」页按 M 填入镜像 $mirror，再按 D 下载内核"
	fi
	echo "  运行 $cmd 打开管理界面，按“状态”面板的引导：下载内核 → 添加订阅 → 启动"
	if ! is_root; then
		echo "  非 root 安装：内核以用户级 systemd 服务或后台进程运行，不支持 TUN"
	fi
}

# openwrt_hints checks what a router needs before the gateway can be turned on.
openwrt_hints() {
	pm="opkg update && opkg install"
	command -v apk >/dev/null 2>&1 && pm="apk add"
	if [ ! -e /dev/net/tun ] && ! grep -q '^tun ' /proc/modules 2>/dev/null; then
		warn "未检测到 TUN 支持，作为网关前请安装：$pm kmod-tun"
	fi
	[ -x /sbin/fw4 ] || warn "未检测到 fw4：网关需要 OpenWrt 22.03 及以上"
	# The core is about 30 MB; small flash should keep it compressed or in RAM.
	avail=$(df -k "$datadir" 2>/dev/null | awk 'NR==2 {print $4}')
	[ -n "$avail" ] || avail=$(df -k / 2>/dev/null | awk 'NR==2 {print $4}')
	if [ -n "$avail" ] && [ "$avail" -lt 40960 ]; then
		warn "闪存剩余 $((avail / 1024)) MB，内核约 30 MB：首次运行后在 设置 → 内核 把“存放位置”改为“压缩存放”或“只放内存”，再下载内核"
	fi
}

systemctl_maybe() {
	command -v systemctl >/dev/null 2>&1 || return 0
	if is_root; then
		systemctl "$@" >/dev/null 2>&1 || true
	else
		systemctl --user "$@" >/dev/null 2>&1 || true
	fi
}

uninstall_tlash() {
	purge="$1"
	# Restore dnsmasq / firewall / forwarding changes before the binary disappears.
	if [ -x "$bindir/tlash" ] && [ -d "$datadir" ]; then
		"$bindir/tlash" --dir "$datadir" gateway off >/dev/null 2>&1 || true
	fi
	info "停止并移除服务"
	systemctl_maybe disable --now tlash-mihomo.service tlash-tasks.timer
	if is_root; then
		unitdir=/etc/systemd/system
	else
		unitdir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
	fi
	rm -f "$unitdir/tlash-mihomo.service" "$unitdir/tlash-tasks.service" "$unitdir/tlash-tasks.timer"
	systemctl_maybe daemon-reload
	if is_root && [ -f /etc/init.d/tlash-mihomo ]; then
		/etc/init.d/tlash-mihomo stop >/dev/null 2>&1 || true
		/etc/init.d/tlash-mihomo disable >/dev/null 2>&1 || true
		command -v rc-update >/dev/null 2>&1 && rc-update del tlash-mihomo default >/dev/null 2>&1 || true
		rm -f /etc/init.d/tlash-mihomo
	fi
	# Background-process mode keeps a pidfile.
	if [ -f "$datadir/runtime/mihomo.pid" ]; then
		kill "$(cat "$datadir/runtime/mihomo.pid")" 2>/dev/null || true
		rm -f "$datadir/runtime/mihomo.pid"
	fi
	if command -v crontab >/dev/null 2>&1 && crontab -l 2>/dev/null | grep -q '# tlash-tasks:'; then
		crontab -l 2>/dev/null | grep -v '# tlash-tasks:' | crontab - || true
	fi
	is_root && rm -f /etc/profile.d/tlash-proxy.sh
	# The RAM copy of the core lives in /tmp/tlash-<first 8 hex of sha256(datadir)>, as in Tlash.
	if command -v sha256sum >/dev/null 2>&1; then
		ramdir="/tmp/tlash-$(printf %s "$datadir" | sha256sum | cut -c1-8)"
		[ -d "$ramdir" ] && rm -rf "$ramdir"
	fi
	rm -f "$bindir/tlash"
	if [ "$purge" = yes ]; then
		rm -rf "$datadir"
		info "已删除数据目录 $datadir"
	else
		info "数据目录 $datadir 已保留（彻底删除请用 purge）"
	fi
	info "Tlash 已卸载"
}

case "${1:-install}" in
install) install_tlash ;;
uninstall) uninstall_tlash no ;;
purge) uninstall_tlash yes ;;
*) die "未知操作: $1（install / uninstall / purge）" ;;
esac
