# edge-sync 全面验收报告（阶段 0-4）

> 验收时间：2026-09-09 · 环境：开发机 macOS（实机验收见阶段 5）
> 范围：阶段 0~4 全部交付物。结论：**通过**（2 项遗留见文末）

---

## B1 自动化回归

| 项 | 结果 |
|---|---|
| `go test ./...` | 8 包全绿（config/engine/ipc/plugin/runner/state/plugin-git/plugin-webdav） |
| `go vet ./...` | clean |
| `pnpm -r build`（protocol/cli/panel-server/panel-web） | TS 0 错误 |

测试覆盖：diff 正确性、applier 原子性/回滚/硬链接、retention 边界、config 校验与 ENV 展开、插件客户端（正常/RPC 错误/超时）、IPC 全方法、git SSH 构造与 keyFile 前置校验、webdav 遍历/降级/续传/认证。

## B2 插件协议一致性

- 三插件 `initialize` 均返回 configSchema（git：url/branch/cacheDir/keyFile/username/token，含 GitHub 私有仓库场景说明；local：root；webdav：六字段）——schema 字段覆盖有测试断言
- snapshot/fetchFile：local 端到端（runner 测试）+ webdav 7 项 + git 9 项全绿
- 行分隔 JSON-RPC 信封、错误码、超时路径均有集成测试

## B3 内核引擎 live 复验

| 项 | 结果 |
|---|---|
| kill -9 自愈 | ✅ 重启后镜像一致（a.txt before=after=`hello panel`），状态完整，重启后正确报 no change |
| 硬链接去重 | ✅ 未变更文件跨版本 `nlink=5`（三版本共享 inode） |
| keepLast 滚动 | ✅ 6 轮变更后稳定保留 5 版本（第 6 轮同步记录 `pruned=1`） |
| 版本元数据 | ✅ `.edge-sync-manifest.json` 每版本落盘，history 查询与面板文件树使用正常 |

## B4 CLI 全命令（13 项全流程实测）

`plugins`（3 插件发现+schema）· `list`（三态正确）· `sync`（同步等待+结果）· `status`（详情/统计/保留）· `history`（6 版本）· `file-versions`（变更时点）· `export`（整版本导出）· `restore-file`（历史版本恢复，内容核对）· `logs`（tail）· `reload`（坏配置拒绝+旧配置继续）· `add`（向导，configSchema 驱动）· `edit`（$EDITOR 流程）· `remove`（--purge）——全部通过。

## B5 面板 12 条清单

| # | 条目 | 结果 |
|---|---|---|
| 1 | 无 token 401，输入 token 正常 | ✅ |
| 2 | 内核停机 → 离线横幅 + 重试按钮 | ✅ 实测（503→横幅「内核未连接」；**验收中修复**：横幅下方冗余 raw ApiError 文案已删除） |
| 3 | 四态卡片（正常/同步中/失败N次/暂停） | ✅ 三任务实测（bad-task 失败重试、paused-task 暂停） |
| 4 | 立即同步 Dialog→Snackbar→状态更新 | ✅ |
| 5 | 版本浏览导航正确、隐藏元数据文件 | ✅ |
| 6 | 单文件下载；路径穿越 400/404 | ✅ |
| 7 | 整版本 zip（X-Edge-Sync-Bytes 头） | ✅；>500MB rsync 建议为代码阈值判断（实机大文件复测归阶段 5） |
| 8 | 单文件历史 Drawer | ✅（验收中修复：手机端关闭按钮可见性、底部关闭按钮、标题截断） |
| 9 | 日志页任务/级别过滤、自动刷新 | ✅（WARN 行含失败详情与退避参数） |
| 10 | 明暗切换 + 响应式 | ✅（验收中修复：colorSchemeSelector/data、100svh、输入框防缩放、长文件名表格、面包屑折叠） |
| 11 | build 通过；web dist gzip 185KB < 500KB | ✅ |
| 12 | 树莓派实机 | ⏳ 阶段 5 |

## B6 资源指标（开发机基线）

| 进程 | RSS | 目标（实机） | 结论 |
|---|---|---|---|
| edge-syncd | **9.6 MB** | < 25 MB | ✅ |
| edge-panel (tsx dev) | **51 MB** | < 60 MB | ✅（实机 esbuild 单文件复测） |

## 验收期间发现并修复的问题

1. 离线状态下 Dashboard 冗余渲染 raw ApiError 文案 → 删除
2. （早前同批）深色切换失效 / 下载 401 / 手机 Drawer 关闭按钮 / 长文件名撑表 / 同步记录不换行 / 视口跳动 / 命令行同步 Dialog 不关闭——均已修复并回归

## 遗留（明确后置）

| 项 | 依赖 |
|---|---|
| GitHub 私有仓库 SSH 真实拉取联调 | 用户提供 deploy key |
| 真实 WebDAV 源联调 | 用户提供测试源 |
| zip >500MB 建议条实测 | 大文件真实源 |
| 阶段 5 全部实机项 | 树莓派 SSH |

**结论**：阶段 0-4 验收通过。下一步：阶段 5 部署实机（等 SSH）。
