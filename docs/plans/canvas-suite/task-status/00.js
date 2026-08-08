window.COSLASH_CANVAS_TASK_STATUS = window.COSLASH_CANVAS_TASK_STATUS || {};
window.COSLASH_CANVAS_TASK_STATUS["00"] = {
  schemaVersion: 1,
  taskId: "00",
  state: "blocked",
  agent: "master",
  branch: "",
  worktree: "",
  baseSha: "",
  sha: "",
  reviewer: "",
  review: "pending",
  reason:
    "Waiting for the master to publish a non-force-pushed archive ref for the exact legacy source SHA and record it in STATUS.md.",
  notes:
    "The Task 00 worker verifies the archive ref but never creates or publishes it.",
  claimedAt: "",
  startedAt: "",
  completedAt: "",
  updatedAt: "2026-08-08T21:33:36Z",
  progress: [],
  tests: [],
  issues: [],
  postImplementation: {
    remainingWork: [],
    improvements: [],
    knownIssues: [],
    followUps: [],
  },
};
