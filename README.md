# edge-sync

边缘同步备份服务，运行于树莓派 2B（ARMv7 / 1GB RAM）。

- 监听远端源（Git / WebDAV / 插件可扩展），轮询检测变更后单向拉取到本地
- 硬链接快照保留历史版本（keepLast 数量限制 + keepDays）
- 存储源以外部进程插件接入（stdin/stdout 行分隔 JSON-RPC）
- 局域网内经 Web 面板 / CLI / 文件系统三种方式取回备份

## 仓库结构

| 目录 | 内容 |
|---|---|
| `kernel/` | Go 内核 edge-syncd（调度 / diff / 原子应用 / IPC） |
| `plugins/` | 官方插件（plugin-git / plugin-webdav），Go 独立二进制 |
| `protocol/` | 插件协议 JSON Schema + TS 类型（双侧对齐的单一真源） |
| `cli/` | TS CLI（配置编辑 / 手动触发 / 导出恢复） |
| `panel/` | Web 面板（server: Node+Hono；web: Vite+React+MUI） |
| `scripts/` | 部署助手（交叉编译 / rsync / systemd） |
| `docs/` | 设计方案与执行清单 |

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
