# AGENTS.md — AI 协作助手项目指引

> 本文件给在此仓库工作的 AI 编码助手提供上下文。读完再动手。

## 项目概述

edge-sync：边缘设备（树莓派 2B 1GB / 骁龙410 随身WiFi 512MB）上的**插件化同步备份服务**。
纯 Go 静态二进制（内核/面板/CLI/插件 ×5-6 个）+ React 前端（`web/`），目标机零运行时依赖。

- 内核：轮询远端源 → Manifest diff → 单向拉取 → **硬链接快照**保留历史
- 面板：Go server（`panel/cmd/edge-panel`，托管 `web/dist`）+ React 前端（`web/`）
- CLI：13 命令（`cmd/edge-sync`）
- 插件：外部进程协议（stdin/stdout 行 JSON-RPC，仅 `snapshot`/`fetchFile` 两方法）

## 硬约束（勿推翻，均为已验证决策）

1. **单向同步**：不做双向/冲突合并（用户明确排除）
2. **数据安全**：
   - 所有状态/元数据写入必须 `internal/fsutil.WriteFileAtomic`（fsync+rename，掉电安全）
   - `current` 只能以 symlink rename 原子切换；staging 校验通过才落盘
   - deploy.sh 的 rsync 必须排除 `data/`、`var/`、`etc/config.yaml`（**用户数据不可被部署覆盖**）
3. **鉴权**：Cookie（浏览器下载 `<a href>` 自动携带）+ Bearer 双通道，缺一即 401——下载用 `<a href>` 原生导航，任何改动不能破坏 cookie 通道
4. **面板非 root 绑 80**：unit 必须有 `AmbientCapabilities=CAP_NET_BIND_SERVICE`（User= + 80 端口互斥问题的解）
5. **WebDAV 已舍弃**（`archive/plugin-webdav`，代码保留不打包、不恢复测试）
6. **慢网络现实**：目标设备经手机热点，GitHub SSH 22 被运营商封（443 绕行已写设备 ~/.ssh/config）；git 任务必须可配 `snapshotTimeout`（设备示例 5m）
7. **无需 Node**：面板/CLI 均为 Go 静态二进制；`web/` 构建用 vite（仅开发机）

## 目录地图

```
cmd/{edge-syncd,edge-panel,edge-sync}   # 三个入口二进制
internal/{config,engine,ipc,logring,plugin,runner,state,fsutil}  # 内核
pkg/protocol                            # 协议+IPC 类型单一真源（Go）
pkg/pluginkit/                          # 插件骨架（行协议循环/SafeJoin/CopyWithHash）
plugins/{plugin-git,plugin-local}       # 官方插件
archive/plugin-webdav                   # 已实现但舍弃（勿恢复部署）
web/                                    # React 前端（teal MD 风格，明暗）
protocol/{schema?已删,src/index.ts}     # TS 类型包（前端 import）
scripts/deploy.sh                       # 部署助手（--target rpi2|qwifi --host user@ip）
docs/{DESIGN,panel-design,acceptance,TODO}.md
```

## 协议类型同步规则

`pkg/protocol`（Go）与 `protocol/src/index.ts`（web 类型导入）与 `docs/DESIGN.md §3`：
改任一处须同步另两处。IPC 返回结构以 `pkg/protocol/ipctypes.go` 为真源。

## 常用命令

```bash
go build ./... && go vet ./... && go test ./...   # 12 包全绿为基线
pnpm -r build                                     # protocol + web（tsc+vite）
./scripts/deploy.sh --target rpi2 --host young143@192.168.43.15   # 实机部署
./scripts/deploy.sh --target rpi2 --dry-run        # 仅构建
```

## 实机（树莓派 2B，运行中）

- SSH：`young143@192.168.43.15`（免密已配）；服务以 young143 运行（勿改回 root——git 认证依赖用户 ~/.ssh）
- 任务：young143blog（GitHub 私有镜像，5m 轮询，snapshotTimeout 5m/fetchTimeout 15m）
- 面板：`http://192.168.43.15`（端口 80，令牌 143335）
- 设备 GitHub SSH 走 443（22 被运营商封），配置在设备 ~/.ssh/config——**不要覆盖该文件**
- 部署/重启不丢数据（current/versions/状态均为原子写）；kill -9 由 systemd 5s 自愈

## 历史决策记录

`docs/acceptance.md`（含部署期间发现的 6 个模板 bug 与修复）、`docs/DESIGN.md`。
GitHub：Young143l/edge-sync（v0.1.0 已发布）。
