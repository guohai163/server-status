# Server Status

Server Status 是一个面向 Linux 服务器的轻量监控系统，由节点 Agent、中心 API 和 PostgreSQL 三部分组成。

- `server-status-agent`：运行在 Ubuntu/CentOS，默认每分钟采集一次服务器状态。
- `server-status-server`：以 Docker 容器运行，负责鉴权、校验、事务入库、查询、定时汇总和 Web 数据展示。
- PostgreSQL：保存节点身份、硬件变更历史、分钟原始指标、最新状态和小时汇总。

当前默认部署目标：

| 组件 | 地址 | 运行方式 |
| --- | --- | --- |
| Agent | `gydev@10.12.54.169` | 静态 Linux 二进制、用户 crontab 守护 |
| 中心服务 | `gydev@10.12.54.200:8080` | Docker Compose |
| PostgreSQL | 独立 PG 实例 | `monitoring` schema |

## 采集内容

| 类别 | 硬件/清单信息 | 每分钟运行指标 |
| --- | --- | --- |
| CPU | 厂商、型号、封装数、物理核心、逻辑线程 | 总使用率、1/5/15 分钟 load |
| 内存 | 插槽、厂商、型号、序列号、类型、容量、速率 | 总量、已用、可用、缓存、buffer、swap |
| 磁盘 | 设备名、厂商、型号、序列号、WWN、介质类型、容量 | 挂载点容量和 inode、整机磁盘读写字节与 IOPS 增量 |
| 网络 | 网卡、MAC、MTU、链路速率、IPv4/IPv6 | 链路状态、收发累计量、区间流量、包、错误和丢包 |
| GPU | NVIDIA 型号、UUID、显存容量 | 每张 GPU 的核心使用率、显存使用率和历史趋势 |

磁盘硬件和文件系统使用率分别建模。这样可以正确处理 LVM、RAID、多挂载点和一个文件系统跨多个块设备的情况。

## 工作原理

### Agent

1. 启动后立即采集一次，以后保持约 60 秒的采集起点间隔。
2. 从 `/proc`、`/sys` 和系统 API 读取 CPU、内存、块设备、文件系统和网卡状态。
3. 对硬件清单排序并计算 SHA-256 指纹。
4. 计算网卡和磁盘累计计数与上一次采集值的差值；计数器重置时本次增量记为 0。
5. 使用节点专属 Bearer Token 将完整 JSON 快照发送到中心 `/api/v1/reports`。
6. 网络错误、HTTP 429 或 5xx 会在本采集周期内短退避重试最多 3 次；认证和数据校验类 4xx 不重试。持续失败也不会退出进程，下一个采集周期继续尝试。

普通用户无法读取 DMI 或虚拟机没有公开设备型号时，Agent 会明确使用 `System Memory (DMI unavailable)`、`Virtio Block Device (vda)` 等回退名称，同时继续上报可验证的数量、容量和使用率，不伪造厂商型号。

### 中心服务

1. 对 Bearer Token 做 SHA-256 后查询数据库，原始 Token 不入库。
2. 校验节点身份、JSON 字段、库存指纹、资源引用、时间窗口、百分比和 `bigint` 范围。
3. 将节点信息、库存变化、分钟主快照、文件系统、网络和 GPU 指标放在同一个 PostgreSQL 事务中。
4. 使用 `(bucket_at, node_id)` 作为分钟幂等键，同一分钟重试只会覆盖，不会生成重复数据。
5. 只有库存指纹变化时才更新硬件清单，并用 `first_seen_at`、`last_seen_at`、`removed_at` 保留设备变更历史。
6. 数据库触发器维护 current 表，乱序的旧数据不能覆盖较新的当前状态。
7. 后台任务维护分区，并重新汇总过去 25 个小时，以覆盖允许的延迟上报。

### 数据流

```mermaid
flowchart TD
    A["Agent 启动或进入下一分钟"] --> B["采集 CPU、内存、磁盘、文件系统、网卡、GPU 和 IP"]
    B --> C["排序硬件清单并计算 SHA-256 指纹"]
    C --> D["计算网络与磁盘区间增量并组装 JSON 快照"]
    D --> E["POST /api/v1/reports<br/>Bearer Node Token"]
    E --> F{"Token、时间和数据校验通过?"}
    F -- "否" --> G["返回 4xx/5xx<br/>Agent 下周期重试"]
    F -- "是" --> H["开启 PostgreSQL 事务"]
    H --> I{"库存指纹变化?"}
    I -- "是" --> J["更新硬件清单和变更历史"]
    I -- "否" --> K["跳过清单写入"]
    J --> L["Upsert 节点分钟主快照"]
    K --> L
    L --> M["替换该分钟的文件系统、网卡和 GPU 明细"]
    M --> N["触发器更新 current 表"]
    N --> O["提交事务并返回 202 Accepted"]
    O --> P["公开状态 API / Web 仪表盘查询"]
    Q["每小时后台任务"] --> R["小时汇总、预建分区、清理过期分区"]
    L --> Q
```

## 数据保留与状态

- 分钟原始数据：按 UTC 天分区，保留 90 天。
- 小时汇总：按 UTC 月分区，保留 24 个月。
- 当前状态：每个节点、文件系统、网卡或 GPU 只保留一条最新记录，列表查询不扫描历史表。
- 连续 3 分钟没有成功上报的节点显示为 `offline`。
- 中心接受最多 24 小时的延迟数据，拒绝超过当前时间 5 分钟的未来数据。

详细表结构见 [docs/database-design.md](docs/database-design.md)。

## 构建与测试

开发机需要 Go 1.25：

```bash
go test ./...
go test -race ./...
go vet ./...
make build
make build-agent-linux
make build-agent-release VERSION=0.3.2
```

产物：

- `bin/server-status-server`：当前开发平台的中心程序。
- `bin/server-status-agent`：当前开发平台的 Agent。
- `dist/server-status-agent-linux-amd64`：Ubuntu/CentOS x86_64 静态 Agent。
- `dist/release/server-status-agent-linux-{amd64,arm64}`：带版本信息的发布二进制。
- `dist/release/checksums.txt`：发布二进制的 SHA-256 校验值。

Agent 版本由构建时注入，可以直接检查：

```bash
server-status-agent --version
```

## Tag 自动发布

推送符合 `v*.*.*` 的 tag 后，[Release 工作流](.github/workflows/release.yml) 自动完成：

1. 运行 `go test ./...` 和 `go vet ./...`。
2. 构建 `linux/amd64` 与 `linux/arm64` 的完全静态 Agent，并附加到同名 GitHub Release。
3. 生成 `checksums.txt`，用于节点安装前校验文件完整性。
4. 构建 `linux/amd64`、`linux/arm64` 中心镜像，发布到 `ghcr.io/guohai163/server-status-central`。
5. 为镜像生成 `版本号`、`主版本.次版本` 和 `latest` 标签，并发布 SBOM 与构建来源证明。
6. 发布后重新读取 Release 资产和 GHCR manifest，缺少任一二进制或目标架构会使工作流失败。

例如发布 `v0.3.2` 后：

```bash
docker pull ghcr.io/guohai163/server-status-central:0.3.2
```

工作流使用 GitHub 自动提供的 `GITHUB_TOKEN`，仓库无需配置 GHCR 用户名或密码。Token 权限被限制为 Release 所需的 `contents: write` 以及镜像任务所需的 `packages/attestations/id-token`。

## 部署中心服务

### 1. 初始化数据库

按顺序执行 `db/migrations` 中的 SQL：

```bash
psql -v ON_ERROR_STOP=1 -f db/migrations/V001__monitoring_schema.sql
psql -v ON_ERROR_STOP=1 -f db/migrations/V002__safe_partition_retention.sql
psql -v ON_ERROR_STOP=1 -f db/migrations/V003__disk_io_metrics.sql
psql -v ON_ERROR_STOP=1 -f db/migrations/V004__primary_network_interface.sql
psql -v ON_ERROR_STOP=1 -f db/migrations/V005__nvidia_gpu_metrics.sql
psql -v ON_ERROR_STOP=1 -f db/migrations/V006__node_tags.sql
psql -v ON_ERROR_STOP=1 -f db/migrations/V007__gpu_history_metrics.sql
```

中央服务应使用继承 `server_status_writer` 的独立登录角色，不要使用 PostgreSQL 超级用户。

### 2. 准备中心配置

参考 `deploy/central.env.example`，在仓库外创建一个权限为 `0600` 的环境文件：

```dotenv
SERVER_STATUS_DATABASE_URL=postgres://server_status_app:password@postgres:5432/server_status_db?sslmode=disable
SERVER_STATUS_ADMIN_TOKEN=至少32位随机字符串
SERVER_STATUS_LISTEN_ADDR=:8080
SERVER_STATUS_CENTRAL_IMAGE=ghcr.io/guohai163/server-status-central:0.8.0
```

Admin Token 只用于节点注册、令牌轮换和管理员查询。

### 3. 一键部署中心

```bash
SERVER_STATUS_CENTRAL_ENV_FILE=/secure/central.env ./scripts/deploy-central.sh
```

脚本默认部署到 `gydev@10.12.54.200:~/server-status-central`，只上传 Compose 与环境文件，然后拉取固定版本的 GHCR 镜像。也可以直接在中心机执行：

```bash
docker compose pull
docker compose up -d --remove-orphans
curl http://127.0.0.1:8080/readyz
```

中心容器使用非 root 用户、只读根文件系统、移除全部 capabilities，并配置 `unless-stopped` 自动恢复。Compose 会挂载独立的 `agent_release_cache` 持久卷，用于缓存 Agent Release 资产。

部署完成后直接访问 `http://中心节点地址:8080/`。Web 看板不需要登录：首屏以卡片显示所有节点的机器名、IP、Tag、CPU、内存、磁盘使用率和磁盘读写速率，并可在页面顶部按 Tag 筛选；点击卡片进入硬件、文件系统、网卡和历史趋势详情，GPU 节点会按每张卡分别显示核心和显存使用率趋势。每个节点最多设置 5 个 Tag。节点上报和管理接口仍分别使用 Node Token 与 Admin Token。浏览器在 Admin Token 成功通过鉴权后会将其保存到本地 30 天，期间添加节点、编辑 Tag 和选择首页 IP 无需重复填写；过期或接口返回 401 时自动清除。

## 一个脚本部署 Agent

目标节点不需要 Go、make、Python、Git 仓库或 SSH 服务，只需 Linux、`sudo`、`curl`、`crontab` 和 `sha256sum`。当前发布支持 `amd64/x86_64` 与 `arm64/aarch64`。

1. 打开中心看板，点击右上角“添加节点”。
2. 输入 Admin Token、节点显示名称和环境名。Agent 版本留空时使用最新 Release，也可以固定为 `0.3.2` 或 `v0.3.2`。
3. 复制生成的命令，在目标节点执行。

命令形态如下，实际内容包含中心生成的节点专属 Agent ID 和 Node Token：

```bash
curl -fsSL 'https://中心节点/install-agent.sh' | sudo env \
  SERVER_STATUS_URL='https://中心节点' \
  SERVER_STATUS_AGENT_ID='节点 Agent ID' \
  SERVER_STATUS_TOKEN='节点 Token' \
  SERVER_STATUS_AGENT_ENVIRONMENT='production' \
  sh
```

安装器从中心节点的 Release 缓存接口下载匹配架构的静态二进制和 `checksums.txt`，目标节点不需要访问 GitHub。中心首次请求某个资产时从 `guohai163/server-status` 的 GitHub Release 下载并校验 SHA-256，后续直接使用持久缓存；`latest` 缓存每 10 分钟允许刷新，固定版本长期复用。Agent 仍会在安装前再次校验 SHA-256，然后原子安装。默认路径：

| 内容 | 路径 |
| --- | --- |
| 二进制 | `/opt/server-agent/server-status-agent` |
| 启动脚本 | `/opt/server-agent/run-agent.sh` |
| Agent 配置 | `/opt/server-agent/agent.env`，权限 `0600` |
| 日志 | `/var/log/server-status-agent.log` |

安装器会保留现有无关 crontab，幂等写入 `@reboot` 启动项和每 5 分钟存活检查，立即启动 Agent，并等待中心接受第一条报告。Node Token 会出现在生成命令和目标机 shell 历史中，但仅具备该节点的上报权限；生产环境应使用 HTTPS。

正式发布的中心镜像内嵌同版本 Agent 的最新版本号。支持自更新的 Agent 上报时，如果自身语义版本低于中心版本，会从中心下载该固定版本，校验 SHA-256 和二进制版本后原子替换自身并重启。自动更新只替换二进制，不修改 `agent.env`，因此原 Agent ID、Node Token 和标签保持不变，也不会自动降级。旧 Agent 需要先手动升级一次到支持自更新的版本。

原有 `scripts/deploy-agent.sh` 继续作为兼容的远程部署方式。只有该旧流程需要开发机安装 Go 1.25、`make`、`python3`、`ssh` 和 `scp`，并配置到中心机和 Agent 节点的免交互 SSH。

## API

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| `GET` | `/healthz` | 无 | 检查中心进程存活 |
| `GET` | `/readyz` | 无 | 检查数据库和迁移就绪 |
| `GET` | `/` | 无 | Web 节点状态看板 |
| `GET` | `/install-agent.sh` | 无 | 获取无凭据的 Agent 安装器 |
| `GET` | `/agent/releases/{version}/{asset}` | 无 | 获取中心校验并缓存的 Agent Release 资产 |
| `GET` | `/api/v1/nodes` | 无 | 查询所有节点最新卡片数据 |
| `GET` | `/api/v1/nodes/{node_id}` | 无 | 查询节点完整硬件与运行状态 |
| `GET` | `/api/v1/nodes/{node_id}/history?range=24h` | 无 | 查询 `1h/6h/24h/7d/30d/90d` 历史趋势 |
| `POST` | `/api/v1/reports` | Node Token | 接收一分钟快照，并在需要时返回固定版本 Agent 更新指令 |
| `POST` | `/api/v1/admin/nodes` | Admin Token | 注册节点或轮换节点 Token |
| `GET` | `/api/v1/admin/nodes` | Admin Token | 查询节点状态列表 |
| `GET` | `/api/v1/admin/nodes/{node_id}` | Admin Token | 查询节点、文件系统和网卡详情 |
| `PUT` | `/api/v1/admin/nodes/{node_id}/primary-network-interface` | Admin Token | 设置首页卡片 IP 使用的网络接口 |

所有受保护接口使用：

```http
Authorization: Bearer <token>
```

## 运行检查

中心服务：

```bash
ssh gydev@10.12.54.200 'docker ps --filter name=server-status-central'
curl http://10.12.54.200:8080/readyz
```

Agent：

```bash
ssh gydev@10.12.54.169 'pgrep -af server-status-agent'
ssh gydev@10.12.54.169 'tail -f ~/.local/state/server-status-agent/agent.log'
```

实际部署路径、权限和当前机器限制见 [docs/deployment.md](docs/deployment.md)。仓库不会保存数据库密码、Admin Token 或 Node Token。
