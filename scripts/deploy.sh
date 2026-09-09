#!/usr/bin/env bash
# deploy.sh — edge-sync 部署助手（双目标：rpi2 / qwifi）
#
# 用法:
#   ./scripts/deploy.sh --target rpi2  --host user@host [--panel always|on-demand|off] [--dry-run]
#   ./scripts/deploy.sh --target qwifi --host user@host [--panel on-demand]  [--dry-run]
#
# 流程: Go 交叉编译 ×5（静态二进制）→ vite build → staging → systemd 双 unit
#       → rsync 至 /opt/edge-sync → 远端装配 → 冒烟。目标机无需任何运行时。
set -euo pipefail

TARGET=rpi2
HOST=""
PANEL=""
TOKEN=""
PORT=80
DRY_RUN=0
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGE="$ROOT/.deploy-staging"

usage() {
  sed -n '2,10p' "$0"
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target) TARGET="$2"; shift 2 ;;
    --host)   HOST="$2"; shift 2 ;;
    --panel)  PANEL="$2"; shift 2 ;;
    --token)  TOKEN="$2"; shift 2 ;;
    --port)   PORT="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage ;;
    *) echo "[deploy] 未知参数: $1"; usage ;;
  esac
done

# 目标矩阵（macOS 自带 bash 3.2，用 case 而非关联数组）
case "$TARGET" in
  rpi2)  GOARCH=arm;   GOARM=7;  PANEL_DEF=always;      MM_SYNCD=64M;  MM_PANEL=128M ;;
  qwifi) GOARCH=arm64; GOARM=""; PANEL_DEF=on-demand;  MM_SYNCD=64M;  MM_PANEL=128M ;;
  *) echo "[deploy] --target 必须是 rpi2 或 qwifi"; exit 1 ;;
esac
PANEL="${PANEL:-$PANEL_DEF}"
REMOTE=/opt/edge-sync
SSH_USER="${HOST%%@*}"

echo "[deploy] target=$TARGET arch=$GOARCH${GOARM:+v$GOARM} panel=$PANEL dryRun=$DRY_RUN"

rm -rf "$STAGE"
mkdir -p "$STAGE/bin" "$STAGE/panel" "$STAGE/etc" "$STAGE/docs"

# 1) Go 交叉编译（静态二进制）
BINS=(
  "cmd/edge-syncd|edge-syncd"
  "panel/cmd/edge-panel|edge-panel"
  "cmd/edge-sync|edge-sync"
  "plugins/plugin-local|plugin-local"
  "plugins/plugin-git|plugin-git"
)
export CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH"
[ -n "$GOARM" ] && export GOARM
for entry in "${BINS[@]}"; do
  pkg="${entry%%|*}"; name="${entry##*|}"
  echo "[deploy] go build $name"
  go build -trimpath -ldflags "-s -w" -o "$STAGE/bin/$name" "edge-sync/$pkg"
done
unset GOARM GOARCH GOOS CGO_ENABLED

# 2) 前端（vite → 静态 dist）
echo "[deploy] vite build (panel web)"
(cd "$ROOT/web" && pnpm build >/dev/null)
rm -rf "$STAGE/panel"
cp -R "$ROOT/web/dist" "$STAGE/panel"

# 3) systemd 双 unit
cat > "$STAGE/edge-syncd.service" <<UNIT
[Unit]
Description=edge-sync kernel daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$REMOTE
User=$SSH_USER
ExecStart=$REMOTE/bin/edge-syncd -config $REMOTE/etc/config.yaml
Restart=always
RestartSec=5
MemoryMax=$MM_SYNCD
Nice=5

[Install]
WantedBy=multi-user.target
UNIT

PANEL_INSTALL="WantedBy=multi-user.target"
[ "$PANEL" = "on-demand" ] && PANEL_INSTALL="# on-demand: systemctl start edge-panel 手动启用"
cat > "$STAGE/edge-panel.service" <<UNIT
[Unit]
Description=edge-sync web panel
After=edge-syncd.service
Requires=edge-syncd.service

[Service]
Type=simple
WorkingDirectory=$REMOTE
User=$SSH_USER
ExecStart=$REMOTE/bin/edge-panel -c $REMOTE/etc/panel.json
Restart=always
RestartSec=5
MemoryMax=$MM_PANEL

[Install]
$PANEL_INSTALL
UNIT

# 示例配置（远端已有则 rsync --ignore-existing 不会覆盖）
cat > "$STAGE/etc/config.yaml.example" <<EOF
server:
  socket: $REMOTE/var/edge-syncd.sock
storage:
  dataDir: $REMOTE/data
  stateDir: $REMOTE/var/state
  pluginBinDir: $REMOTE/bin
tasks: []
EOF
cat > "$STAGE/etc/panel.json.example" <<EOF
{
  "port": $PORT,
  "bind": "0.0.0.0",
  "token": "$TOKEN",
  "socket": "$REMOTE/var/edge-syncd.sock",
  "dataDir": "$REMOTE/data",
  "webDir": "$REMOTE/panel"
}
EOF

for f in DESIGN.md acceptance.md; do
  [ -f "$ROOT/docs/$f" ] && cp "$ROOT/docs/$f" "$STAGE/docs/"
done

echo "[deploy] staged: $(ls "$STAGE/bin" | tr '\n' ' ')+ panel dist + units"

if [[ $DRY_RUN -eq 1 ]]; then
  echo "[deploy] dry-run：仅构建，跳过 rsync/远端操作"
  exit 0
fi
[[ -n "$HOST" ]] || { echo "[deploy] 需要 --host user@host"; exit 1; }

# 4) 远端目录预备（/opt 需 sudo；建好后交目标用户）
ssh "$HOST" "sudo mkdir -p $REMOTE && sudo chown \$(whoami):\$(id -gn) $REMOTE"

# 5) rsync（etc/data/var 远端已有则不覆盖）
rsync -avz --delete "$STAGE/" \
  --exclude 'etc/config.yaml' --exclude 'etc/panel.json' \
  --exclude 'data' --exclude 'var' \
  "$HOST:$REMOTE/"

# 6) 远端装配 + 冒烟
ssh "$HOST" "
  mkdir -p $REMOTE/etc $REMOTE/var/state $REMOTE/data
  [ -f $REMOTE/etc/config.yaml ] || cp $REMOTE/etc/config.yaml.example $REMOTE/etc/config.yaml
  [ -f $REMOTE/etc/panel.json ]  || cp $REMOTE/etc/panel.json.example  $REMOTE/etc/panel.json
  sudo cp $REMOTE/edge-syncd.service $REMOTE/edge-panel.service /etc/systemd/system/
  sudo systemctl daemon-reload
  sudo systemctl enable --now edge-syncd
  $([ "$PANEL" = "always" ] && echo 'sudo systemctl enable --now edge-panel')
  systemctl is-active edge-syncd
  sleep 1
  curl -s -o /dev/null -w 'panel http: %{http_code}\n' http://127.0.0.1:$PORT/ || true
"
# 5) GitHub 通路体检（git 任务依赖 SSH 443 绕行的常见网络）
echo "[deploy] GitHub 通路体检..."
if ssh "$HOST" 'timeout 30 git ls-remote git@github.com:octocat/Hello-World.git HEAD >/dev/null 2>&1'; then
  echo "[deploy] GitHub SSH 通路正常"
else
  echo "[deploy] ⚠ GitHub SSH 不可达（常见：运营商封 22 端口）。修复："
  echo "[deploy]   在设备 ~/.ssh/config 加："
  cat <<'HINT'
Host github.com
  HostName ssh.github.com
  Port 443
  User git
HINT
  echo "[deploy]   （设备已有密钥时无需额外配置；详见 README 部署手册）"
fi

echo "[deploy] 完成。添加同步任务：$REMOTE/bin/edge-sync -c $REMOTE/etc/config.yaml add"
