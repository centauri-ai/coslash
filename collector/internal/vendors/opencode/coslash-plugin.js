// managed by coSlash; changes are overwritten
import { chmod, mkdir, rename, unlink, writeFile } from "node:fs/promises"
import os from "node:os"
import path from "node:path"

const directory = path.join(os.homedir(), ".coslash", "opencode-permissions")
const clientDirectory = path.join(os.homedir(), ".coslash", "opencode-clients")
const requestsBySession = new Map()
let lifecycle = Promise.resolve()

async function recordClient(properties) {
  if (typeof properties.sessionID !== "string") return
  const client = process.env.OPENCODE_CLIENT === "desktop" ? "desktop" : "cli"
  await mkdir(clientDirectory, { recursive: true, mode: 0o700 })
  await chmod(clientDirectory, 0o700)
  const target = path.join(clientDirectory, `${properties.sessionID}.json`)
  const temporary = `${target}.${process.pid}.tmp`
  await writeFile(temporary, JSON.stringify({ sessionID: properties.sessionID, client }), { mode: 0o600 })
  await rename(temporary, target)
}

async function clearClient(properties) {
  if (typeof properties.sessionID !== "string") return
  await unlink(path.join(clientDirectory, `${properties.sessionID}.json`)).catch(() => {})
}

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
  const requests = requestsBySession.get(properties.sessionID) ?? new Set()
  requests.add(properties.id)
  requestsBySession.set(properties.sessionID, requests)
}

async function clear(properties) {
  if (typeof properties.requestID !== "string") return
  await unlink(path.join(directory, `${properties.requestID}.json`)).catch(() => {})
  requestsBySession.get(properties.sessionID)?.delete(properties.requestID)
}

async function clearSession(properties) {
  if (typeof properties.sessionID !== "string") return
  const requests = requestsBySession.get(properties.sessionID)
  requestsBySession.delete(properties.sessionID)
  await Promise.all(
    [...(requests ?? [])].map((requestID) =>
      unlink(path.join(directory, `${requestID}.json`)).catch(() => {}),
    ),
  )
}

export const CoslashPlugin = async () => ({
  event: ({ event }) => {
    lifecycle = lifecycle.then(async () => {
      if (["session.created", "session.updated", "message.updated"].includes(event?.type)) {
        await recordClient(event.properties ?? {})
      }
      if (event?.type === "session.deleted") await clearClient(event.properties ?? {})
      if (event?.type === "permission.asked") await record(event.properties ?? {})
      if (event?.type === "permission.replied") await clear(event.properties ?? {})
      if (event?.type === "session.idle") await clearSession(event.properties ?? {})
    }).catch(() => {
      // Status reporting must never interrupt OpenCode.
    })
    return lifecycle
  },
})
