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
  return import(`${pathToFileURL(target).href}?test=${Date.now()}-${name}`)
}

async function exists(target) {
  try {
    await access(target)
    return true
  } catch {
    return false
  }
}

async function waitFor(predicate) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (await predicate()) return
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  assert.fail("timed out waiting for plugin lifecycle work")
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
const event = {
  subscribe({ signal }) {
    return {
      [Symbol.asyncIterator]() {
        return this
      },
      next() {
        if (queued.length > 0) return Promise.resolve({ value: queued.shift(), done: false })
        if (signal.aborted) return Promise.resolve({ done: true })
        return new Promise((resolve) => {
          wake = resolve
          signal.addEventListener("abort", () => resolve({ done: true }), {
            once: true,
          })
        })
      },
    }
  },
}
function emit(value) {
  if (wake) {
    const resolve = wake
    wake = undefined
    resolve({ value, done: false })
  } else {
    queued.push(value)
  }
}

const cleanup = await v2.default.setup({ event })
const v2Session = "v2-session"
const requestID = "permission-request"
const v2Client = path.join(home, ".coslash", "opencode-clients", `${v2Session}.json`)
const permission = path.join(home, ".coslash", "opencode-permissions", `${requestID}.json`)

emit({ type: "session.viewed", data: { sessionID: v2Session } })
await waitFor(() => exists(v2Client))
emit({
  type: "permission.asked",
  data: { id: requestID, sessionID: v2Session },
})
await waitFor(() => exists(permission))
emit({ type: "session.execution.succeeded", data: { sessionID: v2Session } })
await waitFor(async () => !(await exists(permission)))
emit({ type: "session.deleted", data: { sessionID: v2Session } })
await waitFor(async () => !(await exists(v2Client)))
await cleanup()
