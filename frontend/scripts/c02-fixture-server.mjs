import { createReadStream, existsSync, statSync } from 'node:fs';
import { createServer } from 'node:http';
import { dirname, extname, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const distributionDirectory = resolve(scriptDirectory, '..', 'dist');
const argumentsByName = new Map(
  process.argv.slice(2).map((argument) => {
    const [name, ...value] = argument.replace(/^--/, '').split('=');
    return [name, value.join('=')];
  }),
);
const port = Number(argumentsByName.get('port') || 4174);
const diffError = argumentsByName.get('diff-error') || 'none';
const allowedDiffErrors = new Set(['none', 'stale', 'missing', 'corrupt']);

if (!Number.isInteger(port) || port < 1 || port > 65_535) {
  throw new Error('Use --port=<1-65535>.');
}
if (!allowedDiffErrors.has(diffError)) {
  throw new Error('Use --diff-error=none|stale|missing|corrupt.');
}
if (!existsSync(resolve(distributionDirectory, 'index.html'))) {
  throw new Error('Build the frontend first with `npm run build`.');
}

const now = Date.now();
const remoteSource = 'r_0123456789abcdef';
const exactRevision = 'a'.repeat(64);

function session(sourceId, id, detailRevision, overrides = {}) {
  const remote = sourceId !== 'local';
  return {
    sourceId,
    sourceLabel: remote ? 'SSH fixture' : 'Local Mac',
    sourceClass: remote ? 'ssh_workspace' : 'local',
    logicalSessionId: `${sourceId}:codex:${id}`,
    revision: now,
    detailRevision,
    completion: detailRevision === '' ? 'incomplete' : 'complete',
    privacy: 'shareable',
    shareEligibility: detailRevision === '' ? 'incomplete' : 'eligible',
    eligibleForAggregates: detailRevision !== '',
    displayStale: remote,
    launchable: false,
    launchBlockReason: 'missing_details',
    agent: 'codex',
    id,
    name: remote ? 'Remote parity fixture' : 'Local parity fixture',
    summary: 'Inspect the exact-detail and bounded-summary acceptance paths.',
    status: null,
    cwd: '/workspace/coslash',
    branch: 'feature/c02',
    repo: 'coslash',
    repoLocalOnly: false,
    files: 1,
    durationMs: 180_000,
    tokens: {
      'gpt-5': {
        input_tokens: 1200,
        output_tokens: 300,
        cache_creation_input_tokens: 0,
        cache_creation_1h_input_tokens: 0,
        cache_read_input_tokens: 0,
        cost: 0.01,
      },
    },
    cost: 0.01,
    unpricedModels: [],
    subagents: [],
    mtime: now,
    entrypoint: 'codex',
    synthesis: null,
    synthesisPending: false,
    declaredGoal: 'Close the C02 review gaps.',
    model: 'gpt-5',
    contextTokens: 1500,
    contextWindow: 128_000,
    turns: 4,
    toolUses: 7,
    errors: 0,
    compactions: 0,
    firstPrompt: 'Fix exact detail parity.',
    commands: ['npm test -- --run'],
    commits: ['fixture C02 acceptance'],
    prs: 0,
    todos: [{ text: 'Re-review C02', done: false }],
    digest: [],
    fileEdits: [
      {
        path: 'frontend/src/example.ts',
        adds: 1,
        dels: 1,
        edits: 1,
        isNew: false,
        changeIds: ['change-000000-000000'],
      },
    ],
    git: null,
    lastEditAt: now - 60_000,
    ...overrides,
  };
}

const localSession = session('local', 'same-session', String(now), {
  launchable: true,
  displayStale: false,
});
const remoteSession = session(remoteSource, 'same-session', exactRevision);
const summarySession = session(remoteSource, 'summary-only', '', {
  name: 'Remote bounded summary',
  summary: 'Complete cached detail is unavailable, but this library summary remains inspectable.',
  commands: [],
  fileEdits: [],
});

const settingsResponse = {
  settings: {
    $schema: 'https://example.invalid/coslash-settings.schema.json',
    version: 1,
    synthesis: { enabled: false, backend: '', model: '' },
    appearance: { theme: 'light' },
    launch: { terminal: 'terminal' },
    remote: { id: remoteSource, sshAlias: 'fixture-host', enabled: true },
  },
  persisted: true,
  valid: true,
  options: { synthesisBackends: [], terminals: [] },
};

function sendJSON(response, status, value) {
  const body = JSON.stringify(value);
  response.writeHead(status, {
    'Content-Type': 'application/json',
    'Content-Length': Buffer.byteLength(body),
    'Cache-Control': 'no-store',
  });
  response.end(body);
}

function serveAPI(request, response, url) {
  if (url.pathname === '/api/settings') {
    sendJSON(response, 200, settingsResponse);
    return;
  }
  if (url.pathname === '/api/hub/destination') {
    sendJSON(response, 200, { configured: false, state: 'signed_out', contractVersion: 'hub-share/v1' });
    return;
  }
  if (url.pathname === '/api/sessions') {
    sendJSON(response, 200, {
      sessions: [localSession, remoteSession, summarySession],
      machines: [
        { sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true },
        {
          sourceId: remoteSource,
          label: 'SSH fixture',
          state: 'stale',
          complete: true,
          lastSuccessAtMs: now - 60_000,
          sessionCount: 2,
          transport: 'sftp',
        },
      ],
    });
    return;
  }
  if (url.pathname === '/api/session-detail') {
    const sourceId = url.searchParams.get('source');
    const selected = sourceId === 'local' ? localSession : remoteSession;
    sendJSON(response, 200, {
      sourceId,
      agent: selected.agent,
      sessionId: selected.id,
      revision: selected.detailRevision,
      cachedOffline: sourceId !== 'local',
      session: selected,
    });
    return;
  }
  if (url.pathname === '/api/diff') {
    const failures = {
      stale: [409, 'session_detail_stale', 'revision changed'],
      missing: [404, 'session_change_missing', 'change missing'],
      corrupt: [500, 'session_detail_corrupt', 'cached detail corrupt'],
    };
    const failure = failures[diffError];
    if (failure != null) {
      sendJSON(response, failure[0], { code: failure[1], error: failure[2] });
    } else {
      sendJSON(response, 200, {
        changes: [
          {
            kind: 'diff',
            text: '@@ -1 +1 @@\n-old fixture\n+new fixture',
            operation: 'update',
            additions: 1,
            deletions: 1,
          },
        ],
      });
    }
    return;
  }
  if (url.pathname === '/api/synthesis') {
    sendJSON(response, 200, { synthesis: null, synthesisPending: false });
    return;
  }
  if (url.pathname === '/api/remote/retry') {
    sendJSON(response, 202, { sourceId: remoteSource, state: 'stale' });
    return;
  }
  if (url.pathname === '/api/remote/status') {
    sendJSON(response, 200, { sourceId: remoteSource, label: 'SSH fixture', state: 'stale', complete: true });
    return;
  }
  sendJSON(response, 404, { code: 'fixture_not_found', error: `No fixture for ${url.pathname}` });
}

const contentTypes = {
  '.css': 'text/css; charset=utf-8',
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.woff2': 'font/woff2',
};

function serveStatic(response, pathname) {
  const requested = pathname === '/' ? '/index.html' : decodeURIComponent(pathname);
  let target = resolve(distributionDirectory, `.${requested}`);
  if (target !== distributionDirectory && !target.startsWith(`${distributionDirectory}${sep}`)) {
    response.writeHead(400).end('invalid path');
    return;
  }
  if (!existsSync(target) || !statSync(target).isFile()) {
    target = resolve(distributionDirectory, 'index.html');
  }
  const stats = statSync(target);
  response.writeHead(200, {
    'Content-Type': contentTypes[extname(target)] || 'application/octet-stream',
    'Content-Length': stats.size,
    'Cache-Control': 'no-store',
  });
  createReadStream(target).pipe(response);
}

const server = createServer((request, response) => {
  const url = new URL(request.url || '/', `http://${request.headers.host || '127.0.0.1'}`);
  if (url.pathname.startsWith('/api/')) serveAPI(request, response, url);
  else serveStatic(response, url.pathname);
});

server.listen(port, '127.0.0.1', () => {
  console.log(`C02 fixture (${diffError}) listening at http://127.0.0.1:${port}`);
  console.log('Use the Local Mac / SSH fixture filter to distinguish the same-ID sessions.');
  console.log('Open “Remote bounded summary” to verify summary-only inspection.');
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
