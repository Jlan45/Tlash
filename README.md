# Tlash

针对服务器的 Clash（[mihomo](https://github.com/MetaCubeX/mihomo)）终端管理器：SSH 登录后，在一个 TUI 里管理内核、节点和日志。

> 开发中，当前完成 M1 阶段，见 [计划文档](docs/PLAN.md)。现阶段 Tlash 连接一个**已经在运行**的 mihomo；内核安装、订阅管理和一键安装脚本在后续里程碑中实现。

## 当前功能

- **概览**：内核状态与版本、出站模式切换（规则 / 全局 / 直连）、端口与 TUN 状态、实时速率曲线、内存、连接数，重启内核，关闭所有连接
- **代理**：按配置顺序列出代理组，选择节点，整组或单个节点测速，按延迟 / 名称排序，过滤，取消 URLTest / Fallback 的固定选择
- **日志**：实时日志流，切换级别，过滤，暂停，导出到文件
- 支持鼠标（点击侧栏切页，滚轮滚动）；按 `?` 查看当前页快捷键

## 使用

从 [Releases](https://github.com/Jlan45/Tlash/releases) 下载对应架构的压缩包（linux amd64 / arm64 / armv7 / 386），解压后运行：

```sh
# 优先使用 unix socket（mihomo 配置 external-controller-unix: mihomo.sock）
tlash --controller /path/to/mihomo.sock

# 或 TCP 控制接口
tlash --controller 127.0.0.1:9090 --secret <secret>
```

不指定 `--controller` 时，依次尝试 `/etc/tlash/runtime/mihomo.sock` 和 `127.0.0.1:9090`。也可以用环境变量 `TLASH_CONTROLLER`、`TLASH_SECRET`。退出 Tlash 不会影响内核运行。

## 开发

```sh
go test ./...                                     # 单元测试
TLASH_MIHOMO_BIN=/path/to/mihomo go test ./...    # 加上对真实 mihomo 的集成测试
scripts/build-release.sh v0.0.0-dev               # 交叉编译到 dist/
```

推送 `v*` 标签后，CI 会自动构建并发布 GitHub Release。
