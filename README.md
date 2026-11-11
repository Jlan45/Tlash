# Tlash

针对服务器的 Clash（[mihomo](https://github.com/MetaCubeX/mihomo)）终端管理器：SSH 登录后，在一个 TUI 里管理内核、节点和日志。

> 开发中，当前完成 M3 阶段，见 [计划文档](docs/PLAN.md)。一键安装脚本在 M4 实现，目前需要手动下载 tlash 二进制。

## 界面

参照 lazydocker：左侧 7 个面板同时可见（状态、订阅、代理组、覆写、连接、规则、设置），右侧主面板显示当前选中项的详情，分多个标签。

| 按键 | 作用 |
|---|---|
| `←` `→` / `h` `l` / `Tab`，`1`–`7` | 切换面板 |
| `j` `k` / `↑` `↓` | 列表内移动 |
| `Enter` / `Esc` | 进入主面板 / 返回 |
| `[` `]` | 切换主面板标签 |
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
- **设置**：内核下载与回滚、服务托管与开机自启、接管设置（端口 / 局域网 / 日志级别 / 控制接口等）、DNS 与嗅探、GEO 与 Web 面板、定时任务、代理环境变量（`eval "$(tlash env)"`）、DNS 查询等工具

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/Jlan45/Tlash/main/scripts/install.sh | sh
```

- 国内服务器经镜像下载：`curl -fsSL https://ghfast.top/https://raw.githubusercontent.com/Jlan45/Tlash/main/scripts/install.sh | mirror=https://ghfast.top/ sh`
- 指定版本：`... | version=v0.1.0-alpha.1 sh`
- 卸载（保留数据）：`... | sh -s uninstall`；连数据一起删除：`... | sh -s purge`

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

## 开发

```sh
go test ./...                                     # 单元测试
TLASH_MIHOMO_BIN=/path/to/mihomo go test ./...    # 加上对真实 mihomo 的集成测试
scripts/build-release.sh v0.0.0-dev               # 交叉编译到 dist/
```

推送 `v*` 标签后，CI 会自动构建并发布 GitHub Release。
