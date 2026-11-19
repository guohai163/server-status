# Docker 两文件部署

在装有 Docker Compose 的服务器上新建一个目录，进入后下载两个文件。

```bash
curl -fL -o docker-compose.yml https://github.com/guohai163/server-status/releases/download/v0.29.0/docker-compose.yml
curl -fL -o .env https://github.com/guohai163/server-status/releases/download/v0.29.0/server-status.env.example
chmod 600 .env
```

打开 `.env`，按每项上方的注释填写四个密码或令牌。对每个值分别运行一次 `openssl rand -hex 32`。其他配置可以沿用默认值。

```bash
docker compose up -d
```

Compose 会拉取应用和 PostgreSQL 两个镜像。首次启动会自动建表，升级时只执行缺少的迁移。数据库数据保存在 `postgres_data` 卷中。

在部署机上打开 `http://127.0.0.1:8080/`，用户名填 `viewer`，密码填 `SERVER_STATUS_WEB_ACCESS_KEY`。公网访问时，将宿主机上的 HTTPS 反向代理指向 `127.0.0.1:8080`，浏览器和 Agent 使用 HTTPS 域名。PostgreSQL 只在 Compose 内部网络通信。

用 `docker compose ps` 查看两个容器的健康状态。更新看板密钥后，重新运行 `docker compose up -d`。PostgreSQL 管理密码在首次初始化时设置，使用已有数据卷时保留原值。
