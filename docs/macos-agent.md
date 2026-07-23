# macOS 11+ Agent

`macos-agent/` 是独立的零依赖脚本 Agent。它只复用中心服务的 report v1 HTTP 协议，不导入 Linux Agent 的 Go 采集代码。Agent 使用 macOS 自带的 `zsh`、JXA、`sysctl`、`vm_stat`、`ifconfig`、`netstat` 和 `curl`，同时支持 Intel 与 Apple Silicon。

## 为什么不需要签名和公证

发布产物是文本形式的 `zsh` 脚本，不是 `.app`、`.pkg` 或 Mach-O 可执行文件。看板生成的命令通过 `curl` 下载脚本，安装器校验 Release 的 SHA-256 后将其交给 `/bin/zsh` 执行，因此不进入常规 App 分发和公证流程，也不要求目标机器安装 Homebrew、Python、Ruby、Node 或 Xcode Command Line Tools。

安装仍然需要管理员授权，因为 Agent 作为系统 `LaunchDaemon` 常驻。通过浏览器下载再从 Finder 双击脚本不属于支持的安装方式，也可能受到 quarantine 和 Gatekeeper 行为影响。

## 采集范围

| 类别 | 实现 |
| --- | --- |
| CPU | 芯片型号、物理/逻辑核心、Apple Silicon Performance/Efficiency 核心数、总使用率、1/5/15 分钟 load |
| 内存 | 物理总量、已用、可用、文件缓存、swap、运行时间 |
| 文件系统 | APFS/HFS/外部及网络挂载的设备、类型、挂载选项、容量和使用量 |
| 网络 | 接口、MAC、MTU、IPv4/IPv6、链路状态、收发累计量与区间增量 |
| 系统 | 主机名、macOS 版本、Darwin 内核、架构、Mac 型号和主 IP |

第一版不填充物理磁盘清单、磁盘读写 IOPS、单颗内存序列号和 GPU 指标。对应 report 字段保持空数组或零，不使用估算值冒充系统计数器。

## 发布构建

```bash
make build-macos-agent-release VERSION=0.13.0
```

产物为：

- `dist/release/server-status-agent-macos`
- `dist/release/checksums.txt`

脚本与 CPU 架构无关，同一份资产用于 Intel 和 Apple Silicon。Release 工作流会执行 `zsh -n`、检查注入的版本号，并把它与 Linux、Windows Agent 一起放入 GitHub Release 和中心镜像。

## 安装

部署本版本 Agent 前，中心数据库必须已按顺序执行到 `V008__arm_cpu_core_topology.sql`，并升级中心服务。否则中心无法持久化 Apple Silicon 的 P/E 核心拓扑。

1. 在中心看板点击“添加节点”。
2. 目标平台选择“macOS 11 及以上”。
3. 复制命令并在目标 Mac 的终端执行，按提示提供管理员密码。

命令只访问中心节点：

```bash
curl -fsSL 'https://中心节点/install-agent-macos.sh' | sudo env \
  SERVER_STATUS_URL='https://中心节点' \
  SERVER_STATUS_AGENT_ID='节点 Agent ID' \
  SERVER_STATUS_TOKEN='节点 Token' \
  SERVER_STATUS_AGENT_ENVIRONMENT='production' \
  sh
```

安装器依次下载脚本和 `checksums.txt`、验证 SHA-256、检查 `zsh` 语法、原子写入文件，然后通过 `launchctl bootstrap system` 启动服务并等待第一条报告被中心接受。

| 内容 | 路径 |
| --- | --- |
| Agent | `/Library/Application Support/ServerStatus/server-status-agent` |
| 配置 | `/Library/Application Support/ServerStatus/agent.env`，权限 `0600` |
| LaunchDaemon | `/Library/LaunchDaemons/com.guohai.server-status-agent.plist` |
| 标准日志 | `/Library/Logs/ServerStatus/agent.log` |
| 错误日志 | `/Library/Logs/ServerStatus/agent.error.log` |

Token 不写入 plist，也不作为 `curl` 命令行参数传递。首次安装命令和 shell 历史仍会包含 Token，生产环境应使用 HTTPS，安装后可清理相关历史记录。

## 运维与调试

```bash
sudo launchctl print system/com.guohai.server-status-agent
sudo launchctl kickstart -k system/com.guohai.server-status-agent
sudo tail -f '/Library/Logs/ServerStatus/agent.log'
sudo '/Library/Application Support/ServerStatus/server-status-agent' --version
```

只采集并打印一份 JSON、不发送到中心：

```bash
sudo '/Library/Application Support/ServerStatus/server-status-agent' print-report \
  '/Library/Application Support/ServerStatus/agent.env'
```

卸载服务时先执行：

```bash
sudo launchctl bootout system/com.guohai.server-status-agent
```

随后可删除上述 Agent、配置、plist 和日志路径。

## 自动升级

正式版本成功上报后，中心会在 Agent 版本低于中心版本时返回目标版本。macOS Agent 只接受更高的语义版本，并执行：

1. 从中心 Release 缓存下载目标版本的 `checksums.txt` 和 `server-status-agent-macos`。
2. 校验资产 SHA-256、`zsh` 语法和脚本自身报告的版本号。
3. 在当前安装目录创建临时文件并原子替换脚本，保留原所有者和 `0755` 权限。
4. 正常退出，由 `launchd` 的 `KeepAlive` 立即启动新版本。

任何下载或校验失败都不会覆盖当前脚本，Agent 会继续运行并在后续上报时重试。配置文件不会被自动升级修改，因此 Agent ID、Node Token、环境标签和采集周期保持不变。也可以重新执行看板安装命令完成手动升级。
