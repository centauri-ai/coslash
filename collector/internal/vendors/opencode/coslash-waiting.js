// managed by coSlash; changes are overwritten
import { chmod, mkdir, rename, unlink, writeFile } from "node:fs/promises"
import os from "node:os"
import path from "node:path"

const directory = path.join(os.homedir(), ".coslash", "opencode-permissions")

async function record(properties) {
  if (typeof properties.id !== "string" || typeof properties.sessionID !== "string") return
  await mkdir(directory, { recursive: true, mode: 0o700 })
  await chmod(directory, 0o700)
  const target = path.join(directory, `${properties.id}.json`)
  const temporary = `${target}.${process.pid}.tmp`
  await writeFile(
    temporary,
    JSON.stringify({ sessionID: properties.sessionID, pid: process.pid }),
    { mode: 0o600 },
  )
  await rename(temporary, target)
}

async function clear(properties) {
  if (typeof properties.requestID !== "string") return
  await unlink(path.join(directory, `${properties.requestID}.json`)).catch(() => {})
}

export const CoslashWaitingPlugin = async () => ({
  event: async ({ event }) => {
    try {
      if (event?.type === "permission.asked") await record(event.properties ?? {})
      if (event?.type === "permission.replied") await clear(event.properties ?? {})
    } catch {
      // Status reporting must never interrupt OpenCode.
    }
  },
})
