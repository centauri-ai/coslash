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
const port = Number(argumentsByName.get('port') || 4185);
if (!Number.isInteger(port) || port < 1 || port > 65_535) throw new Error('Use --port=<1-65535>.');
if (!existsSync(resolve(distributionDirectory, 'index.html'))) {
  throw new Error('Build the frontend first with `npm run build`.');
}

const now = Date.now();
const sourceId = 'r_0123456789abcdef';
const sessionId = '019f4dde-db5b-7100-bdc0-09b5aaaac56f';
const revisionId = '318ff26e8f458be0ff7f7058a0411df51245475b2d9241dde8522e573ae62778';
const recordSha256 = 'sha256:617460f0d4b5b1795aea433967e89e8dc3be87b1eb82eb7e23ae4c307d001fbf';
let shareRequests = 0;
let shareValidationFailures = 0;
let lastShareIdempotencyKey = null;
const acceptedShareKeys = new Set();

const session = {
  sourceId,
  sourceLabel: 'SSH fixture',
  sourceClass: 'ssh_workspace',
  logicalSessionId: `${sourceId}:codex:${sessionId}`,
  revision: now,
  fullRevision: revisionId,
  completion: 'complete',
  privacy: 'shareable',
  shareEligibility: 'eligible',
  eligibleForAggregates: true,
  displayStale: false,
  launchable: false,
  agent: 'codex',
  id: sessionId,
  name: 'Complete C03 SSH fixture',
  summary: 'Review every included section and ordered file-change body.',
  status: null,
  cwd: '/workspace/coslash',
  branch: 'feature/full-data',
  repo: 'github.com/centauri-ai/coslash',
  repoLocalOnly: false,
  files: 1,
  durationMs: 12_000,
  tokens: {},
  cost: 0.125,
  unpricedModels: [],
  subagents: [],
  mtime: now,
  entrypoint: 'codex',
  synthesis: null,
  synthesisPending: false,
  declaredGoal: 'Ship one complete thin path.',
  model: 'gpt-5',
  contextTokens: 12_345,
  contextWindow: 272_000,
  turns: 3,
  toolUses: 4,
  errors: 0,
  compactions: 0,
  firstPrompt: 'Restore the complete session without dropping diffs.',
  commands: [],
  commits: [],
  prs: 0,
  todos: [],
  digest: [],
  fileEdits: [],
  git: null,
  lastEditAt: null,
};

const record = {
  schemaVersion: 'full-session-record/v1',
  sourceId,
  agent: 'codex',
  sessionId,
  revisionId,
  session: {
    name: session.name,
    summary: session.summary,
    status: 'inactive',
    cwd: session.cwd,
    branch: session.branch,
    editedFileCount: 1,
    durationMs: 12_000,
    usage: [{ model: 'gpt-5', inputTokens: 2_000, outputTokens: 400, costMicroUsd: 125_000 }],
    costMicroUsd: 125_000,
    unpricedModels: [],
    subagents: [
      {
        id: 'agent-1',
        name: 'verify',
        model: 'gpt-5-mini',
        status: 'returned',
        task: 'run focused tests',
        result: 'all passed',
        commands: [{ label: 'tests', command: 'go test ./...' }],
      },
    ],
    startedAtMs: 1_800_000_000_000,
    lastActivityAtMs: 1_800_000_012_000,
    entrypoint: 'codex',
    model: 'gpt-5',
    contextTokens: 12_345,
    contextWindow: 272_000,
    turns: 3,
    toolUses: 4,
    errors: 0,
    compactions: 0,
    firstPrompt: 'Restore the complete session without dropping diffs.',
    commands: ['go test ./...'],
    commits: ['feat: preserve complete SSH records'],
    commitShas: ['aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'],
    pullRequests: 0,
    todos: [{ text: 'verify restart', done: true }],
    digest: [
      {
        turn: 1,
        category: 'first_prompt',
        description: 'Restore the complete session without dropping diffs.',
        answer: '',
        subagentId: '',
        timeMs: 1_800_000_000_000,
      },
    ],
    fileEdits: [
      {
        path: 'collector/example.go',
        additions: 2,
        deletions: 1,
        edits: 2,
        isNew: false,
        changes: [
          {
            id: 'change-000000-000000',
            kind: 'diff',
            text: '@@\n-old\n+new\n',
            operation: 'Patch',
            additions: 1,
            deletions: 1,
            byteCount: 13,
            sha256: '578fb2b594049b296d357e34bd1d7714ae88e299535b0c3c831e510ce80e6a9c',
          },
          {
            id: 'change-000000-000001',
            kind: 'content',
            text: 'package example\n',
            operation: 'Write',
            additions: 1,
            deletions: 0,
            byteCount: 16,
            sha256: 'e0e0431b63a883552b05817d33ba13f79262019cdefb4e5ae060c299c1de1eb8',
          },
        ],
      },
    ],
    synthesis: {
      goals: ['Ship one complete thin path.'],
      outcome: 'complete',
      keyDecisions: ['use exact revisions'],
      nextStep: 'handoff',
    },
    synthesisPending: false,
    declaredGoal: 'Ship one complete thin path.',
  },
};
const envelope = {
  schemaVersion: 'session-revision/v2',
  mediaType: 'application/vnd.coslash.session-revision.v2+json',
  recordByteCount: 2297,
  recordSha256,
  repository: { canonical: 'github.com/centauri-ai/coslash', localOnly: false },
  record,
};

const settingsResponse = {
  settings: {
    $schema: 'https://example.invalid/coslash-settings.schema.json',
    version: 1,
    synthesis: { enabled: false, backend: '', model: '' },
    appearance: { theme: 'light' },
    launch: { terminal: 'terminal' },
    remote: { id: sourceId, sshAlias: 'fixture-host', enabled: true },
  },
  persisted: true,
  valid: true,
  options: { synthesisBackends: [], terminals: [] },
};
const destination = {
  contractVersion: 'hub-share/v1',
  configured: true,
  state: 'ready',
  hubUrl: 'https://hub.example.test',
  destination: {
    workspaceId: '10000000-0000-4000-8000-000000000001',
    workspaceName: 'Compiler Team',
    currentMemberCount: 2,
    resultingMemberCount: 2,
    currentApprovedSessionCount: 3,
    historyDisclosure: 'Current members can see approved revisions.',
    credentialState: 'paired',
    audienceVersion: 'audience-fixture-v1',
  },
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

async function readJSONRequest(request) {
  const chunks = [];
  let byteCount = 0;
  for await (const chunk of request) {
    byteCount += chunk.length;
    if (byteCount > 1_048_576) throw new Error('request exceeds fixture limit');
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

function validShareRequest(value) {
  const selection = value?.selection;
  const consent = value?.consent;
  return (
    value?.contractVersion === 'full-session-share/v1' &&
    typeof value.idempotencyKey === 'string' &&
    value.idempotencyKey.startsWith('full-session-share/v1:') &&
    value.idempotencyKey.length >= 16 &&
    value.idempotencyKey.length <= 200 &&
    selection?.sourceId === sourceId &&
    selection?.agent === 'codex' &&
    selection?.sessionId === sessionId &&
    selection?.revisionId === revisionId &&
    consent?.previewContractVersion === 'full-session-preview/v1' &&
    consent?.revisionId === revisionId &&
    consent?.recordSha256 === recordSha256 &&
    consent?.recordBytes === 2297 &&
    consent?.payloadBytes === 2599 &&
    consent?.repository?.canonical === 'github.com/centauri-ai/coslash' &&
    consent?.repository?.localOnly === false &&
    consent?.destinationWorkspaceId === destination.destination.workspaceId &&
    consent?.destinationName === destination.destination.workspaceName &&
    consent?.audienceMemberCount === destination.destination.currentMemberCount
  );
}

async function serveAPI(request, response, url) {
  if (url.pathname === '/api/settings') return sendJSON(response, 200, settingsResponse);
  if (url.pathname === '/api/hub/destination') return sendJSON(response, 200, destination);
  if (url.pathname === '/api/sessions') {
    return sendJSON(response, 200, {
      sessions: [session],
      machines: [
        { sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true },
        { sourceId, label: 'SSH fixture', state: 'ok', complete: true, sessionCount: 1 },
      ],
    });
  }
  if (url.pathname === '/api/hub/full-session-preview') {
    return sendJSON(response, 200, {
      adapterVersion: 'full-session-preview/v1',
      state: 'ready',
      approvalAllowed: true,
      selection: { sourceId, agent: 'codex', sessionId, revisionId },
      schemaVersion: envelope.schemaVersion,
      mediaType: envelope.mediaType,
      recordBytes: 2297,
      payloadBytes: 2599,
      maxRecordBytes: 1_040_384,
      recordSha256,
      embeddedSecretRisk: true,
      envelope,
    });
  }
  if (url.pathname === '/api/hub/full-session-shares' && request.method === 'POST') {
    let shareRequest;
    try {
      shareRequest = await readJSONRequest(request);
    } catch {
      shareValidationFailures += 1;
      return sendJSON(response, 400, { code: 'invalid_fixture_request' });
    }
    if (request.headers['content-type'] !== 'application/json' || !validShareRequest(shareRequest)) {
      shareValidationFailures += 1;
      return sendJSON(response, 400, { code: 'invalid_fixture_request' });
    }
    shareRequests += 1;
    const deduplicated = acceptedShareKeys.has(shareRequest.idempotencyKey);
    acceptedShareKeys.add(shareRequest.idempotencyKey);
    lastShareIdempotencyKey = shareRequest.idempotencyKey;
    return sendJSON(response, 200, {
      contractVersion: 'full-session-share/v1',
      state: deduplicated ? 'already_accepted' : 'accepted',
      selection: { sourceId, agent: 'codex', sessionId, revisionId },
      idempotencyKey: shareRequest.idempotencyKey,
      repositoryId: '44444444-4444-4444-8444-444444444444',
      byteCount: 2297,
      contentSha256: recordSha256,
      deduplicated,
      sharedAt: '2026-09-17T12:00:00Z',
      route: {
        hubContractVersion: 'full-session-read/v1',
        path: `/v2/sources/${sourceId}/agents/codex/sessions/${sessionId}/revisions/${revisionId}`,
      },
    });
  }
  if (url.pathname === '/api/c03-fixture-state') {
    return sendJSON(response, 200, {
      shareRequests,
      shareValidationFailures,
      lastShareIdempotencyKey,
      acceptedRevisionCount: acceptedShareKeys.size,
    });
  }
  return sendJSON(response, 404, { code: 'fixture_not_found', error: `No fixture for ${url.pathname}` });
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
  if (!existsSync(target) || !statSync(target).isFile())
    target = resolve(distributionDirectory, 'index.html');
  const stats = statSync(target);
  response.writeHead(200, {
    'Content-Type': contentTypes[extname(target)] || 'application/octet-stream',
    'Content-Length': stats.size,
    'Cache-Control': 'no-store',
  });
  createReadStream(target).pipe(response);
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url || '/', `http://${request.headers.host || '127.0.0.1'}`);
  try {
    if (url.pathname.startsWith('/api/')) await serveAPI(request, response, url);
    else serveStatic(response, url.pathname);
  } catch {
    if (!response.headersSent) sendJSON(response, 500, { code: 'fixture_failed' });
    else response.destroy();
  }
});

server.listen(port, '127.0.0.1', () => {
  console.log(`C03 fixture listening at http://127.0.0.1:${port}`);
  console.log('Legacy full-session endpoints are available for compatibility checks only.');
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
