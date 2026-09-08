# edge-sync 设计方案

> 边缘同步备份服务 · 运行于树莓派 2B · Go 内核 + TypeScript 外层 + 外部进程插件
>
> 状态：定稿 v1（本文件为实施依据，变更须经确认后更新）

---

## 1. 概述

### 1.1 目标

在树莓派 2B 上常驻运行的边缘备份服务：

- 监听多个远端源（Git 仓库、WebDAV 等），轮询检测变更后**单向拉取**到本地
- 以**硬链接快照**方式保留历史版本，支持数量与时间双重保留策略
- 存储源适配以**外部进程插件**接入，第三方可用任意语言编写插件
- 备份可通过 Web 面板、CLI、文件系统三种方式在局域网内取回

### 1.2 非目标（明确不做）

- 双向同步（不处理冲突合并）
- 公网暴露 / HTTPS（仅局域网，简单 token 鉴权）
- 出站推送（不向云盘反向同步）
- 加密存储（硬链接快照与加密不兼容，明示取舍）
- 实时推送监听（webhook 通道留作后续迭代）

### 1.3 运行环境约束

| 项 | 值 | 影响 |
|---|---|---|
| 机型 | Raspberry Pi 2B | ARMv7，32 位用户态（`GOARM=7`） |
| 内存 | 1 GB | 全程流式 IO，插件进程按需 spawn 即退 |
| 存储 | SD 卡 | 硬链接去重、staging 校验后落盘，减少写入 |
| 网络 | 局域网 | 面板与取回仅绑局域网接口 |

### 1.4 组件与技术选型

| 组件 | 语言/框架 | 形态 |
|---|---|---|
| 内核 edge-syncd | Go 1.27 | 静态二进制（`GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0`） |
| 官方插件 plugin-git / plugin-webdav | Go | 独立静态二进制，同仓实现 |
| CLI edge-sync | TypeScript (tsx) | 经 Unix socket 调内核 IPC |
| 面板 server | TypeScript (Node + Hono) | 常驻，IPC 客户端 + REST + 静态托管 |
| 面板 web | TypeScript (Vite + React + MUI) | 静态 SPA，浏览器渲染，树莓派仅托管 |
| 部署助手 | TypeScript (tsx) | 交叉编译、rsync、systemd unit 生成 |

---

## 2. 总体架构

```
浏览器 (React+MUI SPA)
   │ HTTP :8080 (token)
   ▼
┌─────────────────────────┐  unix socket + JSON-RPC  ┌──────────────────────┐
│  edge-panel (Node 常驻)  │ ◄──────────────────────► │  edge-syncd (Go 常驻) │
│  REST API / 静态托管      │                          │  scheduler / engine  │
│  下载流 (fs 直读磁盘)     │                          │  state / retention   │
└─────────────────────────┘                          └──────────┬───────────┘
                                                                │ spawn 按需，同步完即退
                                                     stdin/stdout 行分隔 JSON-RPC
                                                     ┌──────────┼──────────────┐
                                                     ▼          ▼              ▼
                                                plugin-git  plugin-webdav  第三方插件
                                                     │          │
                                                Git 远端     WebDAV 远端
   U盘 / scp / rsync / Samba ──── 直接读文件系统 ────► data/<task>/{current, versions/}
```

### 2.1 进程模型与故障隔离

- `edge-syncd.service`：内核常驻，负责全部同步逻辑；崩溃由 systemd 自动重启
- `edge-panel.service`：面板常驻，只读内核状态 + 转发手动触发 + 提供下载流；**崩溃不影响同步**
- 插件进程：内核在每次任务同步时按需 spawn，同步完成即退出，不常驻

### 2.2 职责边界

| 层 | 负责 | 不负责 |
|---|---|---|
| 插件 | 产出远端 Manifest、把单个文件写到指定路径 | diff、调度、本地目录组织 |
| 内核 | 调度、diff、下载编排、原子应用、快照与保留、状态 | 理解各源协议细节 |
| 面板 server | REST、鉴权、下载流（直读磁盘，不过内核） | 任何写操作（同步只能经内核触发） |
| CLI | 配置编辑（YAML 唯一真源）、触发、导出 | — |

---

## 3. 插件协议规范（协议即合同）

### 3.1 传输与帧格式

- 内核 spawn 插件可执行文件，以 **stdin/stdout 行分隔 JSON-RPC 2.0** 通信（每行一个完整 JSON 消息，`\n` 结尾）
- stdout **只允许输出协议消息**；日志一律走 stderr（内核采集并记入任务日志）
- 请求：`{"jsonrpc":"2.0","id":<n>,"method":"<m>","params":{...}}`
- 响应：`{"jsonrpc":"2.0","id":<n>,"result":{...}}` 或 `{"jsonrpc":"2.0","id":<n>,"error":{"code":<n>,"message":"..."}}`
- 进程非零退出、stdout 写入非法 JSON、或响应超时，均判该次调用失败

### 3.2 方法

#### initialize（握手，必选）

```
→ params: {"protocolVersion": 1}
← result: {"name":"git","version":"0.1.0","configSchema":{...JSON Schema 片段, 可选}}
```

内核据此展示插件元信息；`configSchema` 供 CLI/面板做配置校验提示。

#### snapshot（取远端清单，必选）

```
→ params: {"config": { ...任务 options 原样透传... }}
← result: {
    "entries": [
      {"path":"docs/a.md","fingerprint":"e3b0c4...","size":1024,"mtime":"2025-01-01T00:00:00Z"},
      ...
    ],
    "manifestFingerprint":"<可选，源级整体指纹如 commit hash>"
  }
```

- `path` 约定：相对根路径，`/` 分隔，不以 `/` 开头；目录不出现，仅文件
- `fingerprint`：插件自行决定（git blob hash、ETag、mtime+size 拼接等），内核只做**字符串相等比较**，不解释内容
- `size`/`mtime` 可选，仅用于展示与大文件预警

#### fetchFile（下载单文件，必选）

```
→ params: {"config":{...}, "path":"docs/a.md", "destPath":"/opt/edge-sync/data/t1/staging/docs/a.md"}
← result: {"size":1024, "checksum":"<可选，建议 sha256>"}
```

- **插件将文件直接写入 `destPath`**（先写同目录临时文件再 rename），二进制内容不过协议管道
- 父目录由插件负责创建（`mkdir -p` 语义）
- 内核收到响应后校验 destPath 存在且 size 一致；提供 checksum 则一并校验

### 3.3 超时与生命周期

| 调用 | 默认超时 | 可配置 |
|---|---|---|
| initialize | 10s | 否 |
| snapshot | 60s | 任务级 `snapshotTimeout` |
| fetchFile | 300s | 任务级 `fetchTimeout`（大文件场景） |

- 每次同步：spawn → initialize → snapshot →（有变更时）逐个 fetchFile → 进程退出
- fetchFile 串行执行（并发 1），避免 2B IO 争抢；后续版本可配置小并发

### 3.4 错误语义

- 任一 fetchFile 失败 → 本次同步整体失败：staging 丢弃、本地镜像不动、状态记 `consecutiveFailures++`
- snapshot 失败同上；失败重试由调度退避控制（见 4.6）

### 3.5 官方插件实现要点

#### plugin-git

- 轮询：`git ls-remote <url> <branch>` 取分支 head hash（不 clone，秒级、无本地 IO）
- hash 未变 → 返回缓存的 Manifest（若有）；变了 → 对本地缓存仓库 `git fetch --depth 1 origin <branch>` + `git reset --hard FETCH_HEAD`，遍历 worktree 生成 Manifest（fingerprint = `git hash-object` 结果即 blob hash）
- fetchFile：直接从缓存 worktree 拷贝到 destPath
- 缓存仓库位置：`var/cache/<task>/repo`（非 data 目录，不参与快照）
- 凭证：HTTPS 用 token（`passwordEnv`）；SSH 用 `keyFile` + `GIT_SSH_COMMAND`；配置支持 `${ENV}` 展开，明文凭证不落盘

#### plugin-webdav

- snapshot：`PROPFIND Depth: infinity`（服务器不支持时逐层遍历）收集文件项
- fingerprint 策略（任务配置 `fingerprint`）：`etag`（默认，服务器支持时）| `mtime_size`（降级兼容）
- fetchFile：`GET`，大文件带 `Range` 断点续传；写 destPath
- 兼容性：忽略集合资源（`<collection/>`）、处理 URL 编码路径

---

## 4. 内核设计

### 4.1 模块划分

```
kernel/internal/
├── config/     YAML 加载、校验、${ENV} 展开、热重载
├── scheduler/  每任务 goroutine ticker、jitter、失败退避
├── engine/     differ（Manifest diff）+ applier（原子应用）
├── plugin/     插件进程客户端（spawn、JSON-RPC、超时）
├── state/      任务状态持久化（原子写 JSON）
├── ipc/        Unix socket JSON-RPC server
└── retentoin   版本保留清理（并入 engine）
```

### 4.2 Manifest diff（内核统一实现）

对 `lastManifest` 与新 Manifest 按 path 建索引，三类变更：

- `added`：新有旧无
- `modified`：两者都有且 fingerprint 不等
- `removed`：旧有新无

两次快照对比为 O(n)，数千文件量级在 2B 上毫秒级完成。源级 `manifestFingerprint` 相等时可直接跳过 diff（git 场景命中率高）。

### 4.3 原子应用流程（applier）

前置：本次变更文件已全部由插件写入 `staging/` 并通过校验。

1. 新建 `versions/<UTC时间戳>/`（时间戳格式 `20060102-150405`）
2. 遍历新 Manifest 组装新版本目录：
   - `added`/`modified` → 硬链接自 `staging/` 对应文件
   - 未变更 → 硬链接自 `current` 指向的上一版本目录
   - `removed` → 不出现在新目录（旧版本里的文件不受影响，硬链接引用计数保证）
3. `symlink` 新版本目录为临时名 → `rename` 原子替换 `current`（任意时刻 current 都指向完整一致镜像）
4. 清空 `staging/`
5. 原子写状态文件（temp + rename）
6. 触发保留策略清理（见 4.4）

首次同步：无上一版本，全部文件硬链接自 staging。

### 4.4 版本保留策略（retention）

**每个任务独立配置，数量限制为默认开启：**

```yaml
retention:
  keepLast: 10      # 保留最近 N 个版本（默认 10），核心字段
  keepDays: 90      # 可选；超过 N 天的版本删除
```

- 清理时机：每次成功同步应用完成后；`config.reload` 后补跑一次
- 规则：先按 `keepDays` 删除超期版本，再按 `keepLast` 只保留最新 N 个；`current` 指向的版本始终保留
- 硬链接特性保证：删除任一旧版本目录，不影响其他版本中的同内容文件（inode 引用计数）
- 空间特性：版本间未变更文件零额外占用；SD 卡总占用 ≈ 首次全量 + 历次变更增量

### 4.5 状态持久化

`var/state/<task>.json`（temp+rename 原子写）：

```json
{
  "lastManifest": [ {"path":"...","fingerprint":"...","size":0,"mtime":"..."} ],
  "manifestFingerprint": "...",
  "lastSyncAt": "...", "lastSuccessAt": "...",
  "consecutiveFailures": 0,
  "stats": { "totalSyncs": 0, "totalFiles": 0, "totalBytes": 0 }
}
```

崩溃恢复：状态文件 + `current` symlink 均为原子切换，任意断电点重启后镜像完整；中断的 staging 由下次同步开始时清空。

### 4.6 调度

- 每任务独立 goroutine + ticker，间隔任务级 `interval`（默认 `10m`，最小 `60s`）
- jitter：进程启动时各任务随机延迟 `0 ~ interval*10%` 错峰
- 失败退避：连续失败 n 次后实际间隔 = `interval * 2^n`，上限 1h；成功或手动触发后复位
- 手动触发（IPC `task.trigger`）：立即执行一次；正在执行则返回"进行中"

---

## 5. IPC API（内核 ⇄ CLI/面板）

Unix socket（`var/edge-syncd.sock`），行分隔 JSON-RPC 2.0，与插件协议同风格。

| 方法 | 参数 | 返回 |
|---|---|---|
| `task.list` | — | 任务摘要数组（名称、插件、间隔、启用、状态、上次同步） |
| `task.status` | `{name}` | 单任务详情（含 consecutiveFailures、stats） |
| `task.trigger` | `{name}` | 触发同步，返回执行结果或"进行中" |
| `history.list` | `{name}` | 版本数组（时间戳、文件数、字节数） |
| `history.files` | `{name, version, subPath}` | 版本内目录列表（供面板文件树） |
| `history.fileVersions` | `{name, path}` | 该文件发生变更的版本列表（读各版本 Manifest 元数据） |
| `config.reload` | — | 重载 config.yaml，返回校验结果 |
| `log.tail` | `{n, task?}` | 最近日志行 |
| `event.subscribe` | — | **预留**（第一版不实现，面板先用轮询） |

`history.files` / `history.fileVersions` 为只读元数据查询；文件内容下载流不走内核（面板 server 直读磁盘）。

---

## 6. CLI 设计

命令名 `edge-sync`（tsx 运行），经 Unix socket 调 IPC；配置文件为唯一真源，CLI 直接编辑 YAML 后调 `config.reload`。

```
edge-sync list                          # 任务列表与状态
edge-sync status [task]                 # 详细状态
edge-sync add                           # 交互式向导：插件选择（读 configSchema）→ options → interval → retention
edge-sync edit <task>                   # $EDITOR 打开该任务 YAML 片段，保存后校验+reload
edge-sync remove <task>                 # 停用并从配置移除（--purge 连本地数据删除，默认保留）
edge-sync sync <task> [--wait]          # 手动触发
edge-sync history <task>                # 版本列表
edge-sync export <task> [--version <ts>] --out <dir>
                                        # 整版本导出（默认 current；cp/硬链接复制实现）
edge-sync restore-file <task> <path> [--version <ts>] --out <file>
                                        # 恢复单个历史文件
```

---

## 7. Web 面板

### 7.1 server（panel/server，Node + Hono）

- 静态托管 `panel/web/dist`；REST API 前缀 `/api`
- 鉴权：`Authorization: Bearer <token>`；token 于面板配置文件，首装随机生成
- 仅绑定局域网接口（`0.0.0.0` 可配但默认提示内网使用；无 HTTPS，明示局域网边界）

| 路由 | 说明 |
|---|---|
| `GET /api/overview` | 全任务摘要（聚合 task.list） |
| `GET /api/tasks/:name` | 任务详情 |
| `POST /api/tasks/:name/sync` | 手动触发（转发 IPC） |
| `GET /api/tasks/:name/history` | 版本列表 |
| `GET /api/tasks/:name/files?version=&path=` | 目录列表（转发 IPC history.files） |
| `GET /api/tasks/:name/download?version=&path=` | **文件下载流**：fs.createReadStream → 响应；路径 resolve 后强制 within `data/<task>/versions/<version>/`，防目录穿越 |
| `GET /api/tasks/:name/archive?version=&store=1` | **整版本 zip**：archiver 流式打包边压边发；`store=1` 仅存储不压缩（2B 省 CPU）；>500MB 时响应头附建议改用 rsync 的提示字段 |
| `GET /api/tasks/:name/file-versions?path=` | 单文件历史版本 |
| `GET /api/logs` | 日志（转发 log.tail） |

下载并发限制 1~2，让位同步任务 IO。

### 7.2 web（panel/web，Vite + React + MUI）

- 技术栈：React 19 + MUI v7（Material Design 风格，`colorSchemes` 明暗跟随系统）+ TanStack Query（`refetchInterval` 5s）+ React Router
- 布局：App Bar（服务状态 Chip + 刷新）+ 左侧 Navigation Drawer（小屏收起）

五页：

1. **仪表盘**：任务卡片网格（MUI Card）。卡片含：任务名、插件类型、上次同步相对时间、文件数/体积、状态 Chip（正常 / 同步中[LinearProgress] / 失败[重试N次] / 暂停）
2. **任务详情**：状态头 + [立即同步]（Dialog 确认 → Snackbar 反馈）+ 版本 Timeline（MUI Timeline）+ 最近同步记录表
3. **版本浏览与下载**：Breadcrumbs + 文件表（名称/大小/mtime，点击进入目录）；行内下载按钮；工具栏「下载整版本 zip」（Dialog 显示预估大小，>500MB 建议 rsync）；侧栏展示单文件历史版本并支持下载任意时点文件
4. **日志**：任务筛选 + 最近 N 行，自动滚动
5. **设置**：配置摘要只读展示 + 修改指引（指回 CLI/手改 YAML）

---

## 8. 备份取回（三条路径）

| 路径 | 场景 | 依赖 |
|---|---|---|
| 文件系统直取 | 批量/大文件：`scp -r`、`rsync`、U盘、Samba | 零开发（current/versions 设计天然支持） |
| 面板下载中心 | 浏览器/手机临时取回单文件或整版本 | 7.1 下载流 + zip |
| CLI export/restore-file | 脚本化导出、恢复误删文件 | 6 |

Samba：部署助手提供可选配置模板（共享 `data/` 只读），独立于内核。

---

## 9. 数据与部署布局

### 9.1 树莓派目录

```
/opt/edge-sync/
├── bin/                      # edge-syncd、plugin-git、plugin-webdav、edge-sync(CLI,可选)
├── node/                     # 树莓派 Node 运行时（部署助手下载 armv7l tarball 安装）
├── panel/
│   ├── server.mjs            # esbuild 打包的 panel server 单文件
│   └── web/                  # Vite 构建产物 dist/
├── etc/
│   ├── config.yaml           # 内核配置（唯一真源）
│   └── panel.json            # 面板配置（端口、token）
├── var/
│   ├── edge-syncd.sock
│   ├── state/                # <task>.json
│   ├── logs/
│   └── cache/<task>/repo     # git 插件缓存仓库
└── data/<task>/
    ├── current -> versions/<ts>/
    ├── staging/
    └── versions/<ts>/
```

### 9.2 config.yaml 完整示例

```yaml
server:
  socket: /opt/edge-sync/var/edge-syncd.sock
storage:
  dataDir: /opt/edge-sync/data
  stateDir: /opt/edge-sync/var/state
log:
  level: info

tasks:
  - name: notes-repo
    plugin: git
    enabled: true
    interval: 300s
    snapshotTimeout: 60s
    fetchTimeout: 300s
    options:
      url: git@github.com:me/notes.git
      branch: main
      keyFile: ${HOME}/.ssh/deploy_key
    retention:
      keepLast: 10
      keepDays: 90

  - name: photos
    plugin: webdav
    interval: 1h
    options:
      url: https://dav.example.com/photos
      username: me
      passwordEnv: PHOTOS_DAV_PASSWORD
      fingerprint: etag        # etag | mtime_size
    retention:
      keepLast: 5
```

### 9.3 构建与部署流程

开发机（macOS arm64）：

1. Go 交叉编译：`GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0` → 3 个静态二进制
2. 前端：`pnpm --filter panel build`（Vite dist + esbuild server bundle）
3. `scripts/deploy.ts`：rsync 至树莓派 `/opt/edge-sync/`、生成双 systemd unit、`systemctl enable --now`
4. 可选：Samba 模板、Node 运行时安装

systemd unit 要点：`Restart=always`、`RestartSec=5`、`MemoryMax=128M`（内核）/`256M`（面板）、内核先启动面板后启动（socket 依赖）。

---

## 10. 安全与风险

### 10.1 安全

- 网络边界：仅局域网；面板无 HTTPS，token 明文传输风险由内网边界承担（文档明示）
- 鉴权：面板 Bearer token；CLI/IPC 走 unix socket 文件权限（0600）
- 路径安全：所有下载/导出接口 resolve 后强制限定于 `data/<task>/versions/<version>/` 白名单内，杜绝 `../` 穿越
- 凭证：配置支持 `${ENV}` 展开，密码经环境变量注入，不落明文

### 10.2 风险与对策

| 风险 | 对策 |
|---|---|
| SD 卡写入寿命 | 硬链接去重；staging 校验后一次落盘；写入量 = 纯变更量 |
| 1GB 内存 | 插件按需 spawn 即退；全流式 IO；下载限流；双 unit 各设 MemoryMax |
| 同步中断电/崩溃 | staging 丢弃即可；current symlink 原子切换，镜像永远完整 |
| WebDAV 服务器兼容性 | fingerprint 可配 etag / mtime_size；逐层 PROPFIND 降级 |
| zip 打包大版本慢 | store 模式可选；>500MB 引导走 rsync |
| 面板被滥用手动触发 | 触发接口对进行中任务幂等返回"进行中" |

---

## 附：协议 Schema 单一真源

`protocol/schema/` 下维护插件协议与 IPC 消息的 JSON Schema；Go 类型（`kernel/internal/protocol`）与 TS 类型（`protocol/` 包）与其对齐，双侧不得偏离。
