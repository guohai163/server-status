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
3. 将节点信息、库存变化、分钟主快照、文件系统指标和网络指标放在同一个 PostgreSQL 事务中。
4. 使用 `(bucket_at, node_id)` 作为分钟幂等键，同一分钟重试只会覆盖，不会生成重复数据。
5. 只有库存指纹变化时才更新硬件清单，并用 `first_seen_at`、`last_seen_at`、`removed_at` 保留设备变更历史。
6. 数据库触发器维护 current 表，乱序的旧数据不能覆盖较新的当前状态。
7. 后台任务维护分区，并重新汇总过去 25 个小时，以覆盖允许的延迟上报。

### 数据流

```mermaid
flowchart TD
    A["Agent 启动或进入下一分钟"] --> B["采集 CPU、内存、磁盘、文件系统、网卡和 IP"]
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
    L --> M["替换该分钟的文件系统和网卡明细"]
    M --> N["触发器更新 current 表"]
    N --> O["提交事务并返回 202 Accepted"]
    O --> P["公开状态 API / Web 仪表盘查询"]
    Q["每小时后台任务"] --> R["小时汇总、预建分区、清理过期分区"]
    L --> Q
```

## 数据保留与状态

- 分钟原始数据：按 UTC 天分区，保留 90 天。
- 小时汇总：按 UTC 月分区，保留 24 个月。
- 当前状态：每个节点、文件系统或网卡只保留一条最新记录，列表查询不扫描历史表。
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
make build-agent-release VERSION=0.3.0
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

例如发布 `v0.3.0` 后：

```bash
docker pull ghcr.io/guohai163/server-status-central:0.3.0
```

工作流使用 GitHub 自动提供的 `GITHUB_TOKEN`，仓库无需配置 GHCR 用户名或密码。Token 权限被限制为 Release 所需的 `contents: write` 以及镜像任务所需的 `packages/attestations/id-token`。

## 部署中心服务

### 1. 初始化数据库

按顺序执行 `db/migrations` 中的 SQL：

```bash
psql -v ON_ERROR_STOP=1 -f db/migrations/V001__monitoring_schema.sql
psql -v ON_ERROR_STOP=1 -f db/migrations/V002__safe_partition_retention.sql
psql -v ON_ERROR_STOP=1 -f db/migrations/V003__disk_io_metrics.sql
```

中央服务应使用继承 `server_status_writer` 的独立登录角色，不要使用 PostgreSQL 超级用户。

### 2. 准备中心配置

参考 `deploy/central.env.example`，在仓库外创建一个权限为 `0600` 的环境文件：

```dotenv
SERVER_STATUS_DATABASE_URL=postgres://server_status_app:password@postgres:5432/server_status_db?sslmode=disable
SERVER_STATUS_ADMIN_TOKEN=至少32位随机字符串
SERVER_STATUS_LISTEN_ADDR=:8080
```

Admin Token 只用于节点注册、令牌轮换和管理员查询。

### 3. 一键部署中心

```bash
SERVER_STATUS_CENTRAL_ENV_FILE=/secure/central.env ./scripts/deploy-central.sh
```

脚本默认部署到 `gydev@10.12.54.200:~/server-status-central`，在目标机执行 Docker 构建和 Compose 启动。也可以直接在中心机执行：

```bash
docker compose up -d --build
curl http://127.0.0.1:8080/readyz
```

中心容器使用非 root 用户、只读根文件系统、移除全部 capabilities，并配置 `unless-stopped` 自动恢复。

部署完成后直接访问 `http://中心节点地址:8080/`。Web 看板不需要登录：首屏以卡片显示所有节点的机器名、IP、CPU、内存、磁盘使用率和磁盘读写速率；点击卡片进入硬件、文件系统、网卡和历史趋势详情。节点上报和管理接口仍分别使用 Node Token 与 Admin Token。

## 一个脚本部署 Agent

执行端需要 Go 1.25、`make`、`python3`、`ssh` 和 `scp`，并能使用本机私钥免交互登录中心机和目标节点。目标节点需要 `curl` 与 `crontab`，中心机需要已经运行的中心容器及权限为 `0600` 的 `.env`。

中心服务部署完成后，在本仓库根目录执行：

```bash
./scripts/deploy-agent.sh
```

不需要预先创建 Agent ID、节点 Token 或 Agent 环境文件。脚本自动完成以下工作：

1. 交叉编译 Linux amd64 静态二进制。
2. 通过 SSH 读取节点主机名、系统版本、架构和已有 Agent ID。
3. 从中心机的 `0600` `.env` 读取 Admin Token，并在中心机本地调用注册 API；Admin Token 不会发送到节点。
4. 首次部署创建节点；重复部署复用 Agent ID 并自动轮换节点 Token。
5. 上传 `.new` 文件并原子替换运行中的二进制，避免 `Text file busy`。
6. 写入权限为 `0600` 的 Agent 配置。
7. 安装 `@reboot` 启动项和每 5 分钟存活检查，随后立即启动 Agent。
8. 最多等待 20 秒，必须观察到中心接受第一条报告才返回成功。

默认参数：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `SERVER_STATUS_AGENT_TARGET` | `gydev@10.12.54.169` | Agent SSH 目标 |
| `SERVER_STATUS_CENTRAL_TARGET` | `gydev@10.12.54.200` | 中心 SSH 目标 |
| `SERVER_STATUS_URL` | `http://10.12.54.200:8080` | Agent 上报地址 |
| `SERVER_STATUS_CENTRAL_DIR` | `server-status-central` | 中心机部署目录 |
| `SERVER_STATUS_AGENT_ENVIRONMENT` | `production` | 写入节点 labels 的环境名 |
| `SERVER_STATUS_AGENT_BINARY` | `dist/server-status-agent-linux-amd64` | 自定义 Agent 二进制 |
| `SERVER_STATUS_AGENT_VERSION` | 当前 Git tag | 覆盖构建和注册时使用的 Agent 版本 |

部署其他节点示例：

```bash
SERVER_STATUS_AGENT_TARGET=ops@10.12.54.170 ./scripts/deploy-agent.sh
```

高级用法仍可传入已经准备好的节点配置，此时脚本跳过自动注册：

```bash
SERVER_STATUS_AGENT_ENV_FILE=/secure/agent.env ./scripts/deploy-agent.sh
```

Agent 配置字段参考 `deploy/agent.env.example`。如果目标机具备 systemd user linger 或 root 部署权限，也可以使用 `deploy/server-status-agent.service`；当前默认脚本不要求 sudo。

## API

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| `GET` | `/healthz` | 无 | 检查中心进程存活 |
| `GET` | `/readyz` | 无 | 检查数据库和迁移就绪 |
| `GET` | `/` | 无 | Web 节点状态看板 |
| `GET` | `/api/v1/nodes` | 无 | 查询所有节点最新卡片数据 |
| `GET` | `/api/v1/nodes/{node_id}` | 无 | 查询节点完整硬件与运行状态 |
| `GET` | `/api/v1/nodes/{node_id}/history?range=24h` | 无 | 查询 `1h/6h/24h/7d/30d/90d` 历史趋势 |
| `POST` | `/api/v1/reports` | Node Token | 接收一分钟快照 |
| `POST` | `/api/v1/admin/nodes` | Admin Token | 注册节点或轮换节点 Token |
| `GET` | `/api/v1/admin/nodes` | Admin Token | 查询节点状态列表 |
| `GET` | `/api/v1/admin/nodes/{node_id}` | Admin Token | 查询节点、文件系统和网卡详情 |

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
