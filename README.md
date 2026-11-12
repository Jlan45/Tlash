# Tlash

针对服务器的 Clash（[mihomo](https://github.com/MetaCubeX/mihomo)）终端管理器：SSH 登录后，在一个 TUI 里管理内核、节点和日志。

> 开发中，当前完成 M3 阶段，见 [计划文档](docs/PLAN.md)。一键安装脚本在 M4 实现，目前需要手动下载 tlash 二进制。

## 界面

参照 lazydocker：左侧 7 个面板同时可见（状态、订阅、代理组、覆写、连接、规则、设置），右侧主面板显示当前选中项的详情，分多个标签。

| 按键 | 作用 |
|---|---|
| `↑` `↓`（到头自动跨面板）/ `Tab` / `1`–`7` | 左侧：切换面板 |
| `j` `k` / `↑` `↓` | 列表内移动 |
| `Enter` 或 `→` / `Esc` | 进入主面板 / 返回 |
| `[` `]`；主面板内也可用 `←` `→` / `h` `l` | 切换主面板标签 |
| `/` | 过滤当前列表 |
| `x` | 当前可用的全部操作 |
| `+` / `_` | 屏幕模式：普通 / 主面板加宽 / 全屏 |
| `?` · `q` | 帮助 · 退出（不影响内核运行） |

各面板的动作字母保持一致：`a` 添加、`d` 删除/关闭、`e` 编辑、`u` 更新、`space` 启用/切换。支持鼠标点击与滚轮。

## 功能

- **状态**：内核启停与重启、出站模式切换、实时速率曲线、出口 IP 检测；标签页：概览 / 日志 / 运行配置
- **订阅**：添加链接或导入本地配置，更新、切换、流量与到期、`$EDITOR` 编辑；标签页：信息 / 原始内容
- **代理组**：节点选择与测速、排序、取消固定、代理集更新
- **覆写**：YAML 片段修改订阅配置（如在规则前插入自定义规则），可排序、限定订阅；标签页：内容 / 最终配置预览
- **连接**：实时连接与速率，详情，关闭单个或全部
- **规则**：命中统计、临时停用、规则集更新
- **网关（透明代理）**：让局域网设备不装客户端就走代理（旁路由 / 本机），基于 mihomo TUN；按 MAC 黑名单或白名单选择设备，大陆 IP 可在内核层直接放行，仅 IPv4 或 IPv4 + IPv6；开启后 60 秒内需确认，否则自动回滚（SSH 断开也会回滚）
- **设置**：内核下载与回滚、服务托管与开机自启、接管设置（端口 / 局域网 / 日志级别 / 控制接口等）、DNS 与嗅探、GEO 与 Web 面板、定时任务、代理环境变量（`eval "$(tlash env)"`）、DNS 查询等工具

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/Jlan45/Tlash/main/scripts/install.sh | sh
```

- 国内服务器经镜像下载：`curl -fsSL https://ghfast.top/https://raw.githubusercontent.com/Jlan45/Tlash/main/scripts/install.sh | mirror=https://ghfast.top/ sh`
- 指定版本：`... | version=v0.1.0-alpha.1 sh`
- 卸载（保留数据）：`... | sh -s uninstall`；连数据一起删除：`... | sh -s purge`

两个版本：**标准版**内置 zashboard 面板（约 13 MB）；**精简版**不带面板、经 UPX 压缩（约 2.5 MB），给闪存小的路由器用。OpenWrt 上闪存不足 32 MB 时自动安装精简版，也可用 `variant=full` / `variant=lite` 指定。

root 安装到 `/usr/local/bin/tlash`，数据在 `/etc/tlash`；普通用户安装到 `~/.local/bin/tlash`，数据在 `~/.local/share/tlash`。也可以从 [Releases](https://github.com/Jlan45/Tlash/releases) 手动下载（linux amd64 / arm64 / armv7 / 386）。

## 使用

运行 `tlash`，按“状态”面板的引导：

1. `7` 设置 → 内核，按 `D` 下载内核（国内服务器先按 `M` 设置下载镜像，或按 `i` 导入本地文件）
2. `2` 订阅，按 `a` 添加订阅链接
3. `1` 状态，按 `s` 启动

数据目录：root 为 `/etc/tlash`，普通用户为 `~/.local/share/tlash`，可用 `--dir` 指定。Tlash 生成的运行配置只通过 unix socket 暴露控制接口；需要 Web 面板时在“设置 → 接管”里开启 TCP 控制接口（会强制设置密钥）。

连接一个不由 Tlash 管理的 mihomo（只能查看和切换，不管理配置与进程）：

```sh
tlash --controller /path/to/mihomo.sock
tlash --controller 127.0.0.1:9090 --secret <secret>
```

## 命令行

不带参数运行 `tlash` 打开管理界面；以下命令可用于脚本和定时任务（失败时返回非零）：

| 命令 | 作用 |
|---|---|
| `tlash status` | 内核、订阅流量与到期、模式与端口、网关、速率 |
| `tlash start` / `stop` / `restart` | 启停内核（`restart` 会重新生成配置） |
| `tlash mode rule\|global\|direct` | 切换出站模式 |
| `tlash sub list` / `update [名称]` / `use 名称` / `add 链接` | 订阅管理，`update` 不带名称时更新全部 |
| `tlash core upgrade [--channel alpha]` | 下载最新内核，运行中自动重启 |
| `tlash update` | 升级 Tlash 自身（保持标准版 / 精简版，校验 sha256） |
| `tlash uninstall [--purge]` | 卸载；`--purge` 同时删除数据 |
| `tlash web` / `tlash env` / `tlash gateway check\|off` | 面板地址 / 代理环境变量 / 网关检查与救急关闭 |

## Web 面板

在 设置 → 资源 → Web 面板 按 `w` 选择 zashboard（标准版内置，选择后直接释放，无需访问 GitHub；`P` 在线更新到最新版）或 metacubexd（在线下载），并在 设置 → 接管 开启 TCP 控制接口（`127.0.0.1:9090` 仅本机，`0.0.0.0:9090` 局域网可访问）。然后运行：

```sh
tlash web
```

打印访问地址、后端地址和密钥；控制接口只监听本机时，还会打印可直接复制的 SSH 隧道命令。

## 用作旁路由

需要 root。在 `7` 设置 → 网关 查看环境检查，按 `t` 开启，确认网络正常后按 `y` 保留。然后在需要代理的设备上把**网关**和 **DNS** 都设为这台机器的局域网 IP（或在主路由的 DHCP 里下发）。

- 53 端口被占用（如 systemd-resolved）时，环境检查会提示，把“DNS 监听”改为本机局域网 IP，例如 `192.168.1.2:53`
- 出问题且界面用不了时，在本机控制台执行 `tlash gateway off` 立即关闭并还原

## 用作 OpenWrt 主路由

支持 OpenWrt 22.03 及以上（fw4 / nftables），架构 x86_64、arm64、armv5/6/7、mips、mipsle、riscv64。

1. 安装：`wget -qO- https://raw.githubusercontent.com/Jlan45/Tlash/main/scripts/install.sh | sh`（会提示缺少的 `kmod-tun` 等依赖）
2. 闪存小于约 40 MB 时，先在 `7` 设置 → 内核 把“存放位置”改为**压缩存放**（闪存只存 .gz，开机解压到内存）或**只放内存**（开机自动下载），再按 `D` 下载内核
3. 添加订阅并启动后，在 设置 → 网关 按 `t` 开启

开启网关时 Tlash 会通过 uci 把 dnsmasq 的上游改为 mihomo（`127.0.0.1#1053`），并为 TUN 网卡添加防火墙区域；关闭或卸载时按原样还原。局域网设备无需任何设置。

## 开发

```sh
go test ./...                                     # 单元测试
TLASH_MIHOMO_BIN=/path/to/mihomo go test ./...    # 加上对真实 mihomo 的集成测试
scripts/build-release.sh v0.0.0-dev               # 交叉编译到 dist/
```

推送 `v*` 标签后，CI 会自动构建并发布 GitHub Release。
