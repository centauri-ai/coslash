import assert from "node:assert/strict"
import { readFileSync, mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { stripTypeScriptTypes } from "node:module"

const home = mkdtempSync(path.join(tmpdir(), "coslash-pi-extension-"))
process.env.COSLASH_HOME = home
delete process.env.COSLASH_PI_HANDOFF_FILE
try {
  for (const [version, allowed] of [
    ["0.99.1", true], ["0.99.2", true], ["0.99.3", true], ["0.100.0", true],
    ["1.0.0", true], ["1.2.3+local.1", true], ["2.0.0", true],
    ["0.98.0", false], ["0.99.0", false], ["1.0.0-rc.1", false],
    ["", false], ["unknown", false], ["1.0", false], ["01.0.0", false],
  ]) {
    const source = readFileSync(new URL("./coslash-extension.ts", import.meta.url), "utf8")
      .replace(/function processIdentity\(\)[\s\S]*?\n}/, 'let calls = 0; export const identityCalls = () => calls; function processIdentity() { calls++; return "identity" }')
      .replace(/import \{[^}]+\} from "@earendil-works\/pi-coding-agent"/, `const VERSION = ${JSON.stringify(version)}; const getPackageDir = () => home`)
    const extension = await import(`data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(source)).toString("base64")}`)
    const handlers = new Map()
    extension.default({ on: (name, handler) => handlers.set(name, handler) })
    assert.equal(extension.identityCalls(), 0, "import and registration must not probe process identity")
    if (!allowed || process.platform !== "darwin") {
      assert.equal(handlers.size, 0)
      continue
    }
    assert.ok(handlers.has("agent_settled"), `missing settlement handler on ${version}`)
    assert.ok(handlers.has("ui_prompt_start"), `missing waiting handler on ${version}`)
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
