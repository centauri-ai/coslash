import assert from "node:assert/strict"
import { readFileSync, mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { stripTypeScriptTypes } from "node:module"

const home = mkdtempSync(path.join(tmpdir(), "coslash-pi-extension-"))
process.env.COSLASH_HOME = home
delete process.env.COSLASH_PI_HANDOFF_FILE
try {
  for (const version of ["0.99.1", "0.99.2", "unsupported"]) {
    const source = readFileSync(new URL("./coslash-extension.ts", import.meta.url), "utf8")
      .replace(/function processIdentity\(\)[\s\S]*?\n}/, 'let calls = 0; export const identityCalls = () => calls; function processIdentity() { calls++; return "identity" }')
      .replace(/import \{[^}]+\} from "@earendil-works\/pi-coding-agent"/, `const VERSION = ${JSON.stringify(version)}; const getPackageDir = () => home`)
    const extension = await import(`data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(source)).toString("base64")}`)
    const handlers = new Map()
    extension.default({ on: (name, handler) => handlers.set(name, handler) })
    assert.equal(extension.identityCalls(), 0, "import and registration must not probe process identity")
    if (version === "unsupported") {
      assert.equal(handlers.size, 0)
      continue
    }
    const ctx = { isIdle: () => true, sessionManager: { getSessionId: () => "test", getSessionFile: () => path.join(home, "session.jsonl"), getLeafId: () => null } }
    handlers.get("session_start")({ reason: "startup" }, ctx)
    assert.equal(extension.identityCalls(), 1)
    handlers.get("session_start")({ reason: "reload" }, ctx)
    assert.equal(extension.identityCalls(), 1, "reuse process identity")
    handlers.get("session_shutdown")()
  }
} finally {
  rmSync(home, { recursive: true, force: true })
}
console.log("PASS Pi identity lookup waits until supported runtime tracking starts")
