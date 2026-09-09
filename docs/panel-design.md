# edge-sync Web 面板专项设计

> 配套文档：`docs/DESIGN.md`（总方案）、`docs/TODO.md`（执行清单）
> 本文件是阶段 4（panel/server + panel/web）的实施依据，经确认后不再大改。
>
> 状态：已确认 v1

---

## 1. 定位与边界

面板是内核状态的**可视化皮肤**与备份取回的**浏览器入口**，只做四类事：

| 类别 | 内容 | 不做 |
|---|---|---|
| 看 | 任务状态 / 版本历史 / 文件树 / 日志 | — |
| 触发 | 手动同步（Dialog 确认 → Snackbar 反馈） | 配置修改（走 CLI/手改 YAML + reload） |
| 取回 | 单文件下载 / 整版本 zip / 单文件历史 | — |
| 展示 | 设置页只读摘要 | 在线编辑 |

**硬边界**：面板崩溃不影响同步（独立 systemd unit）；面板只读内核 + 转发触发；下载流直读磁盘不过内核。按用户决策，**不增加**文件预览、版本对比、配置快照导出等增强功能——严格按 DESIGN 第 7 章范围。

## 2. 系统位置与数据流

```
浏览器 (React SPA, 静态托管)
   │ HTTP :8080, Bearer token
   ▼
edge-panel (Node + Hono, 独立 systemd unit)
   ├── /api/* ── unix socket JSON-RPC ──► edge-syncd   (状态/触发/历史)
   └── 下载流 ── fs 直读 data/<task>/…               (不过内核)
```

## 3. server（4a）

### 3.1 自身配置 `etc/panel.json`

```json
{
  "port": 8080,
  "bind": "0.0.0.0",
  "token": "<openssl rand -hex 24>",
  "socket": "/opt/edge-sync/var/edge-syncd.sock",
  "dataDir": "/opt/edge-sync/data"
}
```

- 首装由部署助手生成 token；面板启动读取，缺失字段时自动生成并回写
- 所有 `/api/*` 必检 `Bearer <token>`；静态资源不检
- token 明文经局域网传输（无 HTTPS），由局域网边界承担，设置页明示

### 3.2 REST 契约终版

| 方法 | 路径 | 返回 | 数据源 |
|---|---|---|---|
| GET | `/api/overview` | `TaskSummary[]` | IPC task.list |
| GET | `/api/tasks/:name` | `TaskDetail` | IPC task.status |
| POST | `/api/tasks/:name/sync` | `{triggered:true}`（同步等待完成） | IPC task.trigger |
| GET | `/api/tasks/:name/history` | `VersionInfo[]` | IPC history.list |
| GET | `/api/tasks/:name/files?version=&path=` | `FileListEntry[]` | IPC history.files |
| GET | `/api/tasks/:name/file-versions?path=` | `FileVersionEntry[]` | IPC history.fileVersions |
| GET | `/api/tasks/:name/download?version=&path=` | 文件流（`Content-Disposition: attachment`） | fs 直读 |
| GET | `/api/tasks/:name/archive?version=&store=1` | zip 流 | archiver 流式 |
| GET | `/api/logs?n=&task=` | `LogEntry[]` | IPC log.tail |
| GET | `/api/plugins` | `PluginInfo[]` | IPC plugin.list |

类型全部来自 `@edge-sync/protocol`（TS 侧已定义）。

### 3.3 错误映射与限流

| IPC 错误 | HTTP |
|---|---|
| 鉴权失败 | `401` |
| NotFound / 版本不存在 | `404` |
| InvalidParams / 路径穿越 | `400` |
| 内核 socket 不可达 | `503`（前端显示「内核离线」横幅） |
| 其他 | `502` |

- 下载限流：全局并发 2，排队 + `429` 兜底
- `archive`：默认 deflate，`store=1` 仅存储（2B 省 CPU）；响应头 `X-Edge-Sync-Bytes`（预估体积，读版本 manifest）；>500MB 附 `X-Edge-Sync-Suggest: rsync`
- 下载路径安全：resolve 后强制 within `data/<task>/versions/<version>/`（与内核同款白名单语义，二次防御）

### 3.4 server 技术细节

- Hono + `@hono/node-server`；esbuild 打包为单文件 `panel/server.mjs`
- 启动：`node server.mjs -c etc/panel.json`
- IPC socket 路径与 dataDir 来自 panel.json（部署助手写入），面板自身不读内核 YAML

## 4. web（4b）

### 4.1 技术栈与信息架构

- Vite 6 + React 19 + TypeScript + MUI v7 + TanStack Query + React Router 7
- 类型从 `@edge-sync/protocol` 贯通
- **信息架构**（任务详情与版本浏览合并为详情页双 Tab，Drawer 保持三项静态导航）：

```
Drawer：仪表盘 / 日志 / 设置
路由：
  /                    仪表盘（任务卡片网格）
  /task/:name          任务详情
    Tab: 概览（状态 + 版本时间线 + 最近同步）
    Tab: 版本与文件（面包屑文件树 + 下载 + 单文件历史）
  /logs                日志
  /settings            设置（只读摘要）
```

### 4.2 MD 视觉 token（teal 系，用户已定）

**色彩**：

| token | 亮色 | 暗色 | 用途 |
|---|---|---|---|
| primary | `#00695C` | `#4DB6AC` | 按钮 / 选中 / 品牌元素 |
| primary-container | `#CCE8E4` | `#0F3A35` | 选中卡片描边 / Chip 底 |
| background | `#F4F9F9` | `#10201E` | 页面底 |
| surface | `#FFFFFF` | `#182B28` | 卡片 / Drawer / App Bar |
| text-primary | `#1A2B29` | `#E0ECEA` | 正文 |
| success | `#2E7D32` | `#66BB6A` | 状态 Chip「正常」 |
| warning | `#ED6C02` | `#FFB74D` | 「同步中」 |
| error | `#D32F2F` | `#EF5350` | 「失败」 |
| divider | `#DDE6E4` | `#2A3D3A` | 分隔线 |

**字体**：Roboto + 中文回退 `system-ui, "PingFang SC", "Microsoft YaHei"`；页面标题 h6（20px/600）、正文 body2（14px）、辅助 caption（12px）。

**形状与层级**：卡片圆角 12px（hover 升至 elevation 3）、按钮 8px、Chip 8px；卡片 elevation 1；页面 gutter 24px，卡片内边距 16px。

**动效克制**：Chip 颜色过渡、LinearProgress（同步中）、按钮 ripple（MUI 自带）；无额外动画。

**主题切换**：`colorSchemes` 跟随系统 + App Bar 手动切换按钮，选择存 localStorage。

### 4.3 页面线框

**仪表盘**：

```
┌ App Bar: ☰  edge-sync        [● 运行中] [⟳] [◐] ──────────────┐
├────────┬───────────────────────────────────────────────────────┤
│ 仪表盘  │ ┌ 服务概览条：3 任务 · 1 同步中 · 最近失败 2h 前 ─────┐ │
│ 日志    │ └────────────────────────────────────────────────────┘ │
│ 设置    │ ┌─────────────────┐ ┌─────────────────┐ ┌────────────┐│
│        │ │ notes-repo       │ │ photos-dav       │ │ archives   ││
│        │ │ [● 正常]         │ │ [⟳ 同步中 ▓▓░░]  │ │ [⚠ 失败×3] ││
│        │ │ git · 每5分钟     │ │ webdav · 每1小时  │ │ git        ││
│        │ │ 5 分钟前 · 1204 文│ │ 12 文件 · 1.2 GB  │ │ 340 文件   ││
│        │ │ [立即同步] [详情→]│ │ [立即同步] [详情→]│ │ [→]        ││
│        │ └─────────────────┘ └─────────────────┘ └────────────┘│
│        │ （小屏单列堆叠）                                          │
└────────┴───────────────────────────────────────────────────────┘
```

- 卡片信息：任务名、状态 Chip、插件/间隔、上次成功相对时间、文件数/体积
- 失败卡片：Chip `失败 (n 次重试)`，卡片描边 error 色
- 同步中卡片：LinearProgress indeterminate，[立即同步] 禁用
- 空态：居中提示「还没有任务，用 `edge-sync add` 添加」

**任务详情（双 Tab）**：

```
┌← 返回  notes-repo                     [● 正常] [立即同步] [⇣ 导出] ┐
│ url · 每 5 分钟 · 下次 14:35 · 连续失败 0                           │
│ Tabs: [概览] [版本与文件]                                            │
│ 概览:                           版本与文件:                          │
│ ┌ 版本 Timeline ─────────┐     ┌ 面包屑： 根 / docs / api ──────┐    │
│ │ ● 150053 ← 当前 +3 -1  │     │ 名称        大小    操作         │    │
│ │ ● 144953       +12     │     │ sdk/         —      打开         │    │
│ │ ● 144846       86 MB   │     │ api.md       14KB   下载 历史     │    │
│ └────────────────────────┘     └──────────────────────────────────┘    │
│ ┌ 最近同步记录 ───────────┐     [⇣ 下载整版本 zip]                     │
│ │ 14:49 成功 +3 -1 (812ms)│     点「历史」→ 右侧 Drawer:               │
│ │ 14:44 成功 (短路 96ms)   │     该文件版本列表 + 各时点下载             │
└─────────────────────────────────────────────────────────────────────┘
```

- 「立即同步」：Dialog 确认（任务名 + 预计动作）→ POST sync → Snackbar 反馈
- Timeline 条目点击 → 跳「版本与文件」Tab 并定位该版本；`version` 由 URL query 保持

**日志页**：任务/级别筛选 + 自动刷新开关（默认开 5s）+ 最新在列、等宽字体。

**设置页**（只读）：内核版本 / socket / storage 路径 / 插件列表（名称+版本+configSchema 属性）/ token 脱敏 / 「修改配置请用 CLI」指引。

### 4.4 数据层（TanStack Query）

| Query key | 端点 | 轮询 |
|---|---|---|
| `['overview']` / `['task', name]` | 5s；存在 syncing 时 2s | 
| `['history', name]` | 手动刷新 + sync 成功后失效 |
| `['files', name, version, path]` | 手动 |
| `['fileVersions', name, path]` | 手动 |
| `['logs', n, task]` | 5s（自动刷新开时） |
| `['plugins']` | 一次性 |

全局 `retry: 1`；503 不重试（立即离线横幅）；401 → 清 token 弹登录 Dialog。

### 4.5 响应式断点

| 宽度 | 布局 |
|---|---|
| ≥1200px | Drawer 常驻 240px，卡片 3 列 |
| 900~1199px | Drawer 常驻，卡片 2 列 |
| <900px | Drawer 折叠为图标栏（App Bar ☰ 临时展开），卡片 1 列 |

## 5. 实施顺序与验收清单

**顺序**：4a server（含 curl 验收）→ 4b 脚手架与主题 → 仪表盘 → 任务详情（两 Tab）→ 日志/设置 → 登录与明暗切换 → 端到端联调。

**验收清单（全部满足即阶段 4 完成）**：

1. 无 token 请求 401，输入 token 后正常使用（localStorage 记忆）
2. 内核未启动时：面板可打开，「内核离线」横幅 + 重试按钮
3. 仪表盘四态卡片正确（正常/同步中/失败含重试数/暂停），与 CLI status 一致
4. 立即同步：Dialog 确认 → 内核实际执行 → Snackbar 反馈 → 状态自动更新（轮询可见）
5. 版本浏览：目录导航正确、面包屑回退、隐藏元数据文件（.edge-sync-manifest.json）
6. 单文件下载（Content-Disposition 附件名正确）；路径穿越请求被 400/404 拒绝
7. 整版本 zip：浏览器可下载解压；store=1 选项生效；>500MB 显示 rsync 建议
8. 单文件历史 Drawer：列出变更时点，可下载任意历史版本文件
9. 日志页：任务过滤、级别显示、自动刷新开合
10. 明暗主题切换 + 跟随系统生效；<900px 响应式折叠
11. `pnpm -r build` 通过；web dist gzip 后 < 500KB
12. 树莓派实机（阶段 5）：edge-panel RSS < 60MB，浏览器局域网访问闭环

## 6. 排除项（用户已确认不做）

文件预览、版本对比、配置快照导出、webhook 触发、配置在线编辑。预留的 UI 位（详情页右侧 Drawer、设置页卡片）在后续迭代中可自然扩展。
