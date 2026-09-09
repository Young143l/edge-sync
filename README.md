# edge-sync

边缘同步备份服务，运行于树莓派 2B（ARMv7 / 1GB RAM）。

- 监听远端源（Git / WebDAV / 插件可扩展），轮询检测变更后单向拉取到本地
- 硬链接快照保留历史版本（keepLast 数量限制 + keepDays）
- 存储源以外部进程插件接入（stdin/stdout 行分隔 JSON-RPC）
- 局域网内经 Web 面板 / CLI / 文件系统三种方式取回备份

## 仓库结构

| 目录 | 内容 |
|---|---|
| `cmd/edge-syncd` | 内核入口（调度 / diff / 原子应用 / IPC） |
| `cmd/edge-panel` | Web 面板 server（Go，纯静态二进制） |
| `cmd/edge-sync` | CLI（Go） |
| `internal/` | 内核内部包（config/engine/ipc/plugin/runner/state/logring） |
| `plugins/` | 插件：plugin-git（SSH/HTTPS）、plugin-local（本地目录源） |
| `archive/plugin-webdav/` | WebDAV 插件（已实现，暂不打包部署） |
| `pkg/` | 共享包：protocol（协议与 IPC 类型单一真源）、pluginkit |
| `web/` | 面板前端（Vite + React + MUI，teal，明暗） |
| `scripts/deploy.sh` | 部署助手（双目标：rpi2 / qwifi；rsync + systemd） |
| `docs/` | 设计方案 / 验收报告 / 执行清单 |

## 文档

- 设计方案：`docs/DESIGN.md`
- 分阶段执行清单：`docs/TODO.md`

## Git 私有仓库（SSH deploy key）

拉取 GitHub 私有仓库（私有镜像）推荐 SSH + deploy key：

```bash
# 1. 生成无口令密钥（BatchMode 下无法交互输入 passphrase）
ssh-keygen -t ed25519 -f /opt/edge-sync/etc/keys/github_myrepo -N ""

# 2. 公钥（.pub）添加到 GitHub 仓库：Settings → Deploy keys → Add（只读勾选）

# 3. known_hosts 首连自动接受（StrictHostKeyChecking=accept-new）
```

任务配置示例（多仓库多密钥：每个任务独立 `keyFile`，进程天然隔离）：

```yaml
tasks:
  - name: private-mirror
    plugin: git
    interval: 5m
    options:
      url: git@github.com:me/private-repo.git   # SSH 形式
      branch: main
      cacheDir: /opt/edge-sync/var/cache/private-mirror
      keyFile: /opt/edge-sync/etc/keys/github_myrepo
    retention:
      keepLast: 10
```

注意事项：

- 密钥必须**无口令**（同步为无人值守流程，无法交互输入 passphrase）；有口令密钥可用 `ssh-agent`
- 自定义 SSH 端口使用 `ssh://git@host:<port>/<user>/<repo>.git` 形式
- HTTPS + token 路径：`token` 配置项（建议 `${ENV}` 展开注入），`username` 默认 `x-access-token`
- 密钥文件不存在时同步立即失败并给出明确报错

## 面板访问令牌

默认自动生成（`etc/panel.json` 的 `token` 字段）。自定义：改 token 后重启面板：

```bash
sudo systemctl restart edge-panel
```

浏览器打开 `http://<设备IP>`（端口 80），输入 token 即可；下载由 HttpOnly Cookie 授权，登录一次后 30 天免密。

## 部署手册

### 快速部署

```bash
./scripts/deploy.sh --target rpi2 --host user@<设备IP> [--token <令牌>] [--port 80]
# 随身 WiFi（512MB，面板按需）:
./scripts/deploy.sh --target qwifi --host user@<设备IP>
```

- 构建：5 个 Go 静态二进制（rpi2=armv7 / qwifi=arm64 交叉编译）+ 面板前端
- 产物：`/opt/edge-sync/{bin,panel,etc,docs}` + systemd 双 unit（Restart=always、MemoryMax、以部署用户运行）
- 部署后冒烟：服务 active + 面板 HTTP 探活 + GitHub 通路体检（不可达时给出 443 绕行指引）
- 添加任务：`/opt/edge-sync/bin/edge-sync -c /opt/edge-sync/etc/config.yaml add`（向导）

### GitHub 私有仓库（SSH）

设备密钥（全局 SSH key 或 deploy key）认证后即可。**常见网络问题：22 端口被运营商封锁**，
改走 443（`ssh -T git@github.com` 超时时执行）：

```
# 设备 ~/.ssh/config
Host github.com
  HostName ssh.github.com
  Port 443
  User git
```

慢网络下首次 clone 可能超过默认 60s 快照超时，任务显式放宽：

```yaml
  - name: young143blog
    plugin: git
    interval: 5m
    snapshotTimeout: 5m    # 慢网络建议
    fetchTimeout: 15m
    options:
      url: git@github.com:Young143l/Young143Blog.git
      cacheDir: /opt/edge-sync/var/cache/young143blog
```

### 自定义令牌 / 端口

编辑设备 `etc/panel.json`（token / port）后 `sudo systemctl restart edge-panel`。

### 故障排查

| 现象 | 原因 | 对策 |
|---|---|---|
| 面板提示 IPC 连不上 | 内核未运行 / socket 路径不一致 | `systemctl status edge-syncd`；确认 config.yaml 的 `server.socket` 与 panel.json 的 `socket` 一致（建议都写绝对路径） |
| 面板 401 | 令牌不符 | 用 panel.json 的 token；改后重启 edge-panel |
| 下载 401 | 未登录（Cookie 失效） | 面板重新输入令牌（登录种 30 天 Cookie） |
| git 任务超时 | 慢网络 / 22 被封 | 443 绕行（见上）+ 任务 `snapshotTimeout` 放宽 |
| `remove --purge` 警告残留 | 历史 root 属主文件 | `sudo rm -rf /opt/edge-sync/data/<task>` |

### 开机自启与重启恢复

systemd 已 enable：设备重启后内核与面板自动拉起；`current` symlink 与状态文件均为
原子切换，任意时点断电/重启后镜像完整，中断的 staging 会被下次同步自动清理。
