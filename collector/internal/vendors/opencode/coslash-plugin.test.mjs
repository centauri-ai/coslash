import assert from "node:assert/strict"
import { access, mkdtemp, readFile, writeFile } from "node:fs/promises"
import os from "node:os"
import path from "node:path"
import { pathToFileURL } from "node:url"

const sourcePath = new URL("./coslash-plugin.js", import.meta.url)
const source = await readFile(sourcePath, "utf8")
const home = await mkdtemp(path.join(os.tmpdir(), "coslash-opencode-plugin-"))
process.env.HOME = home
process.env.USERPROFILE = home

async function load(name, contents) {
  const target = path.join(home, name)
  await writeFile(target, contents)
  return import(`${pathToFileURL(target).href}?test=${name}`)
}

async function exists(target) {
  try {
    await access(target)
    return true
  } catch {
    return false
  }
}

// OpenCode v1 calls every module export as a plugin factory.
const v1 = await load("coslash-plugin-v1.mjs", source)
const exports = [...new Set(Object.values(v1))]
assert.ok(exports.length > 0)
assert.ok(exports.every((value) => typeof value === "function"))
const hooks = await exports[0]()
const v1Session = "v1-session"
const v1Client = path.join(home, ".coslash", "opencode-clients", `${v1Session}.json`)
await hooks.event({
  event: { type: "session.created", properties: { sessionID: v1Session } },
})
assert.equal(JSON.parse(await readFile(v1Client, "utf8")).client, "cli")
await hooks.event({
  event: { type: "session.deleted", properties: { sessionID: v1Session } },
})
assert.equal(await exists(v1Client), false)

// OpenCode v2 decodes a default definition and streams events into setup().
const v2 = await load("coslash-plugin-v2.mjs", `${source}\nexport default { id: "coslash", setup: setupV2 }\n`)
assert.equal(v2.default.id, "coslash")
assert.equal(typeof v2.default.setup, "function")

const queued = []
let wake
let acknowledge
function delivery(item) {
  acknowledge = item.acknowledge
  return { value: item.value, done: false }
}
const event = {
  subscribe({ signal }) {
    return {
      [Symbol.asyncIterator]() {
        return this
      },
      next() {
        acknowledge?.()
        acknowledge = undefined
        if (queued.length > 0) return Promise.resolve(delivery(queued.shift()))
        if (signal.aborted) return Promise.resolve({ done: true })
        return new Promise((resolve) => {
          wake = (item) => resolve(delivery(item))
          signal.addEventListener("abort", () => resolve({ done: true }), {
            once: true,
          })
        })
      },
    }
  },
}
function emit(value) {
  return new Promise((resolve) => {
    const item = { value, acknowledge: resolve }
    if (wake) {
      const deliver = wake
      wake = undefined
      deliver(item)
    } else {
      queued.push(item)
    }
  })
}

const cleanup = await v2.default.setup({ event })
const v2Session = "v2-session"
const requestID = "permission-request"
const v2Client = path.join(home, ".coslash", "opencode-clients", `${v2Session}.json`)
const permission = path.join(home, ".coslash", "opencode-permissions", `${requestID}.json`)

await emit({ type: "session.viewed", data: { sessionID: v2Session } })
assert.equal(await exists(v2Client), true)
await emit({
  type: "permission.asked",
  data: { id: requestID, sessionID: v2Session },
})
assert.equal(await exists(permission), true)
await emit({ type: "session.execution.succeeded", data: { sessionID: v2Session } })
assert.equal(await exists(permission), false)
await emit({ type: "session.deleted", data: { sessionID: v2Session } })
assert.equal(await exists(v2Client), false)
await cleanup()
