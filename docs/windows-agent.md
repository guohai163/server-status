# Windows Server 2008 R2+ Agent

`windows-agent/` 是独立于 Linux Agent 的兼容实现。它只复用中心服务的 report v1 HTTP 协议，不导入 `internal/agent`，也不参与 Linux Agent 的安装和自动更新流程。

## 兼容范围

- Windows Server 2008 R2 及以上：`amd64`
- 构建工具链固定为 Go 1.20.14，这是支持 Windows Server 2008 R2/2012 的最后一个 Go 大版本
- 以 `LocalSystem` 身份注册为 `ServerStatusAgent` 自动启动服务

Windows Agent 使用独立的 Go Module，并通过 `golang.org/x/sys/windows/svc` 接入服务控制管理器。Go 1.21 起最低要求 Windows Server 2016，因此不能使用根模块的当前 Go 工具链构建此兼容产物。

## 采集内容

| 类别 | 实现 |
| --- | --- |
| CPU | `GetSystemTimes` 使用率、处理器标识、逻辑处理器数 |
| 内存 | `GlobalMemoryStatusEx` 总量、已用、可用、分页文件容量 |
| 文件系统 | 逻辑卷、文件系统类型、卷序列号、总量、已用、可用 |
| 网络 | 接口、MAC、MTU、IP、链路状态、速率和 `GetIfTable` 流量增量 |
| 系统 | 主机名、Windows 版本、Build、架构、运行时间 |

旧版 `GetIfTable` 只提供 32 位原始计数，Agent 会在常驻进程内处理回绕并扩展为 64 位累计值。

第一版不采集物理磁盘型号/SMART、单条内存序列号、磁盘 IOPS 和 GPU。对应 report 字段保持为空或为零，不伪造硬件信息。

## 发布构建

安装 Go 1.20.14 后执行：

```bash
make build-windows-agent-release VERSION=0.8.0
```

发布目录新增：

- `server-status-agent-windows-amd64.exe`

Release 工作流会先用当前 Go 构建 Linux Agent，再切换到 Go 1.20.14 构建 Windows Agent，并将所有 Agent 二进制写入同一份 `checksums.txt`。

## 安装

1. 在中心看板点击“添加节点”。
2. 目标平台选择 Windows Server 2008 R2+ 64 位。
3. 在目标机器以管理员身份打开命令提示符或 PowerShell，执行看板生成的两行命令。

生成的第一行命令会显式调用系统自带的 `powershell.exe`，通过 `.NET WebClient` 下载到进程当前目录，因此可以同时从命令提示符和 PowerShell 执行。下载完成后，第二行使用 `.\` 形式从当前目录运行 Agent：

```bat
.\server-status-agent.exe install --server "http://central:8080" --id "AGENT_UUID" --token "NODE_TOKEN" --environment "production"
```

安装程序执行以下操作：

1. 写入 `%ProgramFiles%\ServerStatus\server-status-agent.exe`。
2. 写入 `%ProgramFiles%\ServerStatus\agent.json`，并将目录、配置和二进制的 DACL 限制为 LocalSystem 与 Administrators。
3. 创建自动启动的 `ServerStatusAgent` Windows 服务。
4. 以 LocalSystem 启动服务。

Token 会出现在首次安装命令和命令提示符历史中。安装完成后应关闭该窗口；配置文件由 ACL 保护。生产网络应使用支持 TLS 1.2 且证书受旧系统信任的 HTTPS 中心地址，否则 Node Token 会以明文经过网络。

## 运维命令

```bat
server-status-agent.exe status
server-status-agent.exe stop
server-status-agent.exe start
server-status-agent.exe remove
server-status-agent.exe remove --purge
```

已有 Windows 节点可以在中心看板的节点详情页点击“更新 Agent”，复制并以管理员身份执行生成的命令。命令通过 `powershell.exe` 和 `.NET WebClient` 下载中心缓存的最新 Windows Agent，不依赖已弃用的 BITSAdmin，然后执行：

```bat
server-status-agent-upgrade.exe upgrade
```

`upgrade` 从现有 `%ProgramFiles%\ServerStatus\agent.json` 读取 Agent ID、Node Token、标签和采集周期，停止服务、替换 EXE 并重新启动，不会重新注册节点或改写凭据。

配置与日志：

| 内容 | 路径 |
| --- | --- |
| 二进制 | `%ProgramFiles%\ServerStatus\server-status-agent.exe` |
| 配置 | `%ProgramFiles%\ServerStatus\agent.json` |
| 日志 | `%ProgramFiles%\ServerStatus\agent.log` |

`remove` 默认保留配置和日志，`remove --purge` 会删除整个安装目录。

## 调试运行

服务安装前可以在管理员命令提示符中前台运行：

```bat
server-status-agent.exe run --config "C:\path\agent.json"
```

Windows 兼容 Agent 不执行中心下发的自动更新指令。发布前需要在 Windows Server 2008 R2 实机验证，再通过节点详情页生成的更新命令完成升级。
