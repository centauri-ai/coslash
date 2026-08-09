import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const html = readFileSync(
  new URL("./migration-control.html", import.meta.url),
  "utf8",
);

function between(start, end) {
  const from = html.indexOf(start);
  const to = html.indexOf(end, from);
  assert.notEqual(from, -1, `missing start marker: ${start}`);
  assert.notEqual(to, -1, `missing end marker: ${end}`);
  return html.slice(from, to);
}

function dashboardAPI() {
  const definitions = between("const TASK_STATES", "let selectedId");
  const validation = between("function validTaskState", "function labelFor");
  const context = vm.createContext({});
  vm.runInContext(
    `${definitions}
     let state = { tasks: {} };
     ${validation}
     globalThis.dashboard = {
       hasPassingTestEvidence,
       hasCompletionEvidence,
       sanitizeTaskRecord,
       displayTaskState(id, value) {
         state = value;
         return displayState(TASK_BY_ID[id]);
       },
       setState(value) { state = value; },
     };`,
    context,
  );
  return context.dashboard;
}

function loadSidecar(id) {
  const source = readFileSync(
    new URL(`./task-status/${id}.js`, import.meta.url),
    "utf8",
  );
  const context = vm.createContext({ window: {} });
  vm.runInContext(source, context);
  return context.window.COSLASH_CANVAS_TASK_STATUS[id];
}

function completedRecord(tests) {
  return {
    state: "complete",
    agent: "worker",
    branch: "task/branch",
    sha: "a".repeat(40),
    reviewer: "reviewer",
    review: "approved",
    tests,
  };
}

const passing = (command) => ({
  command,
  result: "passed",
  evidence: `${command} passed`,
});

test("completion requires the latest result for every recorded command", () => {
  const { hasCompletionEvidence, hasPassingTestEvidence } = dashboardAPI();
  const mixed = [passing("go test ./..."), {
    command: "npm run build",
    result: "failed",
    evidence: "exit 1",
  }];
  assert.equal(hasPassingTestEvidence(mixed), false);
  assert.equal(hasCompletionEvidence(completedRecord(mixed)), false);

  const rerun = [
    { command: "go test ./...", result: "failed", evidence: "exit 1" },
    passing("go test ./..."),
    passing("npm run build"),
  ];
  assert.equal(hasPassingTestEvidence(rerun), true);
  assert.equal(hasCompletionEvidence(completedRecord(rerun)), true);
});

test("legacy pass results normalize to the canonical passed value", () => {
  const { hasCompletionEvidence, sanitizeTaskRecord } = dashboardAPI();
  const record = completedRecord([
    { command: "go test ./...", result: "pass", evidence: "ok" },
  ]);
  const sanitized = sanitizeTaskRecord(record);
  assert.equal(sanitized.tests[0].result, "passed");
  assert.equal(hasCompletionEvidence(sanitized), true);
});

test("completed sidecars using legacy pass results remain complete", () => {
  const { hasCompletionEvidence, sanitizeTaskRecord } = dashboardAPI();
  for (const id of ["05", "08", "11"]) {
    const record = sanitizeTaskRecord(loadSidecar(id));
    assert.ok(record.tests.length > 0, `Task ${id} has test evidence`);
    assert.ok(
      record.tests.every((item) => item.result === "passed"),
      `Task ${id} results are canonicalized`,
    );
    assert.equal(
      hasCompletionEvidence(record),
      true,
      `Task ${id} remains complete`,
    );
  }
});

test("explicit non-gating failures preserve accepted completion evidence", () => {
  const { hasCompletionEvidence, sanitizeTaskRecord } = dashboardAPI();
  const record = completedRecord([
    passing("npm test"),
    {
      command: "npm run format:check",
      result: "failed",
      gating: false,
      evidence: "accepted legacy formatting backlog",
    },
  ]);
  const sanitized = sanitizeTaskRecord(record);
  assert.equal(sanitized.tests[1].gating, false);
  assert.equal(hasCompletionEvidence(sanitized), true);
  assert.equal(
    hasCompletionEvidence(completedRecord([
      passing("npm test"),
      {
        command: "npm run format:check",
        result: "failed",
        evidence: "unaccepted failure",
      },
    ])),
    false,
  );
});

test("task import/export sanitizer drops unknown task and test fields", () => {
  const { sanitizeTaskRecord } = dashboardAPI();
  const sanitized = sanitizeTaskRecord({
    state: "review",
    agent: "worker",
    token: "top-secret",
    progress: [{ token: "nested-secret" }],
    tests: [
      {
        command: "go test ./...",
        result: "passed",
        evidence: "ok",
        at: "2026-08-09T00:00:00Z",
        token: "test-secret",
      },
    ],
  });
  assert.equal(sanitized.state, "review");
  assert.equal(sanitized.agent, "worker");
  assert.equal(Object.hasOwn(sanitized, "token"), false);
  assert.equal(Object.hasOwn(sanitized, "progress"), false);
  assert.equal(Object.hasOwn(sanitized.tests[0], "token"), false);
  assert.doesNotMatch(JSON.stringify(sanitized), /secret/);
});

test("recommendations and completion use validated display state", () => {
  const { displayTaskState } = dashboardAPI();
  const invalidComplete = completedRecord([
    passing("go test ./..."),
    { command: "npm run build", result: "failed", evidence: "exit 1" },
  ]);
  assert.equal(
    displayTaskState("00", { tasks: { "00": invalidComplete } }),
    "review",
  );
  assert.match(
    html,
    /const byState = \(value\) =>\s*TASKS\.filter\(\(task\) => displayState\(task\) === value\)/,
  );
  assert.match(
    html,
    /TASKS\.every\(\(task\) => displayState\(task\) === "complete"\)/,
  );
});

test("Task 00 remains complete with its accepted baseline failure", () => {
  const record = loadSidecar("00");
  const { hasCompletionEvidence, sanitizeTaskRecord } = dashboardAPI();
  assert.equal(record.state, "complete");
  assert.equal(
    record.tests.find((item) => item.command === "npm run format:check")
      .gating,
    false,
  );
  assert.equal(hasCompletionEvidence(sanitizeTaskRecord(record)), true);
});
