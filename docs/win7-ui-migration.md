# win7-ui KVM 迁移

`scripts/migrate-win7-ui.sh` 用于将已关机的 `win7-ui` libvirt 虚拟机，从 `root@10.12.54.140:63008` 迁移到 `gydev@10.12.54.221:63008`。

请在一台已经能够以非交互方式 SSH 登录两台服务器的工作站上运行脚本。脚本通过工作站使用 `scp -3` 中转 qcow2 镜像，不会在两台服务器之间安装 SSH 密钥，也不会复制凭据。

先执行只读预检：

```sh
./scripts/migrate-win7-ui.sh
```

预检通过后，复制镜像、验证完整性并注册目标虚拟机：

```sh
./scripts/migrate-win7-ui.sh --execute
```

只有确认目标虚拟机可以使用原 MAC 地址，并使用 `br0` 上的 VNC 端口 `50010` 后，才应在同一次操作中启动目标虚拟机：

```sh
./scripts/migrate-win7-ui.sh --execute --start
```

脚本默认使用以下已探测到的配置：

| 项目 | 值 |
| --- | --- |
| 源镜像 | `/backup/kvm/win10-ui.qcow` |
| 目标目录 | `/data/kvm/win7-ui` |
| 网络桥接 | `br0` |
| 目标机型 | `pc-i440fx-jammy` |
| VNC 端口 | `50010` |

所有默认值都可以通过环境变量覆盖：`WIN7_UI_SOURCE_HOST`、`WIN7_UI_SOURCE_PORT`、`WIN7_UI_TARGET_HOST`、`WIN7_UI_TARGET_PORT`、`WIN7_UI_VM_NAME`、`WIN7_UI_SOURCE_IMAGE`、`WIN7_UI_TARGET_BASE`、`WIN7_UI_BRIDGE`、`WIN7_UI_VNC_PORT` 和 `WIN7_UI_TARGET_MACHINE`。

在以下任一条件不满足时，脚本会拒绝执行：源虚拟机未关机；目标域或目标目录已经存在；VNC 端口已被占用；目标端没有 `br0`；目标存储空间不足以容纳完整源镜像并额外预留 5 GiB；或目标端不支持所选机型。复制前脚本会检查源 qcow2 镜像，复制后会比较源端和目标端的 SHA-256，并在定义虚拟机前对目标镜像执行 `qemu-img check`。

源虚拟机和 `/backup/kvm/win10-ui.qcow` 会被刻意保持不变。在目标虚拟机成功启动并完成客户机验证前，请保留源端数据。若需要回滚，请先关闭并取消定义目标域，然后只删除目标端的 `/data/kvm/win7-ui`：

```sh
ssh -p 63008 gydev@10.12.54.221 \
  'virsh -c qemu:///system destroy win7-ui 2>/dev/null || true; virsh -c qemu:///system undefine win7-ui; rm -rf /data/kvm/win7-ui'
```

如果传输中断，脚本会有意保留不完整的目标目录。请先检查该目录，然后在重试前删除 `/data/kvm/win7-ui`；`scp -3` 本身不能续传部分完成的远程到远程复制。
