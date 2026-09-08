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
