// managed by coSlash; changes are overwritten
import { chmod, mkdir, rename, unlink, writeFile } from "node:fs/promises"
import os from "node:os"
import path from "node:path"

const directory = path.join(os.homedir(), ".coslash", "opencode-permissions")
const clientDirectory = path.join(os.homedir(), ".coslash", "opencode-clients")
const requestsBySession = new Map()
let lifecycle = Promise.resolve()

const clientEvents = new Set([
  // OpenCode v1 events.
  "session.created",
  "session.updated",
  "message.updated",
  // OpenCode v2 events that identify a session as soon as it is opened or run.
  "session.viewed",
  "session.execution.started",
  "session.inbox.enqueued",
])

const settledEvents = new Set([
  "session.idle",
  "session.execution.succeeded",
  "session.execution.failed",
  "session.execution.interrupted",
])

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
  await writeFile(temporary, JSON.stringify({ sessionID: properties.sessionID, pid: process.pid }), { mode: 0o600 })
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
    [...(requests ?? [])].map((requestID) => unlink(path.join(directory, `${requestID}.json`)).catch(() => {})),
  )
}

function enqueue(event) {
  lifecycle = lifecycle
    .then(async () => {
      // v1 calls these fields properties; v2 calls them data.
      const properties = event?.data ?? event?.properties ?? {}
      if (clientEvents.has(event?.type)) await recordClient(properties)
      if (event?.type === "session.deleted") await clearClient(properties)
      if (event?.type === "permission.asked") await record(properties)
      if (event?.type === "permission.replied") await clear(properties)
      if (settledEvents.has(event?.type)) await clearSession(properties)
    })
    .catch(() => {
      // Status reporting must never interrupt OpenCode.
    })
  return lifecycle
}

// OpenCode v1 compatibility. v2 ignores named exports and decodes the default
// plugin definition below.
export const CoslashPlugin = async () => ({
  event: ({ event }) => enqueue(event),
})

async function setupV2({ event }) {
  const controller = new AbortController()
  const worker = (async () => {
    for await (const item of event.subscribe({ signal: controller.signal })) {
      await enqueue(item)
    }
  })().catch(() => {
    // Status reporting must never interrupt OpenCode.
  })
  return async () => {
    controller.abort()
    await worker
  }
}

// OpenCode v2's default definition is appended only when coSlash detects a v2
// CLI. Older v1 loaders call every export and reject a non-function default.
