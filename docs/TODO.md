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

- [ ] `config/`：YAML 加载、字段校验、`${ENV}` 展开、SIGHUP/IPC 热重载
- [ ] `plugin/`：插件进程客户端（spawn、行分隔 JSON-RPC、超时控制、stderr 日志采集）
- [ ] `engine/differ`：Manifest diff（added/modified/removed）+ manifestFingerprint 短路
- [ ] `engine/applier`：staging 校验 → `versions/<ts>/` 硬链接组装 → `current` symlink 原子切换 → 状态原子写
- [ ] **retention 保留清理：keepLast（默认 10，数量限制）+ keepDays（可选），current 指向版本始终保留**
- [ ] `state/`：状态 JSON 原子读写
- [ ] `scheduler/`：每任务 ticker、启动 jitter、连续失败指数退避（上限 1h）、手动触发复位
- [ ] mock 插件 `plugins/plugin-mock`：可配置地产生文件变更，用于端到端联调
- [ ] 单元测试：diff 正确性、applier 原子性、retention 清理边界（keepLast=0/1、keepDays 过期、current 保护）

**验收**：
1. mock 插件连续两轮变更后，`versions/` 出现两个快照，`current` 指向最新
2. 未变更文件跨版本硬链接（`stat` 链接数 > 1），保留策略按 keepLast 滚动删除旧版本
3. `kill -9` 内核后重启，本地镜像与状态完整，无半成品
4. `go test ./...` 全绿

**状态**：未开始

---

## 阶段 2：官方插件（纯开发机网络联调）

- [ ] `plugin-webdav`：PROPFIND 遍历（infinity，降级逐层）、fingerprint 策略（etag / mtime_size）、GET + Range 断点续传、写 destPath
- [ ] `plugin-git`：`git ls-remote` 轮询、缓存仓库 `fetch --depth 1`、worktree 遍历生成 Manifest、凭证（token / SSH keyFile + `${ENV}` 展开）
- [ ] 真实源联调（**需用户提供**：测试 WebDAV 地址+账号；测试 Git 仓库，公共仓库可先行）
- [ ] 插件协议一致性测试（schema 校验、超时、错误退出码路径）

**验收**：
1. 真实 WebDAV：首次全量拉取 → 远端改文件 → 下一轮仅拉取变更文件
2. 真实 Git：push 新 commit → 下一轮检测并拉取；无新 commit 时 ls-remote 短路零下载
3. 大文件（≥100MB）下载稳定，中断后重试成功

**状态**：未开始

---

## 阶段 3：IPC + CLI（纯开发机）

- [ ] `ipc/`：Unix socket JSON-RPC server（task.list / task.status / task.trigger / history.list / history.files / history.fileVersions / config.reload / log.tail；event.subscribe 仅留桩）
- [ ] CLI 骨架：`edge-sync` 命令（tsx），IPC 客户端
- [ ] 命令实现：`list / status / add / edit / remove / sync / history`
- [ ] 命令实现：`export [--version] --out`、`restore-file [--version] --out`
- [ ] `add` 向导读取插件 configSchema 做输入提示与校验

**验收**：
1. CLI 完成 add → sync → history → export 全流程
2. 手改 YAML 后 `config.reload` 生效，错误配置返回可读报错
3. restore-file 能从指定历史版本恢复单个文件

**状态**：未开始

---

## 阶段 4：Web 面板（纯开发机）

### 4a server
- [ ] Hono 服务：REST API（overview / tasks / sync / history / files / file-versions / logs）
- [ ] Bearer token 鉴权中间件（panel.json，首装随机生成）
- [ ] 文件下载流（fs.createReadStream + 路径白名单防穿越）
- [ ] 整版本 zip 流式打包（archiver，store 模式可选，>500MB 提示字段）
- [ ] 下载并发限制（1~2）

### 4b web（React + MUI，MD 风格）
- [ ] Vite + React + MUI + TanStack Query + React Router 脚手架
- [ ] 布局：App Bar + Navigation Drawer（小屏收起）+ 明暗主题
- [ ] 仪表盘：任务卡片网格（状态 Chip、LinearProgress、上次同步、文件数/体积）
- [ ] 任务详情：立即同步（Dialog 确认 + Snackbar）、版本 Timeline
- [ ] 版本浏览与下载：Breadcrumbs 文件树、行内下载、整版本 zip、单文件历史侧栏
- [ ] 日志页、设置页（只读摘要）

**验收**：浏览器完成闭环「看状态 → 触发同步 → 浏览版本 → 下载单文件 → 下载 zip」；token 未授权请求被拒

**状态**：未开始

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
