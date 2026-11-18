# 使用 Docker Compose 公网部署

公网部署由 Caddy 终止 HTTPS，中心服务通过 Compose 网络连接 Caddy，并使用独立的 PostgreSQL 实例。只有 Caddy 发布宿主机端口。Caddy 会为配置的域名申请和续期证书，并将 HTTP 请求重定向到 HTTPS。 [Caddy 自动 HTTPS 文档](https://caddyserver.com/docs/automatic-https)

## 1. 准备宿主机和数据库

- 在 Linux 宿主机上安装 Docker Engine 和 Compose 插件，并准备此仓库的源码。
- 将域名的 DNS 记录指向公网宿主机。只在有可用公网 IPv6 时配置 AAAA 记录。开放 TCP 80 和 443，以便完成证书签发并提供 HTTPS。
- 准备中心容器可通过私网访问的 PostgreSQL 数据库。数据库防火墙只需允许来自中心服务的连接。

新建数据库后，从仓库根目录用数据库管理员账号按文件名顺序执行迁移。先按数据库环境配置 psql 的 TLS 证书信任，以下示例使用主机名验证。

```bash
(
  set --
  for migration in db/migrations/V*.sql; do
    set -- "$@" -f "$migration"
  done
  PGSSLMODE=verify-full psql -X -h db.internal -U db_admin -d server_status_db -v ON_ERROR_STOP=1 "$@"
)
```

已有数据库按顺序应用尚未执行的迁移。升级前备份数据库，V016 会清理 7 天窗口之外的原始指标分区。启动时中心会检查 V018 是否已安装。

迁移完成后，在同一个数据库的管理员 psql 会话中创建运行账号并设定独立密码。已有运行账号时沿用其权限和配置。

```sql
CREATE ROLE server_status_app LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE;
GRANT server_status_writer TO server_status_app;
GRANT CONNECT ON DATABASE server_status_db TO server_status_app;
\password server_status_app
```

## 2. 配置 .env

从仓库根目录创建 `.env` 并限制读取权限。已有 `.env` 时直接编辑。

```bash
test -e .env || install -m 600 .env.example .env
chmod 600 .env
```

| 变量 | 填写方式 |
| --- | --- |
| `SERVER_STATUS_DOMAIN` | 公网域名，例如 `status.example.com` |
| `SERVER_STATUS_DATABASE_URL` | PostgreSQL 连接 URI，使用上一步的运行账号。对 URI 密码中的保留字符做百分号编码 |
| `SERVER_STATUS_WEB_ACCESS_KEY` | 运行 `openssl rand -hex 32` 生成看板密钥 |
| `SERVER_STATUS_ADMIN_TOKEN` | 再运行一次 `openssl rand -hex 32` 生成管理令牌 |

数据库连接使用 `sslmode=verify-full` 并填写证书匹配的主机名。数据库使用私有 CA 时，可给中心容器只读挂载 CA 文件，并在 URI 中填写容器内路径 `sslrootcert=/path/to/db-ca.crt`。

保管好 `.env`，并使用 `config -q` 检查配置，避免在终端输出展开后的密钥。 [Docker Compose 变量插值文档](https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/)

## 3. 构建和启动

在仓库根目录执行，显式选择公网部署文件。

```bash
docker compose -f docker-compose.yaml config -q
docker compose -f docker-compose.yaml up -d --build
docker compose -f docker-compose.yaml ps
docker compose -f docker-compose.yaml logs --tail=100 central caddy
```

Compose 从当前源码构建包含鉴权的中心镜像。该构建的版本为 `dev`，首次安装 Agent 所需的公开 Release 资产由中心下载并持久缓存。需要自动更新 Agent 时，使用包含该功能的正式发布镜像。

## 4. 验证访问

在浏览器访问 `https://status.example.com/`。认证弹窗中的用户名填写 `viewer`，密码填写 `.env` 中的 `SERVER_STATUS_WEB_ACCESS_KEY`。告警页面、静态资源和看板读取接口也会校验该密钥。使用浏览器无痕窗口可以重新验证认证提示。

```bash
# 匿名页面和查询应返回 401。
curl -sS -o /dev/null -w '%{http_code}\n' https://status.example.com/
curl -sS -o /dev/null -w '%{http_code}\n' https://status.example.com/api/v1/nodes

# 按提示输入错误密钥应返回 401，输入正确密钥应返回 200。
curl -sS -o /dev/null -w '%{http_code}\n' --user viewer https://status.example.com/api/v1/nodes

# 容器内的就绪探针应返回 {"status":"ready"}。
docker compose -f docker-compose.yaml exec -T central wget -qO- http://127.0.0.1:8080/readyz
```

节点通过 HTTPS 上报，认证方式为节点专用 Bearer Token。管理接口使用 Admin Bearer Token。Agent 安装脚本和 Release 下载是公开端点，以便节点安装和更新。

## 5. 升级及轮换密钥

- 升级中心时更新源码，应用缺少的数据库迁移，再运行 `docker compose -f docker-compose.yaml up -d --build`。
- 轮换看板密钥时更新 `.env`，并运行下方命令重建中心容器。旧密钥随即失效。使用 Homepage 集成时同步更新其认证配置。
- 迁移旧中心服务时，把已有 Agent 配置的中心地址改为 `https://状态域名` 并重启 Agent。沿用同一数据库时可继续使用原 Node Token。

```bash
docker compose -f docker-compose.yaml up -d --no-deps --force-recreate central
```
