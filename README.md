# Tlash

针对服务器的 Clash（[mihomo](https://github.com/MetaCubeX/mihomo)）终端管理器：SSH 登录后，在一个 TUI 里管理内核、节点和日志。

> 开发中，当前完成 M2 阶段，见 [计划文档](docs/PLAN.md)。一键安装脚本在 M4 实现，目前需要手动下载 tlash 二进制。

## 当前功能

- **概览**：内核状态与版本、启动 / 停止、出站模式切换（规则 / 全局 / 直连）、实时速率曲线、内存、连接数；首次使用时显示引导清单
- **代理**：按配置顺序列出代理组，选择节点，整组或单个节点测速，按延迟 / 名称排序，过滤，取消 URLTest / Fallback 的固定选择
- **订阅**：添加订阅链接或导入本地配置，更新，切换，显示流量和到期时间，用 `$EDITOR` 编辑
- **日志**：实时日志流，切换级别，过滤，暂停，导出到文件
- **内核**：从 GitHub 下载（稳定版 / Alpha，可设置下载镜像，sha256 校验），回滚，导入本地文件；systemd / OpenRC / procd / 后台进程托管与开机自启；接管端口、局域网访问、日志级别等设置
- 支持鼠标（点击侧栏切页，滚轮滚动）；按 `?` 查看当前页快捷键；退出 Tlash 不影响内核运行

## 使用

从 [Releases](https://github.com/Jlan45/Tlash/releases) 下载对应架构的压缩包（linux amd64 / arm64 / armv7 / 386），解压后运行 `tlash`，按概览页的引导：

1. 内核页按 `D` 下载内核（国内服务器可先按 `M` 设置下载镜像，或按 `i` 导入本地文件）
2. 订阅页按 `a` 添加订阅链接
3. 概览页按 `s` 启动

数据目录：root 为 `/etc/tlash`，普通用户为 `~/.local/share/tlash`，可用 `--dir` 指定。Tlash 生成的运行配置只通过 unix socket 暴露控制接口；需要 Web 面板时在内核页开启 TCP 控制接口（会强制设置密钥）。

连接一个不由 Tlash 管理的 mihomo（只能查看和切换，不管理配置与进程）：

```sh
tlash --controller /path/to/mihomo.sock
tlash --controller 127.0.0.1:9090 --secret <secret>
```

## 开发

```sh
go test ./...                                     # 单元测试
TLASH_MIHOMO_BIN=/path/to/mihomo go test ./...    # 加上对真实 mihomo 的集成测试
scripts/build-release.sh v0.0.0-dev               # 交叉编译到 dist/
```

推送 `v*` 标签后，CI 会自动构建并发布 GitHub Release。
