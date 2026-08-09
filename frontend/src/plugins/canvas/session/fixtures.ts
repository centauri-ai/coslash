import type { SessionCanvasDetail } from '@/plugins/canvas/session/types';

export function sessionCanvasFixture(agent = 'claude', id = 'shared-id'): SessionCanvasDetail {
  return {
    agent,
    id,
    name: `${agent} migration session`,
    summary: 'The migration is implemented and awaiting final verification.',
    status: 'waiting',
    cwd: '/workspace/coslash',
    branch: 'feature/canvas',
    repo: 'coslash',
    files: 2,
    durationMs: 42_000,
    tokens: {
      model: {
        input_tokens: 1_000,
        output_tokens: 400,
        cache_creation_input_tokens: 0,
        cache_creation_1h_input_tokens: 0,
        cache_read_input_tokens: 200,
      },
    },
    subagents: [],
    mtime: 1_754_694_400_000,
    entrypoint: 'cli',
    synthesis: {
      goals: ['Port Session Canvas'],
      outcome: 'The guarded, server-backed workbench is ready.',
      keyDecisions: ['Keep composite session identity'],
      nextStep: 'Run integration verification',
    },
    synthesisPending: false,
    declaredGoal: 'Port Session Canvas without regressing Log.',
    logPath: '/private/session.jsonl',
    model: 'model',
    contextTokens: 80_000,
    contextWindow: 100_000,
    turns: 2,
    toolUses: 4,
    errors: 1,
    compactions: 0,
    firstPrompt: 'Port the Session Canvas into the coSlash plugin.',
    commandCount: 2,
    commands: ['go test ./...', 'npm test'],
    commits: ['abc1234'],
    prs: 0,
    todos: [
      { text: 'Implement workbench', done: true },
      { text: 'Run browser verification', done: false },
    ],
    digest: [
      { turn: 1, category: 'first_prompt', description: 'Port Session Canvas' },
      { turn: 2, category: 'recap', description: 'Implementation ready' },
    ],
    fileEdits: [
      {
        path: 'frontend/src/plugins/canvas/session/SessionCanvas.tsx',
        adds: 80,
        dels: 0,
        edits: 1,
        isNew: true,
        hunks: ['@@ -0,0 +1,80 @@'],
      },
    ],
    git: { baseBranch: 'main', ahead: 1, behind: 0 },
    lastEditAt: 1_754_694_400_000,
    turnLog: [
      {
        index: 1,
        prompt: 'Port Session Canvas',
        planText: 'Inspect and implement',
        todos: [],
        toolUses: 2,
        errors: 0,
        decisions: [],
        fileEdits: [],
      },
      {
        index: 2,
        prompt: 'Verify it',
        planText: 'Run tests',
        todos: [],
        toolUses: 2,
        errors: 1,
        decisions: [{ question: 'Use composite identity?', answer: 'Yes' }],
        fileEdits: ['SessionCanvas.tsx'],
      },
    ],
    contextFiles: [
      {
        path: 'frontend/src/plugins/canvas/contracts.ts',
        partial: false,
        totalLines: 40,
        capturedContent: true,
        combinedReadId: null,
        segments: [{ startLine: 1, content: 'export type CanvasSessionIdentity' }],
      },
    ],
    contextReadGroups: [],
    deferredContext: [],
    triggeredContext: [{ kind: 'tool', name: 'functions.exec', calls: 3 }],
  };
}

export const FROZEN_SESSION_CANVAS_FIXTURES = {
  claude: sessionCanvasFixture('claude', 'shared-id'),
  codex: sessionCanvasFixture('codex', 'shared-id'),
} as const;
