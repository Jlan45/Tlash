# Tlash 计划文档

> 状态：**已确认，M3 已完成**　·　日期：2026-11-11
>
> Tlash 是运行在服务器上的 mihomo TUI 管理器：通过 SSH 登录后，用一个终端界面完成内核安装、订阅管理、节点切换、配置覆写、连接/日志查看和透明代理开关。

## 1. 定位与边界

| | 说明 |
|---|---|
| 目标用户 | 无桌面环境的 Linux 服务器 / VPS / 软路由 / 容器，通过 SSH 操作 |
| 是什么 | mihomo 的**管理器**：管内核进程、管配置生成、通过 external-controller API 做运行时操作 |
| 不是什么 | 不实现任何代理协议，不 fork mihomo，不做 Web 面板（但可以一键安装 metacubexd / zashboard） |
| 首发平台 | Linux（amd64 / arm64 / armv7；mips 系列视需求追加）。macOS / Windows 不在范围内 |

三个参考项目各取什么：

| 参考 | 借鉴内容 | 不照搬的部分 |
|---|---|---|
| **Sparkle** | 页面划分（代理 / 订阅 / 覆写 / 连接 / 规则 / 日志 / 内核 / TUN / DNS / 嗅探 / 资源 / 设置）、“订阅 + 覆写 → 运行配置”的生成模型、内核稳定版/预览版切换、WebDAV 备份 | 系统代理页（服务器无桌面，改为“代理环境变量”）、JS 覆写、Sub-Store（放到后期可选） |
| **OpenClash** | 运行模式概念（fake-ip / redir-host / TUN / 混合）、多订阅切换、自定义 UA、GEO 数据与规则集更新、面板一键安装、定时任务、绕过大陆/局域网 | LuCI 依赖、OpenWrt 专属的防火墙脚本 |
| **ShellCrash** | 一行命令安装、镜像源选择、安装目录选择、覆盖安装保留配置、init 系统自动识别（systemd / procd / openrc / 兜底）、命令别名、在线更新、卸载 | 纯 shell 菜单式交互、各品牌路由器固件的专属 hook（先只做通用 Linux + OpenWrt） |

## 2. 技术选型

**Go + Bubble Tea（Charm 全家桶）**，产物为单个静态二进制（`CGO_ENABLED=0`）。

- 与 mihomo 同语言，可直接复用其配置结构的理解；交叉编译到各架构零成本
- 静态单文件最适合“一行命令安装”，不依赖目标机的 glibc 版本、Python、Node
- Bubble Tea 是目前最成熟的 TUI 框架，带鼠标支持、组件库（Bubbles）、样式库（Lip Gloss）、测试工具（teatest）

主要依赖：`bubbletea` / `bubbles` / `lipgloss`（界面）、`yaml.v3`（配置合并，需保留键顺序）、一个 WebSocket 库（流式接口）、`cobra`（子命令）。

## 3. 总体架构

```
            ┌────────────────────────── tlash（单二进制）──────────────────────────┐
 SSH 终端 ──▶│  TUI 页面层   │  CLI 子命令（start/stop/sub update/task run …）      │
            ├───────────────┴──────────────────────────────────────────────────────┤
            │ 订阅管理 │ 覆写管理 │ 运行配置生成 │ 内核文件管理 │ 服务管理 │ 定时任务 │
            └────┬───────────────────────┬────────────────────────────┬───────────┘
                 │ REST + WebSocket      │ 写 config.yaml / mihomo -t  │ systemctl 等
                 ▼ (unix socket)         ▼                             ▼
            ┌─────────────────────── mihomo 内核进程 ───────────────────────┐
            └───────────────────────────────────────────────────────────────┘
```

三个关键设计决定：

1. **不设常驻守护进程。** mihomo 由系统的 init（systemd / openrc / procd）直接托管，崩溃自拉起、开机自启都交给它；Tlash 只在打开 TUI 或被定时任务调用时运行。定时任务用 systemd timer 或 cron 调 `tlash task run <名称>`。没有 init 系统时（部分容器）退化为“后台进程 + pidfile + cron 保活”。
   - 好处：少一个要维护、要保活的进程；TUI 退出不影响代理。
2. **控制接口默认走 unix socket**（mihomo 的 `external-controller-unix`）。服务器有公网 IP，默认不监听 TCP 控制端口可以避免把 API 暴露出去；需要 Web 面板时再显式开启 TCP 端口并强制设置随机 secret。
3. **用户的订阅文件永不被改写。** 运行配置每次都由“订阅原文 + 覆写 + Tlash 接管的设置”重新生成（见 §5）。

## 4. 界面设计

布局沿用 Sparkle 的“左侧导航 + 右侧内容”，底部是状态栏和按键提示。最小支持 80×24，窄终端下侧栏收成图标/数字。

```
┌ Tlash ──────────────────────────────────────────────────────────────────────┐
│ 1 概览    │ 代理组                                    规则模式  ↑1.2M ↓8.4M │
│ 2 代理  ◀ │ ▼ 🚀 节点选择  [Selector]  当前: 香港 01                        │
│ 3 订阅    │   ● 香港 01      42ms    ○ 香港 02      58ms   ○ 日本 01  91ms  │
│ 4 覆写    │   ○ 新加坡 01   120ms    ○ 美国 01     187ms   ○ 美国 02  超时  │
│ 5 连接    │ ▶ ♻️ 自动选择  [URLTest]   当前: 香港 01                        │
│ 6 规则    │ ▶ 🎬 流媒体    [Selector]  当前: 新加坡 01                      │
│ 7 日志    │ ▶ 🐟 漏网之鱼  [Selector]  当前: 节点选择                       │
│ 8 内核    │                                                                 │
│ 9 网络    │                                                                 │
│ 0 设置    │                                                                 │
├───────────┴─────────────────────────────────────────────────────────────────┤
│ ● 运行中 v1.19.33 │ 订阅: 机场A │ 内存 48M │ 连接 37     enter 选择  t 测速 │
└─────────────────────────────────────────────────────────────────────────────┘
```

通用操作：数字键 / `Tab` 切页，`j/k` 或方向键移动，`/` 过滤，`Enter` 确认，`?` 当前页帮助，`q` 退出；支持鼠标点击和滚轮。颜色自适应明暗终端，遵守 `NO_COLOR`，在 16 色终端下可降级。界面语言中文 / 英文可切换。

### 页面清单

| # | 页面 | 功能 | 对应参考 |
|---|---|---|---|
| 1 | **概览** | 内核状态与版本、启动/停止/重启、出站模式（规则/全局/直连）切换、实时上下行曲线、内存、连接数、当前订阅及流量/到期、出口 IP 检测 | Sparkle 侧栏卡片 |
| 2 | **代理** | 代理组折叠列表、节点选择、单节点/整组测速、按延迟/名称排序、过滤、代理集（proxy-provider）更新与健康检查 | Sparkle proxies |
| 3 | **订阅** | 添加（URL / 本地文件）、更新、自动更新间隔、自定义 UA、切换当前订阅、显示已用流量与到期时间、用 `$EDITOR` 编辑、删除 | Sparkle profiles、OpenClash 订阅 |
| 4 | **覆写** | YAML 覆写片段的增删改、启用/停用、排序、全局生效或绑定到某个订阅；预览最终生成的配置与差异 | Sparkle override、OpenClash 覆写 |
| 5 | **连接** | 实时连接表（主机、规则、代理链、速率、流量、时长）、排序、过滤、查看详情、关闭单个/全部 | Sparkle connections |
| 6 | **规则** | 规则列表与搜索、命中统计、临时停用规则（内核版本支持时）、规则集（rule-provider）更新 | Sparkle rules |
| 7 | **日志** | 实时日志流、级别切换、关键字过滤、暂停/继续、导出 | Sparkle logs |
| 8 | **内核** | 端口（mixed / http / socks / redir / tproxy）、allow-lan、IPv6、日志级别、统一延迟、TCP 并发、进程匹配、控制接口与 secret；内核版本管理（稳定版/Alpha 切换、升级、回滚到上一版本）；开机自启 | Sparkle mihomo |
| 9 | **网络** | 分三个子页：**TUN / 透明代理**（见 §7）、**DNS**（增强模式 fake-ip / redir-host、上游、fallback、nameserver-policy、fake-ip 过滤）、**嗅探** | Sparkle tun / dns / sniffer、OpenClash 运行模式 |
| 0 | **设置** | 资源（GEO 数据库更新、Web 面板安装）、定时任务、代理环境变量、镜像源、备份/恢复（本地 + WebDAV）、语言与主题、自更新、卸载 | Sparkle resources / settings、ShellCrash 更新与卸载 |

“代理环境变量”是服务器上对“系统代理”的替代：一键生成 `/etc/profile.d/tlash-proxy.sh`，以及 `eval "$(tlash env)"` 这类临时用法。

## 5. 配置生成模型

```
订阅原文 (profiles/<id>.yaml)          ← 只读，只由“更新订阅”写入
      │
      ├─ 依次合并已启用的覆写片段 (overrides/*.yaml)
      │     · 映射深度合并；列表支持前插 / 追加 / 整体替换三种写法
      │
      ├─ 应用 Tlash 接管的设置 (tlash.yaml 中的 端口 / 控制接口 / TUN / DNS / 嗅探 …)
      │     · 每个接管项都可单独切回“使用订阅自带的值”
      ▼
runtime/config.yaml  →  mihomo -t 校验  →  通过则热重载（PUT /configs）或重启
                              └ 失败：保留上一份可用配置，界面显示内核报错原文
```

- 订阅更新后自动重新生成并校验，校验失败不替换正在运行的配置
- 每次成功应用都保留上一份 `config.yaml`，可一键回退
- 订阅 URL 含有 token：相关文件权限 `0600`、目录 `0700`，日志中打码显示

## 6. 安装模式（参考 ShellCrash）

```sh
# 直连 GitHub
sh -c "$(curl -fsSL https://raw.githubusercontent.com/<owner>/Tlash/main/scripts/install.sh)"
# 指定镜像源（国内服务器在装好代理之前访问不了 GitHub，这是必须的）
url=https://<镜像>/Tlash sh -c "$(curl -fsSL https://<镜像>/Tlash/scripts/install.sh)"
```

`install.sh` 用 POSIX sh 编写（兼容 busybox ash），只负责“把二进制放到位”，其余交给 `tlash` 自身：

1. **环境检测**：架构（含 amd64 的 v1/v2/v3 指令集级别）、init 系统、是否 root、是否容器、`curl`/`wget` 哪个可用
2. **选择下载源**：GitHub 直连 / 内置镜像列表 / `url=` 环境变量自定义
3. **选择安装目录**：
   - 系统级（root，推荐）：`/etc/tlash`
   - 用户级（非 root）：`~/.local/share/tlash`，使用 `systemd --user`，不提供 TUN
   - 自定义路径（拒绝 `/tmp` 等易失目录，检查可写与剩余空间）
4. **已有安装的处理**：覆盖安装（保留配置）/ 卸载后重装 / 取消
5. **下载并校验** `tlash` 二进制（SHA-256），放到 `/usr/local/bin/tlash`（用户级为 `~/.local/bin`）
6. 执行 `tlash init` 进入**首次运行向导**：下载 mihomo 内核 → 注册服务 → 添加第一个订阅 → 启动

其他入口：

- **离线安装**：下载发布页的整合包（tlash + mihomo + GEO 数据），解压后 `./install.sh --offline`；也支持在向导里从本地文件导入内核
- **更新**：`tlash update`（自身）/ 内核页升级内核，均可选稳定版或预览版通道
- **卸载**：`tlash uninstall`，可选保留配置；移除服务、定时任务、环境变量文件和由 Tlash 写入的路由/防火墙规则

安装后的目录结构（以系统级为例，全部集中在一个目录下便于备份和迁移）：

```
/etc/tlash/
├── tlash.yaml            Tlash 自身设置与接管项
├── profiles/             订阅原文 + 元数据（URL、UA、更新间隔、流量信息）
├── overrides/            覆写片段
├── core/                 mihomo 二进制（当前版本 + 上一版本）
├── runtime/              config.yaml、cache.db、GEO 数据、规则集缓存、ui/、mihomo.sock
└── backups/              配置备份
```

服务注册：systemd → `tlash-mihomo.service`；OpenWrt → `/etc/init.d/tlash`（procd）；Alpine → openrc 脚本；都没有 → 后台进程 + cron 保活。

## 7. 透明代理与服务器安全

服务器场景的三个档位，默认停在第一档：

| 档位 | 做法 | 风险 |
|---|---|---|
| **仅代理端口**（默认） | mixed-port + 代理环境变量 | 无 |
| **TUN** | 开启 mihomo 的 TUN（auto-route / auto-redirect），由内核自己维护路由和 nftables；可同时作为局域网网关 | 可能切断当前 SSH 会话、影响本机对外提供的服务 |
| **Tlash 托管防火墙规则**（redirect / tproxy，绕过大陆 IP、按设备黑白名单） | 像 OpenClash / ShellCrash 那样自己写 nftables 规则 | 工作量大，规则与发行版/防火墙前端耦合深 |

在远程服务器上开 TUN 最大的危险是**把自己锁在外面**。计划的保护措施：

- **确认或回滚**：开启 TUN 后要求在 60 秒内按键确认，否则自动恢复原状。倒计时由独立于 SSH 会话的后台进程执行，会话断了也能回滚
- 开启前自动把当前 SSH 客户端地址加入路由排除，并检查本机是否有对外监听的服务，给出提示
- 启动内核前检测端口占用和 53 端口冲突（如 systemd-resolved）

第三档建议放到最后、视实际需求决定做不做。

## 8. 代码结构

```
cmd/tlash/               入口与子命令
internal/
  mihomoapi/             external-controller 客户端（REST + WebSocket，unix socket / TCP）
  corebinary/            内核下载、校验、版本切换、架构识别
  coreservice/           systemd / openrc / procd / 后台进程 四种托管方式
  profile/               订阅的获取、存储、流量信息解析
  override/              覆写片段与合并规则
  runtimeconfig/         运行配置生成、校验、应用、回退
  appsettings/           tlash.yaml 读写
  scheduledtask/         定时任务（timer / cron 注册与执行）
  downloadsource/        镜像源与下载
  tui/                   应用外壳、各页面、通用组件、按键表、主题
  i18n/                  中英文文案
scripts/install.sh
docs/
```

## 9. 里程碑

每个里程碑结束时都是可运行、可演示的状态。

| 阶段 | 内容 | 完成标志 |
|---|---|---|
| **M1 骨架与只读界面** | 工程初始化、API 客户端、TUI 外壳、概览 / 代理 / 日志三页 | 对着一个已在运行的 mihomo 能看状态、切节点、测速、看日志 |
| **M2 内核与配置** | 内核下载与托管、订阅管理、配置生成与校验、内核设置页 | 在一台干净机器上，从零到“添加订阅并跑起来”全程在 TUI 内完成 |
| **M3 补齐页面** | 覆写、连接、规则、DNS、嗅探、资源、定时任务、代理环境变量 | §4 页面清单除 TUN 外全部可用 |
| **M4 安装与发布** | `install.sh`、首次运行向导、多架构发布流水线、自更新、卸载、离线包 | 一行命令在 Debian / Ubuntu / Alpine / OpenWrt 上装好并能卸干净 |
| **M5 TUN 与收尾** | TUN 开关与确认回滚机制、备份恢复（本地 + WebDAV）、英文文案、文档 | 远程开 TUN 误操作后能自动恢复 |
| **后续可选** | Tlash 托管防火墙规则、Sub-Store 集成、JS 覆写、流媒体解锁检测 | 按需求排期 |

## 10. 测试方式

- **单元测试**：配置合并规则、订阅流量信息解析、架构识别、服务单元文件生成
- **API 客户端**：对接本地假服务端；另有一组测试在 CI 中拉起真实 mihomo 二进制跑通
- **界面**：用 teatest 做页面级快照测试
- **安装脚本**：在 Debian / Alpine / OpenWrt 容器里跑“安装 → 启动 → 卸载”全流程
- TUN 的确认回滚机制需要在真实虚拟机上手工验证，容器内无法完整覆盖

## 11. 已确认的决策（2026-11-11）

1. 技术栈：Go + Bubble Tea
2. 透明代理：首版做到 TUN；Tlash 托管 nftables 规则放到后续可选
3. 不设常驻守护进程，mihomo 交给 init 系统托管，定时任务走 timer / cron
4. 目标系统：systemd 发行版 + Alpine + OpenWrt，不做各品牌路由器固件
5. 发布：GitHub 公开仓库（Jlan45/Tlash），由 GitHub Actions 自动构建和发布 Release
6. 界面语言：中文为主，英文可切换

## 进度

- **M1 已完成**：mihomo API 客户端（unix socket / TCP，REST + 流式接口）、TUI 外壳（侧栏、状态栏、帮助、确认弹窗、鼠标）、概览 / 代理 / 日志三页；单元测试 + 对真实 mihomo 的集成测试；CI 与 tag 触发的 Release 流水线
- M1 推迟到后续的小项：概览页的出口 IP 检测（M3，需要选定检测服务）；英文文案（M5）
- **M2 已完成**：
  - 内核：按 CPU 指令集（amd64 v1/v2/v3）选择资产，稳定版 / Alpha 通道，通过 `version.txt` 解析版本（可走下载镜像），用 GitHub API 的资产摘要做 sha256 校验（API 不可达时跳过并提示），保留上一版本可回滚，支持导入本地文件
  - 服务托管：systemd（系统级 / 用户级）、OpenRC、procd、后台进程四种，自动识别；开机自启开关
  - 订阅：添加链接（自定义 UA、可经内核代理下载）、导入本地文件、更新 / 全部更新、流量与到期信息、`$EDITOR` 编辑后自动校验、删除
  - 运行配置：订阅原文 + 接管设置生成，`mihomo -t` 校验通过才替换，运行中自动热重载，保留上一份可回退；控制接口固定为 unix socket，TCP 接口需显式开启且强制密钥
  - TUI：订阅页、内核页（版本 / 服务 / 接管设置），概览页启停内核与首次使用引导，状态栏后台任务进度
- M2 与计划的差异：订阅“自动更新间隔”目前只保存，定时执行随 M3 的定时任务一起实现；配置回退（`RollbackConfig`）已实现但界面入口放到 M3 的覆写页
- **M3 已完成**：
  - 覆写：YAML 片段按顺序合并（映射深度合并，`+key` 前插、`key+` 追加、`key!` 整体替换），可启用 / 停用、排序、限定作用订阅，编辑后自动校验，预览最终配置，回退上一份运行配置
  - 连接：实时连接表（速率、流量、时长、规则、链路），排序、过滤、详情、关闭单个 / 全部；只在页面可见时轮询
  - 规则：规则列表与命中次数、临时停用、规则集列表与更新；代理页新增代理集更新
  - 网络：DNS 与嗅探可由 Tlash 接管（带推荐默认值，DNS 只覆盖接管的键、保留 nameserver-policy 等）；DNS 查询、清空 Fake-IP / DNS 缓存
  - 设置：GEO 数据库状态与更新、资源下载镜像（geox-url）、Web 面板（metacubexd / zashboard）、定时任务（systemd timer 或 cron，每 15 分钟执行 `tlash task run-due`，按订阅间隔和 GEO 间隔更新）、代理环境变量（`tlash env`，root 可写入 `/etc/profile.d`）
  - 概览：出口 IP 检测（经 api.ip.sb，对比代理与直连）
  - 侧栏按计划排成 10 页，`0` 键对应第 10 页；外部内核模式只显示运行时相关的 6 页
- M3 与计划的差异：TUN 仍按计划放在 M5
- **界面改版（2026-11-11）**：改为 lazydocker 式操作，详见 [TUI-REDESIGN.md](TUI-REDESIGN.md)。§4 的页面清单相应合并为 7 个面板：日志并入“状态”，内核 / 网络 / 设置三页并入“设置”的分类
- **安装脚本提前完成**：`scripts/install.sh`（原属 M4），已发布 v0.1.0-alpha.1 供测试；M4 剩余：首次运行向导、自更新、离线包、多发行版容器测试
- **路由器支持（2026-11-12 确认）**：顺序 R1 本机 / 旁路由 → R2 设备与分流 → R3 OpenWrt 主路由；代理范围黑名单与白名单都支持；大陆 IP 内核层放行与交给规则都支持；仅 IPv4 与 IPv4 + IPv6 作为选项；目标设备覆盖 x86、ARM、MIPS 路由器（R3 增加 mips / armv5 / armv6 构建与“内核放内存”）
- **R1、R2 已完成**：基于 mihomo TUN（auto-route + auto-redirect + dns-hijack）；Tlash 负责环境检查、IP 转发（含 sysctl 持久化）、FORWARD 为 DROP 时插入放行规则、服务启动前恢复（ExecStartPre / start_pre）、SSH 客户端自动排除、60 秒确认否则由脱离会话的 guard 进程回滚、`tlash gateway off` 救急；局域网设备从 ARP 与 DHCP 租约发现，按 MAC 黑 / 白名单；大陆 IP 用 `route-exclude-address-set` 在内核层放行。真实网络效果需在 root 环境手工验证
- **R3 已完成**：新增 mips / mipsle（软浮点）、armv5、armv6、riscv64 构建，内核按 Tlash 自身的 GOARM / GOMIPS 选择资产；安装脚本识别 OpenWrt（装到 /usr/bin、mips 大小端、依赖与闪存提示，卸载前先还原网关）；网关在 OpenWrt 上通过 uci 让 dnsmasq 转发到 mihomo（默认 127.0.0.1:1053）并添加 fw4 防火墙区域，关闭时还原；内核存放位置可选数据目录 / 压缩存放 / 只放内存，开机由 `tlash prestart`（procd、systemd、OpenRC 启动钩子）恢复；CI 在 OpenWrt 24.10 容器中验证真实 uci 的修改与还原、安装与卸载。真实路由器上的透明代理效果仍需手工验证

## 12. 已知的不确定项

- mihomo 发布页没有统一的校验和文件，内核下载的完整性校验方式（GitHub API 的资产摘要 / 自建校验清单）需要在 M2 时确认
- “临时停用规则”等较新的 API 需要按内核版本做能力探测，旧内核上隐藏对应入口
- 非 root 用户级安装下，低位端口和 TUN 均不可用，功能会明显受限，是否值得支持可在 M4 再评估
