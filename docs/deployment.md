# 当前部署

## 中央服务

- SSH：`gydev@10.12.54.200`
- 目录：`/home/gydev/server-status-central`
- 容器：`server-status-central`
- HTTP：`http://10.12.54.200:8080`
- 配置：`/home/gydev/server-status-central/.env`，权限 `0600`
- 重启策略：Docker `unless-stopped`
- 容器安全：非 root 用户、只读根文件系统、移除全部 Linux capabilities

检查：

```bash
ssh gydev@10.12.54.200 'docker ps --filter name=server-status-central'
curl http://10.12.54.200:8080/readyz
```

浏览器访问 `http://10.12.54.200:8080/` 即可打开无需鉴权的节点看板。公开范围只包括状态展示接口；节点上报和节点注册仍需要各自的 Bearer Token。

数据库使用专用登录角色 `server_status_app`，该角色只继承 `server_status_writer`，没有超级用户、建库或建角色权限。

## 节点 Agent

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

## 更新

中心使用：

```bash
SERVER_STATUS_CENTRAL_ENV_FILE=/secure/central.env scripts/deploy-central.sh
```

节点默认只需执行一个脚本，脚本会自动注册、生成配置、轮换 Token、上传并验证首条上报：

```bash
./scripts/deploy-agent.sh
```

只有需要跳过自动注册时才传入已有配置：

```bash
SERVER_STATUS_AGENT_ENV_FILE=/secure/agent.env ./scripts/deploy-agent.sh
```

部署脚本不会在仓库保存数据库密码、管理员 Token 或节点 Token。Agent 更新采用上传临时文件后原子替换，避免覆盖运行中二进制时出现 `Text file busy`。
