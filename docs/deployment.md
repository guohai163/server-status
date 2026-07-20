# 当前部署

## 中央服务

- SSH：`gydev@10.12.54.200`
- 目录：`/home/gydev/server-status-central`
- 容器：`server-status-central`
- HTTP：`http://10.12.54.200:8080`
- 配置：`/home/gydev/server-status-central/.env`，权限 `0600`
- 镜像：`ghcr.io/guohai163/server-status-central:0.4.3`
- 重启策略：Docker `unless-stopped`
- 容器安全：非 root 用户、只读根文件系统、移除全部 Linux capabilities
- Agent Release 缓存：Compose 持久卷 `agent_release_cache`，容器内路径 `/var/cache/server-status`

检查：

```bash
ssh gydev@10.12.54.200 'docker ps --filter name=server-status-central'
curl http://10.12.54.200:8080/readyz
```

`scripts/deploy-central.sh` 会上传 `compose.yaml` 与 `.env`，在中心机执行 `docker compose pull` 后重建容器，不再上传源码或在中心机编译镜像。

浏览器访问 `http://10.12.54.200:8080/` 即可打开无需鉴权的节点看板。公开范围只包括状态展示接口；节点上报和节点注册仍需要各自的 Bearer Token。

数据库使用专用登录角色 `server_status_app`，该角色只继承 `server_status_writer`，没有超级用户、建库或建角色权限。

## 节点 Agent

新节点的推荐接入方式是在中心看板点击“添加节点”，填写显示名称与环境后，在目标 Linux 节点执行生成的命令。安装器只访问中心节点，由中心按需从 GitHub Release 下载、校验并持久缓存 `amd64` 或 `arm64` 静态二进制；因此目标节点无需访问 GitHub。安装器会再次校验 SHA-256，随后以 root 安装到：

- 二进制：`/opt/server-agent/server-status-agent`
- 配置：`/opt/server-agent/agent.env`，权限 `0600`
- 日志：`/var/log/server-status-agent.log`
- 守护方式：root crontab 的 `@reboot` 启动和每 5 分钟存活检查

目标节点只需要 Linux、`sudo`、`curl`、`crontab` 与 `sha256sum`，不需要 Go、make、Python、仓库副本或 SSH 服务。当前列出的长期验证节点仍保留旧版用户目录部署，重新执行中心生成的新安装命令后才会迁移到 `/opt/server-agent`。

### 当前旧版节点

- SSH：`gydev@10.12.54.169`
- 二进制：`/home/gydev/.local/lib/server-status-agent/server-status-agent`
- 配置：`/home/gydev/.config/server-status-agent/env`，权限 `0600`
- 日志：`/home/gydev/.local/state/server-status-agent/agent.log`
- 采集周期：1 分钟

目标账号没有免密 sudo，且 user linger 未启用，因此当前使用 `nohup` 运行，并安装两个用户 crontab 项：开机启动和每 5 分钟存活检查。Agent 自带文件锁，即使两个启动路径同时触发也只会保留一个进程。

检查：

```bash
ssh gydev@10.12.54.169 'pgrep -af server-status-agent'
ssh gydev@10.12.54.169 'tail -f ~/.local/state/server-status-agent/agent.log'
```

该节点是虚拟机，普通用户不能读取 DMI 内存表，Virtio 设备也没有公开具体磁盘型号。Agent 会分别报告 `System Memory (DMI unavailable)` 和 `Virtio Block Device (vda)`，同时正常上报数量、容量及使用率。

### 长期验证节点

以下节点保留常驻部署，供中心看板和接口联调：

| SSH | 系统 | Agent | 守护方式 |
| --- | --- | --- | --- |
| `root@10.12.54.140:63008` | CentOS 7.8.2003 | `0.3.2` | root crontab 开机启动及每 5 分钟存活检查 |
| `root@10.12.54.1:63008` | RHEL 6.4 | `0.3.2` | root crontab 开机启动及每 5 分钟存活检查 |

两台 root 节点的二进制、配置和日志分别位于 `/root/.local/lib/server-status-agent/server-status-agent`、`/root/.config/server-status-agent/env` 和 `/root/.local/state/server-status-agent/agent.log`。配置权限为 `0600`。RHEL 6.4 节点只提供旧版 `ssh-rsa`，部署时需要设置 `SERVER_STATUS_AGENT_LEGACY_SSH=1`。

检查：

```bash
ssh -p 63008 root@10.12.54.140 'pgrep -af server-status-agent'
ssh -p 63008 -o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa root@10.12.54.1 'pgrep -af server-status-agent'
curl http://10.12.54.200:8080/api/v1/nodes
```

## 更新

中心使用：

```bash
SERVER_STATUS_CENTRAL_ENV_FILE=/secure/central.env scripts/deploy-central.sh
```

新节点和后续版本更新默认使用中心看板生成的安装命令。留空 Agent 版本时安装最新稳定 Release；填写语义版本时固定下载对应 Release。重复执行同一条命令会原子替换二进制、刷新配置与守护记录并验证首条上报。

兼容的旧版远程部署仍可从仓库执行；它需要本机 Go、make、Python、SSH/SCP 及免交互登录权限：

```bash
./scripts/deploy-agent.sh
```

安装流程不会在仓库保存数据库密码、Admin Token 或 Node Token。Agent 更新采用临时文件后原子替换，避免覆盖运行中二进制时出现 `Text file busy`。

## 发布产物

推送语义版本 tag 后，GitHub Actions 会在对应 GitHub Release 发布以下文件：

- `server-status-agent-linux-amd64`：Ubuntu/CentOS/RHEL x86_64 静态二进制。
- `server-status-agent-linux-arm64`：Ubuntu/CentOS/RHEL arm64 静态二进制。
- `checksums.txt`：两个二进制的 SHA-256 校验值。

中心服务镜像发布到 `ghcr.io/guohai163/server-status-central`，支持 `linux/amd64` 和 `linux/arm64`。正式版本可用完整版本号拉取，`latest` 始终指向最后一次稳定 tag 构建。

兼容性基线在 `root@10.12.54.140:63008` 的 CentOS 7.8 和 `root@10.12.54.1:63008` 的 RHEL 6.4 上验证。后者使用 glibc 2.12 与 Linux 2.6.32，发布 Agent 仍能启动、采集并持续成功上报；产物不依赖目标机 glibc。
