# Homepage 自定义 API 配置

Server Status 提供一个面向 [Homepage Custom API](https://gethomepage.dev/widgets/services/customapi/) 组件的公开聚合接口，用于展示正常、离线和异常告警服务器数量。

## 接口

```http
GET /api/v1/homepage
```

该接口与公开看板采用相同的访问策略，不需要 Admin Token 或 Node Token。成功响应示例：

```json
{
  "normal": 8,
  "offline": 2,
  "alerts": 1
}
```

字段含义：

| 字段 | 含义 |
| --- | --- |
| `normal` | 已完成首次上报、最近 3 分钟内在线，并且当前没有异常的服务器数 |
| `offline` | 已完成首次上报，但超过 3 分钟没有上报的服务器数 |
| `alerts` | 当前在线并且至少存在一项异常的服务器数 |

待安装和已禁用节点不进入统计。三类服务器互斥，同一台服务器即使同时存在多项异常也只计入一次 `alerts`。

`alerts` 与 Server Status 告警页面采用相同口径，统计存在当前未恢复的负载、文件系统容量、SMART、RAID 或硬件温度告警的在线服务器。离线告警不重复计入 `alerts`，而是单独计入 `offline`。

## Homepage 配置

编辑 Homepage 的 `services.yaml`，加入下面的服务。将示例域名替换为 Server Status 中心服务地址：

```yaml
- 基础设施:
    - Server Status:
        href: https://status.example.com/
        description: 服务器监控
        widget:
          type: customapi
          url: https://status.example.com/api/v1/homepage
          refreshInterval: 60000
          mappings:
            - field: normal
              label: 正常
              format: number
            - field: offline
              label: 离线
              format: number
            - field: alerts
              label: 告警
              format: number
```

`refreshInterval` 的单位是毫秒。Server Status Agent 每分钟上报一次，因此建议设置为 `60000`。

## Docker 网络

Homepage 在服务端请求 `widget.url`，该地址必须能从 Homepage 容器内部访问。Homepage 与 Server Status 位于同一个 Docker 网络时，可以使用中心服务的容器名，例如：

```yaml
widget:
  type: customapi
  url: http://server-status-central:8080/api/v1/homepage
  refreshInterval: 60000
  mappings:
    - field: normal
      label: 正常
      format: number
    - field: offline
      label: 离线
      format: number
    - field: alerts
      label: 告警
      format: number
```

如果两个容器不共享网络，请使用 Homepage 容器可解析、可访问的反向代理域名或宿主机地址。`href` 是浏览器点击卡片时打开的地址，可以与容器内部使用的 `widget.url` 不同。

## 验证

先从 Homepage 所在主机或容器验证接口：

```bash
curl -fsS https://status.example.com/api/v1/homepage
```

返回包含 `normal`、`offline` 和 `alerts` 的 JSON 后，重新加载 Homepage 配置即可。
