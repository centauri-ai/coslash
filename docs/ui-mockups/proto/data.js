/* coSlash prototype — shared sample dataset and helpers.
   Sessions are modelled on the live local dataset inspected 2026-09-10
   (52 sessions · 2 Active · 41 Inactive · 9 Unknown · 0 Waiting · $296.20).
   Waiting and Idle rows are marked `illustrative: true` — neither state was
   present at inspection time, but both exist in STATUSES and must be designed for. */

const STATUS = {
  busy:     { key: 'busy',     label: 'Active',   tone: 'active'  },
  waiting:  { key: 'waiting',  label: 'Waiting',  tone: 'waiting' },
  idle:     { key: 'idle',     label: 'Idle',     tone: 'idle'    },
  inactive: { key: 'inactive', label: 'Inactive', tone: 'off'     },
  unknown:  { key: 'unknown',  label: 'Unknown',  tone: 'unknown' },
};

const AGENT = {
  claude: { key: 'claude', label: 'Claude Code' },
  codex:  { key: 'codex',  label: 'Codex' },
};

const SESSIONS = [
  {
    id: '05e982d2-edb6-43c2-adec-1802656398e5', title: 'Coslash webapp design modernization',
    agent: 'claude', status: 'busy', repo: 'coslash', branch: 'milan/ui', source: 'local',
    when: 'just now', rank: 0, duration: '1m', files: 0, cost: 1.27, tokens: '1.18M',
    turns: 14, tools: 61, ctx: 26, entrypoint: 'Claude Desktop',
    doing: 'Reading SessionBoard.tsx',
    goal: 'Review the coSlash web UI and propose design concepts that make it more modern and easier to scan.',
    outcome: 'Analysed the live app, identified the card hierarchy and board density problems, and began building comparative prototypes.',
    decisions: [
      'Treat busy cards, board navigation and slow retrieval as one hierarchy problem.',
      'Keep every concept in the light theme so they compare on equal ground.',
      'Build clickable prototypes rather than annotated design boards.',
    ],
    artifacts: ['01-calm-list.html', '02-navigation.html'],
  },
  {
    id: '01a08e65-3c24-7030-9f3b-c1d18261041a', title: 'Design Coslash UI concepts',
    agent: 'codex', status: 'busy', repo: 'goal-x20-you-are-a-senior', branch: null, source: 'local',
    when: 'just now', rank: 1, duration: '1m', files: 0, cost: 0.96, tokens: '284k',
    turns: 4, tools: 12, ctx: 9, entrypoint: 'Codex Work Desktop',
    doing: 'Writing prototype markup',
    goal: 'Produce design concepts for finding the right coSlash session faster.',
    outcome: 'Drafted a quiet list, a workspace navigator and a glass finder, each with sample data and working search.',
    decisions: [
      'Lead with retrieval rather than visual styling.',
      'Show why a result matched, not just that it matched.',
    ],
    artifacts: ['01-quiet-list.html'],
  },
  {
    id: '7c31aa02-91bd-4c11-bf03-2af0c9911d40', title: 'Refactor the settings schema validator',
    agent: 'claude', status: 'waiting', repo: 'coslash', branch: 'milan/settings-v2', source: 'local',
    when: '4m ago', rank: 2, duration: '23m', files: 6, cost: 2.41, tokens: '890k',
    turns: 11, tools: 48, ctx: 34, entrypoint: 'Claude Desktop', illustrative: true,
    question: 'Should I keep the legacy synthesis.backend key as a read-only alias, or migrate existing settings files on first load?',
    goal: 'Tighten settings.json validation and report actionable errors instead of a generic parse failure.',
    outcome: 'Validator rewritten with per-field errors; blocked on how to treat the deprecated synthesis.backend key.',
    decisions: [
      'Report every invalid field at once rather than failing on the first.',
      'Keep the repair path in the existing settings dialog.',
    ],
    artifacts: ['settings.schema.json', 'settings.test.ts'],
  },
  {
    id: '01a08b12-77aa-4c90-9f21-bb1190ee4412', title: 'Refresh the model table from LiteLLM',
    agent: 'codex', status: 'idle', repo: 'coslash', branch: 'milan/models', source: 'local',
    when: '18m ago', rank: 3, duration: '31m', files: 4, cost: 3.90, tokens: '1.02M',
    turns: 9, tools: 26, ctx: 41, entrypoint: 'Codex Work Desktop', illustrative: true,
    goal: 'Update the bundled pricing table from the upstream LiteLLM catalogue.',
    outcome: 'Regenerated the pricing table and reconciled four models that LiteLLM had renamed.',
    decisions: [
      'Keep unpriced models visible with a warning rather than hiding them.',
      'Pin the catalogue revision so refreshes are reproducible.',
    ],
    artifacts: ['models.json', 'pricing.go'],
  },
  {
    id: '01a08d83-8e17-7e32-9475-f81ff523aa0c', title: 'Generate 4 kitchen renderings',
    agent: 'codex', status: 'inactive', repo: 'ge', branch: null, source: 'local',
    when: '14m ago', rank: 4, duration: '20m', files: 1, cost: 9.66, tokens: '4.05M',
    turns: 22, tools: 71, ctx: 58, entrypoint: 'Codex Work Desktop',
    goal: 'Render the kitchen in a new corner perspective with four grout options on the Zia tile.',
    outcome: 'Created and corrected all four renderings in the new corner perspective with the warm, flat ceramic Zia tile surface and four grout options: Bright White, Sauterne, Light Pewter and Frosty.',
    decisions: [
      'Use the corner perspective rather than the frontal one for depth.',
      'Keep the tile surface flat and matte to read as ceramic.',
    ],
    artifacts: ['kitchen-corner-frosty.png', 'kitchen-corner-sauterne.png'],
  },
  {
    id: '01a08d74-dead-7752-b5e3-a7757d2df441', title: 'Choose Laticrete grout color',
    agent: 'codex', status: 'inactive', repo: 'pick-the-best-grout-color-for', branch: null, source: 'local',
    when: '4h ago', rank: 6, duration: '10m', files: 2, cost: 5.09, tokens: '2.73M',
    turns: 12, tools: 33, ctx: 22, entrypoint: 'Codex Work Desktop',
    goal: 'Pick the best Laticrete grout colour for the Zia tile and show it rendered.',
    outcome: 'Selected Laticrete Frosty as the recommended grout color and generated renderings for Bright White, Sauterne, Light Pewter, and Frosty.',
    decisions: [
      'Recommend Frosty: warm enough to avoid a cold joint, light enough to keep the tile pattern reading.',
      'Rule out Bright White as too high-contrast against the warm glaze.',
    ],
    artifacts: ['grout-comparison.png', 'frosty-detail.png'],
  },
  {
    id: '01a08d65-4d3f-7f32-85b8-082200f464e0', title: 'Find Laticrete color equivalents',
    agent: 'codex', status: 'inactive', repo: 'wha', branch: null, source: 'local',
    when: '4h ago', rank: 7, duration: '2m', files: 0, cost: 0.93, tokens: '236k',
    turns: 3, tools: 6, ctx: 8, entrypoint: 'Codex Work Desktop',
    goal: 'Map the custom swatches in the screenshots to their closest Laticrete equivalents.',
    outcome: 'Warm Gray maps to Silver Shadow and the closest match to Frosty is Alabaster; comparison notes record the difference in warmth for each pair.',
    decisions: ['Compare in daylight balance rather than showroom lighting.'],
    artifacts: ['swatch-matches.csv'],
  },
  {
    id: '01a08cf3-2b41-70a9-91ee-33c7710de882', title: 'Compare SPECTRALOCK and PRISM grout',
    agent: 'codex', status: 'inactive', repo: 'compare-https-www-laticrete-com-products', branch: null, source: 'local',
    when: '6h ago', rank: 8, duration: '9m', files: 0, cost: 1.56, tokens: '635k',
    turns: 7, tools: 14, ctx: 12, entrypoint: 'Codex Work Desktop',
    goal: 'Decide between SPECTRALOCK epoxy and PRISM cement grout for the powder-room wall.',
    outcome: 'Prism is the better choice for this powder-room wall provided a sample test confirms it will not scratch or discolour the marble.',
    decisions: [
      'Epoxy is not required for a low-traffic vertical surface.',
      'Sample-test on an offcut before committing.',
    ],
    artifacts: [],
  },
  {
    id: '01a08d3f-f9a4-7f92-a244-354dafcb5736', title: 'Access Google Sheet',
    agent: 'codex', status: 'inactive', repo: 'AgentsWorkspace', branch: null, source: 'local',
    when: '5h ago', rank: 9, duration: '12m', files: 0, cost: 3.53, tokens: '6.22M',
    turns: 5, tools: 19, ctx: 15, entrypoint: 'Codex Work Desktop',
    goal: 'Confirm whether the tile takeoff sheet can be read.',
    outcome: 'The sheet "45 Warmwood Tile takeoff" is accessible with view-only permissions.',
    decisions: ['Request edit access separately rather than copying the sheet.'],
    artifacts: [],
  },
  {
    id: '01a08cfd-9d6d-7171-82b7-aa9da7cf6442', title: 'Access Google Sheet',
    agent: 'codex', status: 'inactive', repo: 'AgentsWorkspace', branch: null, source: 'local',
    when: '5h ago', rank: 10, duration: '9m', files: 0, cost: 10.53, tokens: '21.4M',
    turns: 4, tools: 12, ctx: 11, entrypoint: 'Codex Work Desktop',
    goal: 'Retry reading the tile takeoff sheet after the permission change.',
    outcome: 'No substantive work was completed; the retry hit the same view-only permission.',
    decisions: [],
    artifacts: [],
  },
  {
    id: '01a08d22-6612-7b31-9a02-1f2e77a19bb1', title: 'Find Mapei grout color matches',
    agent: 'codex', status: 'inactive', repo: 'AgentsWorkspace', branch: null, source: 'local',
    when: '5h ago', rank: 11, duration: '7m', files: 0, cost: 2.17, tokens: '1.12M',
    turns: 6, tools: 11, ctx: 9, entrypoint: 'Codex Work Desktop',
    goal: 'Find the Mapei equivalents for the shortlisted Laticrete colours.',
    outcome: 'Closest matches identified: Alabaster to #90 Light Pewter, Avalanche to #44 Bright White, Eggshell/White to #44.',
    decisions: ['Prefer Mapei Ultracolor Plus for the shorter cure time.'],
    artifacts: ['mapei-crosswalk.csv'],
  },
  {
    id: '01a087b4-0c22-7e41-9911-5533aa771902', title: 'Generate TV wall rendering',
    agent: 'codex', status: 'inactive', repo: 'ge', branch: null, source: 'local',
    when: '3h ago', rank: 5, duration: '31m', files: 2, cost: 12.04, tokens: '8.15M',
    turns: 26, tools: 88, ctx: 63, entrypoint: 'Codex Work Desktop',
    goal: 'Render the 65-inch TV wall with the upper-right recess corner and new lighting.',
    outcome: 'Created and refined the rendering through the latest upper-right recess corner and lighting change.',
    decisions: ['Move the recess to the upper right so the TV mount clears the stud line.'],
    artifacts: ['tv-wall-v4.png'],
  },
  {
    id: '5333c6fb-1c02-4a88-9f31-ab6612ee0031', title: 'Remote Machine design fixes',
    agent: 'claude', status: 'inactive', repo: 'AgentsWorkspace', branch: null, source: 'local',
    when: '21h ago', rank: 12, duration: '1h38m', files: 9, cost: 7.78, tokens: '595k',
    turns: 17, tools: 123, ctx: 26, entrypoint: 'Claude Desktop',
    goal: 'Implement and refine the Remote host landing-page section in coslash-internal from the supplied design.',
    outcome: 'Implemented the remote host section, refined its structure and copy, added the angled NEW navigation badge, committed the changes and pushed the branch.',
    decisions: [
      'Lead the section with the remote session-list board.',
      'Use "Remote host" consistently in navigation and section labelling.',
      'Position the NEW badge as an angled overlay on the Remote host label.',
    ],
    artifacts: ['Remote-Machine-Fixed.tsx', 'nav-badge.svg'],
  },
  {
    id: 'c6b455d8-77e1-4a10-b662-19c0b1ee7741', title: 'Paris fine dining availability',
    agent: 'claude', status: 'inactive', repo: 'AgentsWorkspace', branch: null, source: 'local',
    when: 'yesterday', rank: 13, duration: '2h04m', files: 3, cost: 64.45, tokens: '21.4M',
    turns: 41, tools: 210, ctx: 78, entrypoint: 'Claude Desktop', subagents: 7,
    goal: 'Rank the best fine-dining options in Paris and check availability for the requested dates.',
    outcome: 'Completed a Top 25 ranking with component and final scores, addresses, CDG/Orly distances, and availability for the requested dates.',
    decisions: [
      'Weight food quality above room and service, but keep all three in the composite.',
      'Expand the candidate pool to 1-star restaurants to avoid an all-3-star list.',
    ],
    artifacts: ['paris-top25.csv', 'availability.json'],
  },
  {
    id: '23e411ff-0a92-4c31-b7a1-9911ee220034', title: 'Fine-dining restaurant list',
    agent: 'claude', status: 'inactive', repo: 'AgentsWorkspace', branch: null, source: 'local',
    when: 'yesterday', rank: 14, duration: '22m', files: 1, cost: 4.22, tokens: '1.88M',
    turns: 9, tools: 31, ctx: 19, entrypoint: 'Claude Desktop',
    goal: 'Assemble a weighted candidate pool of fine-dining restaurants across several cities.',
    outcome: 'A weighted candidate pool was assembled and Venice was expanded to include 1-star restaurants.',
    decisions: ['Include 1-star rooms where the city has few 2- and 3-star options.'],
    artifacts: ['candidates.csv'],
  },
  {
    id: 'aa7c9391-2b71-4c01-9a17-0099ee771122', title: 'Keep referenced spawns and full session cost',
    agent: 'claude', status: 'inactive', repo: 'coslash', branch: 'milan/caps', source: 'local',
    when: '2d ago', rank: 15, duration: '54m', files: 12, cost: 11.03, tokens: '5.02M',
    turns: 19, tools: 96, ctx: 44, entrypoint: 'Claude Desktop',
    goal: 'Stop remote fact-list caps from dropping referenced spawns or understating session cost.',
    outcome: 'Referenced spawns are retained when capping remote fact lists, and the full session cost is preserved.',
    decisions: [
      'Cap the list for display only; never drop a spawn another row references.',
      'Compute cost from the full history, not the capped view.',
    ],
    artifacts: ['remote-api.ts', 'remote-api.test.ts'],
  },
  {
    id: 'ce9a8c51-9931-4b02-8f10-77aa11bb3300', title: 'Clarify commit SHA array independence',
    agent: 'claude', status: 'inactive', repo: 'coslash', branch: 'main', source: 'local',
    when: '3d ago', rank: 16, duration: '18m', files: 1, cost: 1.44, tokens: '420k',
    turns: 6, tools: 14, ctx: 11, entrypoint: 'Claude Desktop',
    goal: 'Document that the snapshot commit SHA arrays are independent of one another.',
    outcome: 'Snapshot docs now state that each commit SHA array is independent and must not be zipped positionally.',
    decisions: ['Document the invariant rather than enforcing it in code for now.'],
    artifacts: ['snapshot.md'],
  },
  {
    id: '9f21ab70-3c11-4e02-aa31-5511ee990077', title: 'Nightly integration sweep',
    agent: 'claude', status: 'unknown', repo: 'coslash', branch: 'main', source: 'agent-box',
    when: '2d ago', rank: 17, duration: null, files: 7, cost: 3.11, tokens: '1.4M',
    turns: 12, tools: 54, ctx: 31, entrypoint: 'CLI',
    goal: 'Run the nightly integration sweep across the collector and frontend test suites.',
    outcome: 'Last transcript write was 2d ago; the host became unreachable before any completion was recorded.',
    decisions: [],
    artifacts: ['sweep.log'],
  },
  {
    id: '4a8811cd-7712-4b31-9902-3311aa660055', title: 'Backfill remote collector helpers',
    agent: 'claude', status: 'unknown', repo: 'coslash', branch: 'main', source: 'agent-box',
    when: '3d ago', rank: 18, duration: null, files: 4, cost: 4.02, tokens: '1.9M',
    turns: 8, tools: 37, ctx: 24, entrypoint: 'CLI',
    goal: 'Install and verify the Linux collector helper on the remote host.',
    outcome: 'Helper installation started; process liveness could not be confirmed after the host stopped responding.',
    decisions: [],
    artifacts: [],
  },
  {
    id: '6612aa99-1d02-4b71-8831-99ee2200aa11', title: 'Review GHI CSV exports',
    agent: 'codex', status: 'inactive', repo: 'centauri-ai', branch: 'hlu/gcp', source: 'agent-box',
    when: '8m ago', rank: 19, duration: '14m', files: 3, cost: 2.04, tokens: '760k',
    turns: 7, tools: 22, ctx: 17, entrypoint: 'CLI',
    goal: 'Check the GHI export mappings before the data hand-off.',
    outcome: 'Three ambiguous source mappings need review before the export can continue.',
    decisions: ['Block the export until the ambiguous columns are resolved.'],
    artifacts: ['source-mapping.csv'],
  },
  {
    id: '8831cc11-4a02-4e91-bb02-7711aa330099', title: 'Resume staging machines',
    agent: 'codex', status: 'inactive', repo: 'centauri-ai', branch: 'main', source: 'agent-box',
    when: '15m ago', rank: 20, duration: '26m', files: 2, cost: 9.14, tokens: '3.1M',
    turns: 15, tools: 61, ctx: 35, entrypoint: 'CLI',
    goal: 'Bring the staging machines back up after the restart window.',
    outcome: 'Checked frontend and backend health after restarting the staging services; both returned healthy.',
    decisions: ['Restart backend before frontend so health checks pass on first try.'],
    artifacts: [],
  },
  {
    id: '2201bb44-9c11-4d31-a012-6611ee880022', title: 'Inspect session cache after refresh',
    agent: 'codex', status: 'inactive', repo: 'coslash', branch: 'main', source: 'local',
    when: '38m ago', rank: 21, duration: '8m', files: 1, cost: 1.42, tokens: '512k',
    turns: 5, tools: 17, ctx: 10, entrypoint: 'Codex Work Desktop',
    goal: 'Check whether the session cache is invalidated correctly on refresh.',
    outcome: 'Cache refresh returned; the session remains open and the stale entry was not evicted.',
    decisions: ['Evict by source id rather than clearing the whole cache.'],
    artifacts: [],
  },
];

/* ---------- helpers ---------- */

const SEARCH_FIELDS = [
  { key: 'title',    label: 'title'    },
  { key: 'goal',     label: 'goal'     },
  { key: 'outcome',  label: 'outcome'  },
  { key: 'repo',     label: 'repo'     },
  { key: 'branch',   label: 'branch'   },
  { key: 'artifact', label: 'file'     },
];

/** Rank: title > goal/outcome > repo/branch/file. Recency breaks ties.
    Live sessions do NOT jump the queue once a query exists. */
function searchSessions(query) {
  const q = query.trim().toLowerCase();
  if (!q) return SESSIONS.map((s) => ({ session: s, evidence: null, score: 0 }));

  const hits = [];
  for (const s of SESSIONS) {
    let best = null;
    let score = 0;
    const test = (field, value, weight) => {
      if (!value) return;
      const i = value.toLowerCase().indexOf(q);
      if (i === -1) return;
      if (weight > score) { score = weight; best = { field, value, index: i }; }
    };
    test('title', s.title, 100);
    test('outcome', s.outcome, 60);
    test('goal', s.goal, 55);
    test('repo', s.repo, 30);
    test('branch', s.branch, 28);
    for (const a of s.artifacts || []) test('file', a, 25);
    if (best) hits.push({ session: s, evidence: best, score });
  }
  hits.sort((a, b) => b.score - a.score || a.session.rank - b.session.rank);
  return hits;
}

function snippet(value, index, q, pad = 46) {
  const start = Math.max(0, index - pad);
  const end = Math.min(value.length, index + q.length + pad);
  return (start > 0 ? '…' : '') + value.slice(start, end) + (end < value.length ? '…' : '');
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);
}

function highlight(text, q) {
  if (!q) return escapeHtml(text);
  const i = text.toLowerCase().indexOf(q.toLowerCase());
  if (i === -1) return escapeHtml(text);
  return escapeHtml(text.slice(0, i)) + '<mark>' + escapeHtml(text.slice(i, i + q.length)) + '</mark>' + escapeHtml(text.slice(i + q.length));
}

function money(n) { return '$' + n.toFixed(2); }

function statusOf(s) { return STATUS[s.status]; }
function agentOf(s) { return AGENT[s.agent]; }

/** Attention buckets used by the list groups and the board lanes. */
function bucketOf(s) {
  if (s.status === 'waiting') return 'needs';
  if (s.status === 'busy') return 'running';
  if (s.status === 'idle') return 'idle';
  if (s.status === 'unknown') return 'unknown';
  return /ago|now/.test(s.when) && !/\d+d ago|yesterday/.test(s.when) ? 'today' : 'earlier';
}

const BUCKETS = [
  { key: 'needs',   label: 'Needs you',        empty: 'Nothing is waiting on you.' },
  { key: 'running', label: 'Running',          empty: 'No sessions running.' },
  { key: 'idle',    label: 'Idle',             empty: 'No idle sessions.' },
  { key: 'today',   label: 'Last active today',empty: 'Nothing active today.' },
  { key: 'unknown', label: 'Liveness unknown', empty: 'No unknown sessions.' },
  { key: 'earlier', label: 'Earlier',          empty: 'Nothing earlier.' },
];

/* Derived from SESSIONS so every surface agrees. The live dataset held 52 sessions;
   this sample carries 22 of them, so counts shown are the sample's own. */
const TOTALS = {
  sessions: SESSIONS.length,
  claude: SESSIONS.filter((s) => s.agent === 'claude').length,
  codex: SESSIONS.filter((s) => s.agent === 'codex').length,
  spend: SESSIONS.reduce((n, s) => n + s.cost, 0),
  active: SESSIONS.filter((s) => s.status === 'busy').length,
  inactive: SESSIONS.filter((s) => s.status === 'inactive').length,
  unknown: SESSIONS.filter((s) => s.status === 'unknown').length,
  waiting: SESSIONS.filter((s) => s.status === 'waiting').length,
  tokens: '68M',
};
