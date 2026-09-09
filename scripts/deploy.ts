// deploy.ts — edge-sync 部署助手（双目标：rpi2 / qwifi）。
//
// 用法：
//   npx tsx scripts/deploy.ts --target rpi2  --host user@host [--dry-run]
//   npx tsx scripts/deploy.ts --target qwifi --host user@host [--panel on-demand|always|off]
//
// 流程：构建（Go 交叉编译 ×5 + web dist）→ rsync 至 /opt/edge-sync →
// 生成 systemd 双 unit → 远端冒烟。--dry-run 只构建不 rsync。
//
// 约定：目标机器无需 Node/运行时（全 Go 静态二进制）；面板前端为纯静态文件。
import { execSync, spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'

// ---------- 参数 ----------

interface Args {
  target: 'rpi2' | 'qwifi'
  host?: string
  dryRun: boolean
  panel: 'always' | 'on-demand' | 'off'
}

function parseArgs(argv: string[]): Args {
  const a: Args = { target: 'rpi2', dryRun: false, panel: 'always' }
  for (let i = 0; i < argv.length; i++) {
    const v = argv[i + 1]
    switch (argv[i]) {
      case '--target':
        if (v !== 'rpi2' && v !== 'qwifi') die(`--target 必须是 rpi2 或 qwifi（got ${v}）`)
        a.target = v
        i++
        break
      case '--host':
        a.host = v
        i++
        break
      case '--panel':
        if (v !== 'always' && v !== 'on-demand' && v !== 'off') {
          die(`--panel 必须是 always|on-demand|off（got ${v}）`)
        }
        a.panel = v
        i++
        break
      case '--dry-run':
        a.dryRun = true
        break
      default:
        die(`未知参数 ${argv[i]}`)
    }
  }
  return a
}

function die(msg: string): never {
  console.error(`[deploy] ${msg}`)
  process.exit(1)
}

function sh(cmd: string, opts: { dry?: boolean } = {}): void {
  console.log(`[deploy] $ ${cmd}`)
  if (opts.dry) return
  execSync(cmd, { stdio: 'inherit', cwd: ROOT })
}

function shCapture(cmd: string): string {
  return execSync(cmd, { cwd: ROOT, encoding: 'utf8' })
}

const ROOT = resolve(import.meta.dirname ?? ".", "..")

// ---------- 目标矩阵 ----------

interface Target {
  name: string
  goArch: string
  goArm: string // GOARM（arm 时）
  remoteDir: string
  panelDefault: Args['panel']
  memoryMaxSyncd: string
  memoryMaxPanel: string
}

const TARGETS: Record<string, Target> = {
  rpi2: {
    name: 'rpi2',
    goArch: 'arm',
    goArm: '7',
    remoteDir: '/opt/edge-sync',
    panelDefault: 'always',
    memoryMaxSyncd: '64M',
    memoryMaxPanel: '128M',
  },
  qwifi: {
    name: 'qwifi',
    goArch: 'arm64', // 骁龙 410 刷机系统多为 aarch64；勘察后若为 32 位改 arm/7
    goArm: '',
    remoteDir: '/opt/edge-sync',
    panelDefault: 'on-demand', // 512MB 精简
    memoryMaxSyncd: '64M',
    memoryMaxPanel: '128M',
  },
}

// ---------- 构建 ----------

const GO_BINS: Array<[string, string]> = [
  ['kernel/cmd/edge-syncd', 'edge-syncd'],
  ['panel/goserver', 'edge-panel'],
  ['cli-go', 'edge-sync'],
  ['plugins/plugin-local', 'plugin-local'],
  ['plugins/plugin-git', 'plugin-git'],
]

function goBuild(t: Target, out: string): void {
  const env = {
    ...process.env,
    GOOS: 'linux',
    GOARCH: t.goArch,
    ...(t.goArm ? { GOARM: t.goArm } : {}),
    CGO_ENABLED: '0',
  }
  for (const [pkg, name] of GO_BINS) {
    console.log(`[deploy] go build ${name} (${t.goArch}${t.goArm ? 'v' + t.goArm : ''})`)
    const r = spawnSync('go', ['build', '-trimpath', '-ldflags', '-s -w', '-o', join(out, name), `edge-sync/${pkg}`], {
      cwd: ROOT,
      env,
      stdio: 'inherit',
    })
    if (r.status !== 0) die(`go build ${name} 失败`)
  }
}

function buildWebDist(out: string): void {
  console.log('[deploy] vite build (panel web)')
  const r = spawnSync('pnpm', ['--filter', '@edge-sync/panel-web', 'build'], {
    cwd: ROOT,
    stdio: 'inherit',
  })
  if (r.status !== 0) die('web build 失败')
  // 拷贝 dist 到 staging。
  const dist = resolve(ROOT, 'panel/web/dist')
  rmSync(out, { recursive: true, force: true })
  mkdirSync(out, { recursive: true })
  for (const f of readdirSync(dist)) {
    execSync(`cp -R ${JSON.stringify(join(dist, f))} ${JSON.stringify(join(out, f))}`)
  }
}

// ---------- systemd ----------

function systemdUnits(t: Target, panelMode: Args['panel']): Array<[string, string]> {
  const d = t.remoteDir
  const syncd = `[Unit]
Description=edge-sync kernel daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${d}
ExecStart=${d}/bin/edge-syncd -config ${d}/etc/config.yaml
Restart=always
RestartSec=5
MemoryMax=${t.memoryMaxSyncd}
Nice=5

[Install]
WantedBy=multi-user.target
`

  const panel = `[Unit]
Description=edge-sync web panel
After=edge-syncd.service
Requires=edge-syncd.service

[Service]
Type=simple
WorkingDirectory=${d}
ExecStart=${d}/bin/edge-panel -c ${d}/etc/panel.json
Restart=always
RestartSec=5
MemoryMax=${t.memoryMaxPanel}

[Install]
${panelMode === 'always' ? 'WantedBy=multi-user.target' : '# 按需模式：不随开机自启（systemctl start edge-panel 手动启用）'}
`

  return [
    ['edge-syncd.service', syncd],
    ['edge-panel.service', panel],
  ]
}

// ---------- main ----------

function main(): void {
  const args = parseArgs(process.argv.slice(2))
  const t = TARGETS[args.target]
  args.panel = args.panel ?? t.panelDefault

  const stage = resolve(ROOT, '.deploy-staging')
  rmSync(stage, { recursive: true, force: true })
  for (const d of [`${stage}/bin`, `${stage}/panel`, `${stage}/etc`, `${stage}/docs`]) {
    mkdirSync(d, { recursive: true })
  }

  console.log(`[deploy] target=${args.target} panel=${args.panel} dryRun=${args.dryRun}`)
  goBuild(t, `${stage}/bin`)
  buildWebDist(`${stage}/panel`)

  // systemd unit + 示例配置（已存在则不覆盖远端，rsync --ignore-existing）。
  for (const [name, content] of systemdUnits(t, args.panel)) {
    writeFileSync(join(stage, `${name}`), content)
  }
  if (!existsSync(join(stage, 'etc/config.yaml'))) {
    writeFileSync(
      join(stage, 'etc/config.yaml.example'),
      `storage:\n  dataDir: ${t.remoteDir}/data\n  stateDir: ${t.remoteDir}/var/state\n  pluginBinDir: ${t.remoteDir}/bin\ntasks: []\n`,
    )
    writeFileSync(
      join(stage, 'etc/panel.json.example'),
      JSON.stringify(
        { port: 8080, bind: '0.0.0.0', socket: `${t.remoteDir}/var/edge-syncd.sock`, dataDir: `${t.remoteDir}/data` },
        null,
        2,
      ) + '\n',
    )
  }
  for (const f of ['DESIGN.md', 'acceptance.md']) {
    if (existsSync(resolve(ROOT, 'docs', f))) {
      execSync(`cp ${JSON.stringify(resolve(ROOT, 'docs', f))} ${JSON.stringify(join(stage, 'docs', f))}`)
    }
  }

  const bins = readdirSync(join(stage, 'bin'))
  console.log(`[deploy] staged: ${bins.join(', ')} + panel dist + units`)

  if (args.dryRun) {
    console.log('[deploy] dry-run：仅构建，跳过 rsync/远端操作')
    return
  }
  if (!args.host) die('需要 --host user@host（或用 --dry-run 仅构建）')

  // rsync（排除旧 bin 之外全部替换；etc 不覆盖已有配置）。
  sh(
    `rsync -avz --delete ${JSON.stringify(stage + '/')} ` +
      `--exclude 'etc/config.yaml' --exclude 'etc/panel.json' --exclude 'data' --exclude 'var' ` +
      `${JSON.stringify(args.host + ':' + t.remoteDir + '/')}`,
  )
  // 首次部署：示例配置落到 etc（远端已有则跳过）。
  sh(
    `ssh ${JSON.stringify(args.host)} ` +
      `'mkdir -p ${t.remoteDir}/{etc,var/state,data}; ` +
      `[ -f ${t.remoteDir}/etc/config.yaml ] || cp ${t.remoteDir}/etc/config.yaml.example ${t.remoteDir}/etc/config.yaml; ` +
      `[ -f ${t.remoteDir}/etc/panel.json ] || cp ${t.remoteDir}/etc/panel.json.example ${t.remoteDir}/etc/panel.json; true'`,
  )
  sh(`ssh ${JSON.stringify(args.host)} 'sudo cp ${t.remoteDir}/edge-syncd.service ${t.remoteDir}/edge-panel.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable --now edge-syncd${args.panel === 'always' ? ' edge-panel' : ''}'`)
  // 冒烟。
  sh(`ssh ${JSON.stringify(args.host)} 'systemctl is-active edge-syncd && systemctl is-active edge-panel 2>/dev/null; curl -s -o /dev/null -w "panel http: %{http_code}\\n" http://127.0.0.1:8080/ 2>/dev/null || true'`)
  console.log('[deploy] 完成')
}

main()
