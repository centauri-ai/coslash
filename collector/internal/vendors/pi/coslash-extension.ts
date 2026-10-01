// managed by coSlash; changes are overwritten
import { chmodSync, mkdirSync, readFileSync, renameSync, realpathSync, unlinkSync, writeFileSync } from "node:fs"
import { execFileSync } from "node:child_process"
import { randomUUID, createHash } from "node:crypto"
import { getPackageDir, VERSION } from "@earendil-works/pi-coding-agent"
import os from "node:os"
import path from "node:path"

const supportedPlatform = process.platform === "darwin"
const home = process.env.COSLASH_HOME || path.join(os.homedir(), ".coslash")
const runtimeId = randomUUID()
const startedAtMs = Date.now()
const nativeHost = (() => {
  if (!supportedPlatform) return false
  try {
    const directory = getPackageDir()
    const pkg = JSON.parse(readFileSync(path.join(directory, "package.json"), "utf8"))
    if (pkg.name !== "@earendil-works/pi-coding-agent") return false
    const bins = typeof pkg.bin === "string" ? [pkg.bin] : Object.values(pkg.bin || {})
    const candidates = [...bins, "dist/cli.js", "dist/bundle/rpc-entry.js", "dist/rpc-entry.js"]
    const host = realpathSync(process.argv[1])
    return candidates.some(candidate => {
      try { return typeof candidate === "string" && realpathSync(path.join(directory, candidate)) === host }
      catch { return false }
    })
  } catch { return false }
})()
const declaredSDK = process.env.COSLASH_PI_ENTRYPOINT === "pi-sdk"
function entrypoint() {
  if (!nativeHost) return declaredSDK ? "pi-sdk" : undefined
  return ["tui", "rpc", "json", "print"].includes(context.mode) ? `pi-${context.mode}` : undefined
}
function processIdentity() {
  try {
    if (process.platform === "linux") {
      const stat = readFileSync(`/proc/${process.pid}/stat`, "utf8")
      return `linux:${readFileSync("/proc/sys/kernel/random/boot_id", "utf8").trim()}:${stat.slice(stat.lastIndexOf(")") + 2).trim().split(/\s+/)[19]}`
    }
    if (process.platform === "win32") {
      const ticks = execFileSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", `(Get-Process -Id ${process.pid} -ErrorAction Stop).StartTime.ToUniversalTime().ToFileTimeUtc().ToString([System.Globalization.CultureInfo]::InvariantCulture)`], { encoding: "utf8", timeout: 5000 }).trim()
      return /^\d+$/.test(ticks) ? `windows:${ticks}` : ""
    }
    return `ps:${execFileSync("ps", ["-p", String(process.pid), "-o", "lstart="], { encoding: "utf8", env: { ...process.env, LC_ALL: "C" } }).trim().replace(/\s+/g, " ")}`
  } catch { return "" }
}
let processStartIdentity: string | undefined
let sequence = 0
let context: any
let dialogOpen = false
let agentRunning = false
let active = false
let lastRecord: any
let timer: ReturnType<typeof setInterval> | undefined
function atomic(directory: string, name: string, value: any) {
  mkdirSync(directory, { recursive: true, mode: 0o700 })
  chmodSync(directory, 0o700)
  const target = path.join(directory, name + ".json")
  const temporary = target + ".tmp"
  writeFileSync(temporary, JSON.stringify(value), { mode: 0o600 })
  renameSync(temporary, target)
}
function history(record: any, exited: boolean) {
  const key = createHash("sha256").update(runtimeId + "\0" + record.transcriptPath).digest("hex")
  atomic(path.join(home, "pi-history"), key, { record, exited })
}
function publish() {
  if (!active || !context) return
  try {
    const transcriptPath = context.sessionManager.getSessionFile()
    const sessionId = context.sessionManager.getSessionId()
    if (!transcriptPath || !path.isAbsolute(transcriptPath) || !sessionId) return
    const leafId = context.sessionManager.getLeafId() ?? null
    const modality = entrypoint()
    const workState = agentRunning || !context.isIdle() ? "busy" : "idle"
    if (lastRecord && lastRecord.sessionId === sessionId && lastRecord.transcriptPath === transcriptPath && lastRecord.leafId === leafId && lastRecord.workState === workState && lastRecord.dialogOpen === dialogOpen && lastRecord.entrypoint === modality) return
    const record = { version: 1, runtimeId, pid: process.pid, processStartIdentity, startedAtMs,
      sessionId, transcriptPath, leafId, workState, dialogOpen, entrypoint: modality,
      sequence: ++sequence, updatedAtMs: Date.now() }
    atomic(path.join(home, "pi-runtime"), runtimeId, record)
    history(record, false)
    lastRecord = record
  } catch { /* Reporting must never interrupt Pi. */ }
}
function clear() {
  active = false
  if (timer) clearInterval(timer)
  timer = undefined
  try {
    if (lastRecord) history(lastRecord, true)
    lastRecord = undefined
    unlinkSync(path.join(home, "pi-runtime", runtimeId + ".json"))
  } catch { /* Another owner's claim is never touched. */ }
}
export default function (pi: any) {
  if (!supportedPlatform) return
  // Event semantics have been verified on this release only.
  if (VERSION !== "0.99.1" && VERSION !== "0.99.2") return
  const ready = process.env.COSLASH_PI_READY
  delete process.env.COSLASH_PI_READY
  const handoffPath = process.env.COSLASH_PI_HANDOFF_FILE
  // Replacement runtimes must not consume the initial session's notes again.
  const handoff = handoffPath ? readFileSync(handoffPath, "utf8") : ""
  delete process.env.COSLASH_PI_HANDOFF_FILE
  let handoffSession: string | undefined
  if (handoff) pi.on("before_agent_start", (event: any, ctx: any) => {
    if (ctx.sessionManager.getSessionId() === handoffSession) return { systemPrompt: event.systemPrompt + "\n\n" + handoff }
  })
  const update = (_event: any, ctx: any) => { context = ctx; publish() }
  pi.on("session_start", (event: any, ctx: any) => {
    processStartIdentity ??= processIdentity()
    handoffSession = event.reason === "startup" ? ctx.sessionManager.getSessionId() : undefined
    // Pi installs its editor submit handler before emitting startup session_start.
    if (event.reason === "startup" && ctx.mode === "tui" && ready && /^[a-zA-Z0-9._-]+$/.test(ready)) process.stdout.write(`\x1b]777;coslash-ready=${ready}\x07`)
    context = ctx; active = true; dialogOpen = false; agentRunning = false
    publish()
    if (!timer) {
      // Operation callbacks precede Pi's idle flags settling. Refresh afterward.
      timer = setInterval(publish, 250)
      timer.unref()
    }
  })
  pi.on("agent_start", (_event: any, ctx: any) => { agentRunning = true; context = ctx; publish() })
  pi.on("agent_end", update)
  pi.on("agent_settled", (_event: any, ctx: any) => { agentRunning = false; context = ctx; publish() })
  pi.on("ui_prompt_start", (_event: any, ctx: any) => { dialogOpen = true; context = ctx; publish() })
  pi.on("ui_prompt_end", (_event: any, ctx: any) => { dialogOpen = false; context = ctx; publish() })
  for (const event of ["session_before_compact", "session_compact", "session_compact_failed", "session_before_tree", "session_tree"]) pi.on(event, update)
  pi.on("session_shutdown", clear)
}
