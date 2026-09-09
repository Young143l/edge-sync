# edge-sync 执行清单（TODO）

> 配套设计文档：`docs/DESIGN.md`。逐项勾选留痕，每阶段完成后汇报验收结果，确认后进入下一阶段。
>
> 阶段 0~4 全部在开发机（macOS）完成；阶段 5 需树莓派 SSH。

---

## 阶段 0：脚手架

- [x] monorepo 目录结构：`kernel/ plugins/ protocol/ cli/ panel/ scripts/ docs/`
- [x] Go module 初始化（根 module `edge-sync`），`kernel/cmd/edge-syncd`、`plugins/plugin-git`、`plugins/plugin-webdav` 空 main 可编译
- [x] pnpm workspace 初始化（`cli/`、`protocol/`；`panel/*` 预留 glob），最小可构建
- [x] `protocol/schema/` 插件协议 JSON Schema：envelope / manifest / methods 三文件，initialize / snapshot / fetchFile 报文与 Manifest 结构
- [x] Go 侧类型 `kernel/internal/protocol/types.go` 与 schema 对齐
- [x] TS 侧类型包 `protocol/`（`@edge-sync/protocol`）与 schema 对齐
- [x] `.gitignore`、git 仓库初始化、首次提交

**验收**：`go build ./...` 通过；`go vet ./...` 通过；`pnpm -r build` 通过（tsc × 2）；`tsx` 运行 CLI 占位入口正常，workspace 链接生效

**状态**：已完成（2025 开工首日）

**备注**：pnpm v12 拦截 esbuild postinstall（安全机制），平台二进制经 optionalDependencies 已就位、功能无影响；消除警告需在交互终端跑一次 `pnpm approve-builds` 选 esbuild。

---

## 阶段 1：内核骨架 + mock 插件端到端（纯开发机）

- [x] `config/`：YAML 加载、字段校验、`${ENV}` 展开（缺失变量 fail-fast）、SIGHUP 热重载已接线（IPC reload 阶段 3）
- [x] `plugin/`：插件进程客户端（spawn、行分隔 JSON-RPC、超时 kill + WaitDelay 防管道阻塞、stderr 日志采集）
- [x] `engine/differ`：Manifest diff（added/modified/removed）
- [x] `engine/applier`：staging 校验 → `versions/<ts>/` 硬链接组装 → `current` 相对 symlink 原子切换 → 失败自动回滚
- [x] **retention 保留清理：keepLast（默认 10，数量限制，0=不限）+ keepDays（可选），current 指向版本始终保留且计入预算**
- [x] `state/`：状态 JSON 原子读写（temp+rename）
- [x] `scheduler/`（并入 runner）：每任务 ticker、启动 jitter、连续失败指数退避（上限 1h）、手动触发复位
- [x] mock 插件 → **plugin-local**：本地目录源（sha256 指纹 + manifestFingerprint 短路），既当 mock 又是真实可用的源类型；源级指纹未变时零下载短路
- [x] 单元测试：diff 正确性、applier 原子性/回滚/硬链接、retention 边界（keepLast 滚动/keepDays 过期/current 保护/外部目录不碰）、config 校验与 ENV、插件客户端（正常/RPC 错误/超时）、runner 端到端

**验收**（全部通过）：
1. ✅ 连续两轮变更后 `versions/` 出现两个快照，`current` 指向最新
2. ✅ 未变更文件跨版本硬链接（实测 nlink=2），keepLast=2 三轮滚动删除旧版本
3. ✅ `kill -9` 后重启，镜像与状态完整，重启后正确报 "no change"（无重复下载、无半成品）
4. ✅ `go test ./...` 全绿（engine/plugin/runner/config/state 五包）

**状态**：已完成

**备注**：
- Go 协议类型从 `kernel/internal/protocol` 迁至 `pkg/protocol`（internal 可见性限制，插件需复用）；schema 单一真源不变
- TODO 原定的 `plugin-mock` 实现为 `plugin-local`（本地目录源）：真实 IO、可当正式源类型用，测试直接往 root 写文件制造变更

---

## 阶段 2：官方插件（纯开发机网络联调）

- [x] `pkg/pluginkit`：插件公共骨架（行协议主循环、带码错误、SafeJoin、CopyWithHash、源级指纹合成）
- [x] `plugin-webdav`：PROPFIND 遍历（infinity + 403/400 降级逐层 BFS）、href 三策略消歧（绝对路径 / 挂载点相对 / 当前目录相对）、fingerprint 策略（etag / mtime_size）、GET + Range 断点续传（size 不符自动全量重试）、Basic Auth（password / passwordEnv）
- [x] `plugin-git`：`git ls-remote` 轮询、缓存仓库 init/fetch `--depth 1`（token 只经命令行不落盘）、`ls-files` blob hash 生成 Manifest、commit hash 短路、symlink 内容拉取、SSH keyFile（GIT_SSH_COMMAND）、认证错误分类
- [x] 本地联调（真实内核驱动双任务）：WebDAV 本地服务器 + 本地 git 仓库
- [x] 测试：webdav 7 项（快照/变更检测/Depth 降级/认证/指纹策略/options 校验/Range 续传）；git 6 项（快照/新 commit 检测/缺失分支/缺 options/buildAuthURL/token 校验）
- [ ] 真实远端源联调（**待用户提供**：测试 WebDAV 地址+账号；测试 Git 仓库）
- [ ] 大文件（≥100MB）真实下载与中断重试（依赖真实源联调）

**验收**（本地部分全部通过）：
1. ✅ 本地 WebDAV：首次全量（2 文件）→ 远端改 1 文件 → 下一轮仅拉取该文件（totalFiles 2+1）
2. ✅ 本地 Git：新 commit 检测并拉取；无新 commit 时 ls-remote + commit hash 双重短路零下载
3. ⏳ 大文件下载稳定（Range 续传逻辑已由单测验证，10KB 级）；≥100MB 级验证待真实源

**状态**：本地部分已完成；真实源联调待用户提供信息后补验收

**备注**：调试中沉淀的两个 WebDAV 兼容性要点已固化在代码注释——① href 可能是「挂载点相对路径」（如 x/net/webdav StripPrefix 行为）需拼回 base 前缀；② 相对引用优先按「相对 WebDAV 根」解释（主流实现形态），其次才是 RFC 3986 相对当前目录。

---

## 阶段 3：IPC + CLI（纯开发机）

- [x] `ipc/`：Unix socket JSON-RPC server（task.list / task.status / task.trigger / history.list / history.files / history.fileVersions / config.reload / log.tail / plugin.list；event.subscribe 留桩返回明确未实现错误）
- [x] `logring/`：内存环形日志（slog handler 包装，供 log.tail；支持按 task 过滤）
- [x] 版本元数据：applier 每版本落盘 `.edge-sync-manifest.json`（history 查询直接读元数据）
- [x] `runner` 增强：任务快照（syncing/nextRunAt）、配置热重载生命周期
- [x] config 路径语义统一：相对路径一律相对配置文件所在目录（内核与 CLI 一致，cwd 无关）
- [x] CLI 全命令：list / status / add（向导，读 configSchema）/ edit（$EDITOR）/ remove（--purge）/ sync / history / file-versions / export / restore-file / logs / plugins / reload
- [x] CLI IPC 客户端 + YAML 配置编辑（编辑后自动 config.reload）

**验收**（全部通过，实测演示）：
1. ✅ sync → history → export 全流程（live daemon + 双任务）
2. ✅ 手改 YAML 后 config.reload 生效；坏配置 reload 被拒且错误可读（"invalid name '../evil'..."），旧任务集继续工作，恢复后 reload 成功
3. ✅ restore-file 从指定历史版本恢复单个文件（内容与版本一致）
4. ✅ export 过滤元数据文件；current symlink 正确解引用导出
5. ✅ go test 8 包全绿；pnpm -r build 零错误

**状态**：已完成

---

## 阶段 4：Web 面板（纯开发机）

> 专项设计：`docs/panel-design.md`（已确认 v1：teal 主色 / 卡片网格 / 不加增强 / 详情页双 Tab）。验收清单 12 条见该文档第 5 节。

### 4a server
- [ ] `panel/server`：Hono + @hono/node-server，panel.json 配置（port/bind/token/socket/dataDir，缺 token 自动生成回写）
- [ ] Bearer token 鉴权（/api/*）；IPC 错误→HTTP 映射（404/400/503/502）
- [ ] 状态类路由（overview / task / sync / history / files / file-versions / logs / plugins 转发 IPC）
- [ ] 下载流：单文件（fs + 路径白名单）+ 整版本 zip（archiver 流式，store=1，X-Edge-Sync-Bytes / >500MB 建议）
- [ ] 下载并发限流（全局 2 + 429 兜底）
- [ ] curl 端到端冒烟（真实内核 + 401/200/404 断言）

### 4b web（React + MUI，MD 风格）
- [ ] Vite + React + MUI + TanStack Query + React Router 脚手架 + teal 主题（明暗双 scheme）
- [ ] 布局：App Bar（状态 Chip/刷新/明暗切换）+ Drawer（仪表盘/日志/设置）+ 响应式断点
- [ ] 仪表盘：任务卡片网格（四态 Chip、LinearProgress、文件数/体积、服务概览条）
- [ ] 任务详情（双 Tab）：概览（版本 Timeline + 最近同步 + 立即同步 Dialog）| 版本与文件（面包屑文件树 + 下载 + 单文件历史 Drawer + zip）
- [ ] 日志页、设置页（只读摘要）
- [ ] 登录 token Dialog + 内核离线横幅 + 401 处理
- [ ] 浏览器端到端闭环验收（对照 panel-design 验收清单 1~11）

**状态**：进行中（4a）

---

## 阶段 5：部署实机（**需用户提供树莓派 SSH**）

- [ ] **环境勘察**：`uname -m`、系统版本、`free -h`、`df -h`、SD 卡健康，确认 GOARM=7 与 `/opt/edge-sync` 落位
- [ ] `scripts/deploy.ts`：交叉编译（`GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0`）→ rsync → 生成 `edge-syncd.service` + `edge-panel.service` → enable
- [ ] 树莓派 Node 运行时安装（armv7l tarball，部署助手完成）
- [ ] 可选：Samba 只读共享模板
- [ ] 实机压测：内存、轮询 IO、SD 卡写入量观测
- [ ] 故障演练：kill -9 / 断电模拟后自愈
- [ ] README 与使用文档（部署、配置、取回备份操作手册）

**验收**：
1. `edge-syncd` RSS < 25MB，`edge-panel` RSS < 60MB
2. 24h 连续轮询稳定，无内存增长趋势
3. kill -9 后 systemd 5s 内拉起，镜像完整
4. 手机浏览器经局域网访问面板并成功下载备份

**状态**：未开始

---

## 待用户提供的信息

| 时间点 | 事项 | 状态 |
|---|---|---|
| ~~阶段 0 前~~ | 开发机安装 Go | 已完成（go1.27.1 darwin/arm64） |
| 阶段 2 联调 | 测试 WebDAV 源（地址+账号） | 待提供 |
| 阶段 2 联调 | 测试 Git 仓库（公共仓库可先行） | 待提供 |
| 阶段 5 部署 | 树莓派 SSH 连接（IP/用户/密钥） | 待提供 |
