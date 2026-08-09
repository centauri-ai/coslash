// The Atlas board: an editable graph of agent seats and typed connections.
//
// Where DaGama's shape is fixed, Atlas lets an operator add seats, wire them,
// and configure a committee per seat. Only one shape is runnable today — the
// plan → build → review starter chain — and the editor says so rather than
// hiding the Run control, because a graph you can draw but not explain is worse
// than one that tells you what it is waiting for.
//
// This module mirrors `collector/internal/plugins/canvas/atlas/graph.go`. It is
// pure: no fetch, no storage, no DOM. Every field is repaired toward a legal
// value rather than rejected, except anything that could reach a command line.

import {
  ATLAS_COMPONENT_IDS,
  ATLAS_DEFAULT_FEEDBACK_MAX_ROUNDS,
  ATLAS_MAX_ARGV_TOKENS,
  ATLAS_MAX_CHECKS,
  ATLAS_MAX_FEEDBACK_ROUNDS,
  ATLAS_MAX_WORKERS,
  effortsFor,
  hasSeat,
  modelsFor,
  permissionsFor,
  validArgvToken,
  validBaseBranch,
  validCheckCommand,
  validCheckName,
  validGraphId,
  validRequiredOutput,
  type AtlasComponentID,
  type AtlasEdgeKind,
  type AtlasTriggerMode,
  type AtlasVendor,
  type AtlasWorkerRole,
} from '@/plugins/canvas/atlas/vocabulary';
import type { CanvasNodeBox } from '@/plugins/canvas/shared';

/** The current graph schema. A v1 document is migrated on read. */
export const ATLAS_BOARD_SCHEMA_VERSION = 2;
/** The record-shaped schema accepted at the migration boundary. */
export const ATLAS_LEGACY_BOARD_SCHEMA_VERSION = 1;
/** The storage envelope's version, which is deliberately not the graph's. */
export const ATLAS_DOCUMENT_SCHEMA_VERSION = 1;

export const ATLAS_WORLD = { width: 5200, height: 3200 } as const;
export const ATLAS_ZOOM_BOUNDS = { min: 0.25, max: 2, step: 0.1 } as const;
export const ATLAS_DEFAULT_ZOOM = 0.55;
export const ATLAS_NODE_MIN_WIDTH = 240;
export const ATLAS_NODE_MIN_HEIGHT = 120;

const SEAT_TERMINAL_WIDTH = 440;
const SEAT_TERMINAL_HEIGHT = 760;
const SEAT_PROMPT_WIDTH = 380;
const SEAT_PROMPT_HEIGHT = 260;
const SEAT_INFO_WIDTH = 380;
const SEAT_INFO_HEIGHT = 320;
const COMPANION_GAP = 20;
const CLUSTER_STACK_GAP = 28;
const STAGE_GAP = 90;
const RAIL_X = 120;
const RAIL_Y = 160;

export const ATLAS_MAX_PROMPT_LENGTH = 8_000;
export const ATLAS_MAX_INSTRUCTIONS_LENGTH = 8_000;
export const ATLAS_MAX_TITLE_LENGTH = 120;

export type AtlasSeat = {
  vendor: AtlasVendor;
  model: string;
  effort: string;
  permission: string;
};

/** One committee member. The profile is inline, matching the saved shape. */
export type AtlasWorkerSeat = {
  id: string;
  role: AtlasWorkerRole;
  vendor: AtlasVendor;
  model: string;
  effort: string;
  permission: string;
  preserved: Readonly<Record<string, unknown>>;
};

export type AtlasCheck = { name: string; argv: string[] };

export type AtlasPublishConfig = { base: string; draft: boolean };

export type AtlasRunPolicy = { checks: AtlasCheck[]; publish: AtlasPublishConfig };

export type AtlasComponent = {
  id: string;
  title: string;
  prompt: string;
  /** Mirrors the sole worker, or the main worker when there are several. */
  seat: AtlasSeat;
  seats: AtlasWorkerSeat[];
  consolidationPrompt: string;
  requiredOutputs: string[];
  box: CanvasNodeBox;
  promptBox: CanvasNodeBox;
  infoBox: CanvasNodeBox;
  /** Binds a seat to a pipeline stage. Null for a freeform seat. */
  legacyRole: AtlasComponentID | null;
  preserved: Readonly<Record<string, unknown>>;
};

export type AtlasEdge = {
  id: string;
  from: string;
  to: string;
  kind: AtlasEdgeKind;
  mode: AtlasTriggerMode;
  /** Caps automatic repair Builds. Only meaningful on a feedback edge. */
  maxRounds: number;
  preserved: Readonly<Record<string, unknown>>;
};

export type AtlasViewport = { zoom: number; panX: number; panY: number };

export type AtlasSystemPrompts = {
  plan: string;
  build: string;
  review: string;
  planRefine: string;
};

export type AtlasBoard = {
  kind: string;
  schemaVersion: typeof ATLAS_BOARD_SCHEMA_VERSION;
  instructions: string;
  systemPrompts: AtlasSystemPrompts;
  components: AtlasComponent[];
  edges: AtlasEdge[];
  /** Omitted by a board that configures neither checks nor a publish target. */
  runPolicy: AtlasRunPolicy | null;
  viewport: AtlasViewport;
  preserved: Readonly<Record<string, unknown>>;
};

// ---------------------------------------------------------------------------
// Node identity
// ---------------------------------------------------------------------------

export type AtlasSeatRole = 'terminal' | 'prompt' | 'info';

export function seatPromptNodeId(componentId: string): string {
  return `${componentId}-prompt`;
}

export function seatInfoNodeId(componentId: string): string {
  return `${componentId}-info`;
}

export function parseAtlasNodeId(nodeId: string): { componentId: string; role: AtlasSeatRole } {
  if (nodeId.endsWith('-prompt')) {
    return { componentId: nodeId.slice(0, -'-prompt'.length), role: 'prompt' };
  }
  if (nodeId.endsWith('-info')) {
    return { componentId: nodeId.slice(0, -'-info'.length), role: 'info' };
  }
  return { componentId: nodeId, role: 'terminal' };
}

/** Every node id a board renders, seat cluster by seat cluster. */
export function atlasNodeIds(board: AtlasBoard): string[] {
  const ids: string[] = [];
  for (const component of board.components) {
    ids.push(component.id, seatPromptNodeId(component.id), seatInfoNodeId(component.id));
  }
  return ids;
}

export function componentById(board: AtlasBoard, id: string): AtlasComponent | null {
  return board.components.find((component) => component.id === id) ?? null;
}

export function componentByRole(board: AtlasBoard, role: AtlasComponentID): AtlasComponent | null {
  return board.components.find((component) => component.legacyRole === role) ?? null;
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

export function defaultSeatForVendor(vendor: AtlasVendor): AtlasSeat {
  return vendor === 'codex'
    ? { vendor: 'codex', model: 'gpt-5.6-terra', effort: 'high', permission: 'workspace-write' }
    : { vendor: 'claude', model: 'opus', effort: 'high', permission: 'acceptEdits' };
}

// Review deliberately runs the other vendor family: reviewing with a different
// model is the cheapest available diversity lever.
export function defaultSeatForRole(role: AtlasComponentID | null): AtlasSeat {
  return defaultSeatForVendor(role === 'review' ? 'codex' : 'claude');
}

export function defaultRequiredOutputs(role: AtlasComponentID | null): string[] {
  switch (role) {
    case 'plan':
      return ['PLAN.md'];
    case 'build':
      return ['IMPLEMENTATION.md', 'CHANGESET.patch', 'change.json'];
    case 'review':
      return ['REVIEW.md', 'review.json'];
    default:
      return ['OUTPUT.md'];
  }
}

export function workerSeatId(componentId: string, index: number): string {
  return `${componentId}-w${index + 1}`;
}

export function defaultTerminalBox(index: number): CanvasNodeBox {
  return {
    x: RAIL_X + index * (SEAT_TERMINAL_WIDTH + STAGE_GAP),
    y: RAIL_Y,
    width: SEAT_TERMINAL_WIDTH,
    height: SEAT_TERMINAL_HEIGHT,
    collapsed: false,
    locked: false,
  };
}

export function defaultPromptBox(terminal: CanvasNodeBox): CanvasNodeBox {
  return {
    x: terminal.x,
    y: terminal.y + terminal.height + CLUSTER_STACK_GAP,
    width: SEAT_PROMPT_WIDTH,
    height: SEAT_PROMPT_HEIGHT,
    collapsed: false,
    locked: false,
  };
}

export function defaultInfoBox(terminal: CanvasNodeBox): CanvasNodeBox {
  return {
    x: terminal.x + SEAT_PROMPT_WIDTH + COMPANION_GAP,
    y: terminal.y + terminal.height + CLUSTER_STACK_GAP,
    width: SEAT_INFO_WIDTH,
    height: SEAT_INFO_HEIGHT,
    collapsed: false,
    locked: false,
  };
}

const ROLE_TITLES: Record<AtlasComponentID, string> = {
  intake: 'INTAKE',
  plan: 'PLAN',
  build: 'BUILD',
  verify: 'VERIFY',
  review: 'REVIEW',
  publish: 'PUBLISH',
};

export function newAgentSeat(
  id: string,
  role: AtlasComponentID | null,
  index: number,
  workerCount = 1,
): AtlasComponent {
  const box = defaultTerminalBox(index);
  const profile = defaultSeatForRole(role);
  const seats: AtlasWorkerSeat[] = [];
  for (let position = 0; position < Math.max(1, workerCount); position += 1) {
    seats.push({
      id: workerSeatId(id, position),
      // A sole worker is never `main`: the role only means something when there
      // is a committee for it to be the main member of.
      role: workerCount > 1 && position === 0 ? 'main' : 'worker',
      ...profile,
      preserved: {},
    });
  }
  return {
    id,
    title: role ? ROLE_TITLES[role] : id.toUpperCase(),
    prompt: '',
    seat: profile,
    seats,
    consolidationPrompt: '',
    requiredOutputs: defaultRequiredOutputs(role),
    box,
    promptBox: defaultPromptBox(box),
    infoBox: defaultInfoBox(box),
    legacyRole: role,
    preserved: {},
  };
}

export function defaultSystemPrompts(): AtlasSystemPrompts {
  // The editor never invents prompt text: a blank here means "use the server's
  // current default", which normalization on the backend fills in.
  return { plan: '', build: '', review: '', planRefine: '' };
}

/** The starter chain: the one graph shape that is runnable today. */
export function defaultAtlasBoard(): AtlasBoard {
  const plan = newAgentSeat('plan', 'plan', 0);
  const build = newAgentSeat('build', 'build', 1);
  const review = newAgentSeat('review', 'review', 2);
  return {
    kind: 'atlas',
    schemaVersion: ATLAS_BOARD_SCHEMA_VERSION,
    instructions: '',
    systemPrompts: defaultSystemPrompts(),
    components: [plan, build, review],
    edges: [triggerEdge('plan', 'build'), triggerEdge('build', 'review'), feedbackEdge('review', 'build')],
    runPolicy: null,
    viewport: { zoom: ATLAS_DEFAULT_ZOOM, panX: 0, panY: 0 },
    preserved: {},
  };
}

export function triggerEdge(from: string, to: string, mode: AtlasTriggerMode = 'auto'): AtlasEdge {
  return { id: `${from}->${to}`, from, to, kind: 'trigger', mode, maxRounds: 0, preserved: {} };
}

export function feedbackEdge(from: string, to: string): AtlasEdge {
  return {
    id: `${from}~>${to}`,
    from,
    to,
    kind: 'feedback',
    mode: 'auto',
    maxRounds: ATLAS_DEFAULT_FEEDBACK_MAX_ROUNDS,
    preserved: {},
  };
}

// ---------------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------------

function finiteNumber(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback;
}

function asString(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback;
}

function clampedText(value: unknown, limit: number): string {
  return asString(value).slice(0, limit);
}

function oneOf(value: unknown, allowed: readonly string[], fallback: string): string {
  return typeof value === 'string' && allowed.includes(value) ? value : fallback;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value != null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function preservedFields(source: Record<string, unknown>, known: readonly string[]): Record<string, unknown> {
  const extra: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(source)) {
    if (!known.includes(key)) extra[key] = value;
  }
  return extra;
}

const BOARD_FIELDS = [
  'kind',
  'schemaVersion',
  'instructions',
  'systemPrompts',
  'components',
  'edges',
  'runPolicy',
  'viewport',
] as const;
const COMPONENT_FIELDS = [
  'id',
  'title',
  'prompt',
  'seat',
  'seats',
  'committee',
  'requiredOutputs',
  'box',
  'promptBox',
  'infoBox',
  'legacyRole',
] as const;
const WORKER_FIELDS = ['id', 'role', 'vendor', 'model', 'effort', 'permission'] as const;
const EDGE_FIELDS = ['id', 'from', 'to', 'kind', 'mode', 'maxRounds'] as const;

function normalizeBox(value: unknown, fallback: CanvasNodeBox): CanvasNodeBox {
  const box = asRecord(value) as Partial<CanvasNodeBox>;
  const width = Math.max(
    ATLAS_NODE_MIN_WIDTH,
    Math.min(ATLAS_WORLD.width, finiteNumber(box.width, fallback.width)),
  );
  const height = Math.max(
    ATLAS_NODE_MIN_HEIGHT,
    Math.min(ATLAS_WORLD.height, finiteNumber(box.height, fallback.height)),
  );
  return {
    x: Math.max(0, Math.min(ATLAS_WORLD.width - width, finiteNumber(box.x, fallback.x))),
    y: Math.max(0, Math.min(ATLAS_WORLD.height - height, finiteNumber(box.y, fallback.y))),
    width,
    height,
    collapsed: typeof box.collapsed === 'boolean' ? box.collapsed : false,
    locked: typeof box.locked === 'boolean' ? box.locked : false,
  };
}

function normalizeSeat(value: unknown, role: AtlasComponentID | null): AtlasSeat {
  const fallback = defaultSeatForRole(role);
  const seat = asRecord(value);
  const vendor: AtlasVendor =
    seat.vendor === 'codex' ? 'codex' : seat.vendor === 'claude' ? 'claude' : fallback.vendor;
  // Switching vendor invalidates the whole vocabulary, so a mismatched model
  // resolves against that vendor's default rather than being kept.
  const vendorDefault = vendor === fallback.vendor ? fallback : defaultSeatForVendor(vendor);
  const model = oneOf(seat.model, modelsFor(vendor), vendorDefault.model);
  return {
    vendor,
    model,
    effort: oneOf(seat.effort, effortsFor(vendor, model), vendorDefault.effort),
    permission: oneOf(seat.permission, permissionsFor(vendor), vendorDefault.permission),
  };
}

function normalizeWorkers(
  value: unknown,
  componentId: string,
  role: AtlasComponentID | null,
): AtlasWorkerSeat[] {
  const raw = Array.isArray(value) ? value.slice(0, ATLAS_MAX_WORKERS) : [];
  const seen = new Set<string>();
  const workers: AtlasWorkerSeat[] = [];
  for (const [index, entry] of raw.entries()) {
    const record = asRecord(entry);
    const profile = normalizeSeat(record, role);
    // A duplicate or unusable id would make two committee members
    // indistinguishable in the run log, so it is replaced by position.
    let id = asString(record.id).trim();
    if (!validGraphId(id) || seen.has(id)) id = workerSeatId(componentId, index);
    seen.add(id);
    workers.push({
      id,
      role: record.role === 'main' ? 'main' : 'worker',
      ...profile,
      preserved: preservedFields(record, WORKER_FIELDS),
    });
  }
  if (workers.length === 0) {
    workers.push({
      id: workerSeatId(componentId, 0),
      role: 'worker',
      ...defaultSeatForRole(role),
      preserved: {},
    });
  }
  // Exactly one main when there is a committee, and never one when there is not:
  // `main` names the seat that writes the promoted artifact, which only exists
  // as a distinction among siblings.
  if (workers.length === 1) {
    workers[0].role = 'worker';
  } else {
    const mainIndex = Math.max(
      0,
      workers.findIndex((worker) => worker.role === 'main'),
    );
    workers.forEach((worker, index) => {
      worker.role = index === mainIndex ? 'main' : 'worker';
    });
  }
  return workers;
}

export function normalizeCheck(value: unknown): AtlasCheck | null {
  const check = asRecord(value);
  const name = asString(check.name).trim();
  if (!validCheckName(name)) return null;
  const argv = check.argv;
  // Over-length argv is rejected, not truncated: truncating would run a
  // different, shorter command than the one configured.
  if (!Array.isArray(argv) || argv.length === 0 || argv.length > ATLAS_MAX_ARGV_TOKENS) return null;
  if (!validCheckCommand(argv[0])) return null;
  if (!argv.every(validArgvToken)) return null;
  return { name, argv: argv as string[] };
}

function normalizeRequiredOutputs(value: unknown, role: AtlasComponentID | null): string[] {
  const raw = Array.isArray(value) ? value : [];
  const outputs = raw.map((entry) => asString(entry).trim()).filter((entry) => validRequiredOutput(entry));
  const unique = [...new Set(outputs)];
  return unique.length > 0 ? unique : defaultRequiredOutputs(role);
}

function normalizeComponent(value: unknown, index: number, taken: Set<string>): AtlasComponent {
  const record = asRecord(value);
  const legacyRole = isRole(record.legacyRole) ? record.legacyRole : null;

  let id = asString(record.id).trim();
  if (!validGraphId(id) || taken.has(id)) id = uniqueId(legacyRole ?? `seat-${index + 1}`, taken);
  taken.add(id);

  const box = normalizeBox(record.box, defaultTerminalBox(index));
  const seats = normalizeWorkers(record.seats, id, legacyRole);
  const main = seats.find((worker) => worker.role === 'main') ?? seats[0];
  const committee = asRecord(record.committee);

  return {
    id,
    title: clampedText(record.title, ATLAS_MAX_TITLE_LENGTH) || id.toUpperCase(),
    prompt: clampedText(record.prompt, ATLAS_MAX_PROMPT_LENGTH),
    // The mirrored seat is derived, never trusted: it exists so a reader can
    // see one profile without walking the committee, and a stored value that
    // disagreed with the main worker would be a second source of truth.
    seat: { vendor: main.vendor, model: main.model, effort: main.effort, permission: main.permission },
    seats,
    consolidationPrompt: clampedText(committee.consolidationPrompt, ATLAS_MAX_PROMPT_LENGTH),
    requiredOutputs: normalizeRequiredOutputs(record.requiredOutputs, legacyRole),
    box,
    promptBox: normalizeBox(record.promptBox, defaultPromptBox(box)),
    infoBox: normalizeBox(record.infoBox, defaultInfoBox(box)),
    legacyRole,
    preserved: preservedFields(record, COMPONENT_FIELDS),
  };
}

function isRole(value: unknown): value is AtlasComponentID {
  return typeof value === 'string' && (ATLAS_COMPONENT_IDS as readonly string[]).includes(value);
}

function uniqueId(stem: string, taken: Set<string>): string {
  const base = validGraphId(stem) ? stem : 'seat';
  if (!taken.has(base)) return base;
  for (let suffix = 2; suffix < 1000; suffix += 1) {
    const candidate = `${base}-${suffix}`;
    if (!taken.has(candidate)) return candidate;
  }
  return `${base}-${taken.size + 1}`;
}

function normalizeEdges(value: unknown, componentIds: Set<string>): AtlasEdge[] {
  const raw = Array.isArray(value) ? value : [];
  const edges: AtlasEdge[] = [];
  const seen = new Set<string>();
  for (const entry of raw) {
    const record = asRecord(entry);
    const from = asString(record.from).trim();
    const to = asString(record.to).trim();
    // An edge to a seat that no longer exists is dropped rather than kept as a
    // dangling reference the runtime would have to interpret.
    if (!componentIds.has(from) || !componentIds.has(to) || from === to) continue;
    const kind: AtlasEdgeKind = record.kind === 'feedback' ? 'feedback' : 'trigger';
    const key = `${kind}:${from}->${to}`;
    if (seen.has(key)) continue;
    seen.add(key);

    let id = asString(record.id).trim();
    if (!validGraphId(id)) id = kind === 'feedback' ? `${from}~>${to}` : `${from}->${to}`;
    edges.push({
      id,
      from,
      to,
      kind,
      mode: record.mode === 'manual' ? 'manual' : 'auto',
      maxRounds:
        kind === 'feedback'
          ? Math.max(
              1,
              Math.min(
                ATLAS_MAX_FEEDBACK_ROUNDS,
                finiteNumber(record.maxRounds, ATLAS_DEFAULT_FEEDBACK_MAX_ROUNDS),
              ),
            )
          : 0,
      preserved: preservedFields(record, EDGE_FIELDS),
    });
  }
  return edges;
}

function normalizeRunPolicy(value: unknown): AtlasRunPolicy | null {
  if (value == null) return null;
  const record = asRecord(value);
  const checks = Array.isArray(record.checks)
    ? record.checks
        .slice(0, ATLAS_MAX_CHECKS)
        .map(normalizeCheck)
        .filter((check): check is AtlasCheck => check != null)
    : [];
  const publish = asRecord(record.publish);
  const base = asString(publish.base).trim();
  return {
    checks,
    publish: {
      base: validBaseBranch(base) ? base : '',
      draft: typeof publish.draft === 'boolean' ? publish.draft : true,
    },
  };
}

function normalizeViewport(value: unknown): AtlasViewport {
  const viewport = asRecord(value);
  return {
    zoom: Math.max(
      ATLAS_ZOOM_BOUNDS.min,
      Math.min(ATLAS_ZOOM_BOUNDS.max, finiteNumber(viewport.zoom, ATLAS_DEFAULT_ZOOM)),
    ),
    panX: finiteNumber(viewport.panX, 0),
    panY: finiteNumber(viewport.panY, 0),
  };
}

function normalizeSystemPrompts(value: unknown): AtlasSystemPrompts {
  const record = asRecord(value);
  return {
    plan: clampedText(record.plan, ATLAS_MAX_PROMPT_LENGTH),
    build: clampedText(record.build, ATLAS_MAX_PROMPT_LENGTH),
    review: clampedText(record.review, ATLAS_MAX_PROMPT_LENGTH),
    planRefine: clampedText(record.planRefine, ATLAS_MAX_PROMPT_LENGTH),
  };
}

/**
 * Repair any input into a complete, valid v2 board.
 *
 * A v1 document is migrated rather than rejected: it is a board someone made,
 * and the alternative is silently handing them an empty canvas. A version this
 * build has never heard of is a different case — it may carry meaning that
 * would be destroyed by a save, so it yields the default and the caller is
 * expected to refuse to write.
 */
export function normalizeAtlasBoard(value: unknown): AtlasBoard {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) return defaultAtlasBoard();
  const candidate = value as Record<string, unknown>;
  const version = candidate.schemaVersion;
  if (version === ATLAS_LEGACY_BOARD_SCHEMA_VERSION) return migrateLegacyBoard(candidate);
  if (version !== undefined && version !== ATLAS_BOARD_SCHEMA_VERSION) return defaultAtlasBoard();

  const taken = new Set<string>();
  const components = (Array.isArray(candidate.components) ? candidate.components : []).map(
    (component, index) => normalizeComponent(component, index, taken),
  );
  if (components.length === 0) return defaultAtlasBoard();

  return {
    kind: asString(candidate.kind, 'atlas') || 'atlas',
    schemaVersion: ATLAS_BOARD_SCHEMA_VERSION,
    instructions: clampedText(candidate.instructions, ATLAS_MAX_INSTRUCTIONS_LENGTH),
    systemPrompts: normalizeSystemPrompts(candidate.systemPrompts),
    components,
    edges: normalizeEdges(candidate.edges, new Set(components.map((component) => component.id))),
    runPolicy: normalizeRunPolicy(candidate.runPolicy),
    viewport: normalizeViewport(candidate.viewport),
    preserved: preservedFields(candidate, BOARD_FIELDS),
  };
}

/**
 * Migrate a v1 record-shaped board into the v2 graph.
 *
 * v1 stored the three stages as named members rather than a component list, and
 * had no edges: the chain was implied. The migration makes both explicit so the
 * result is an ordinary v2 board the editor can extend.
 */
function migrateLegacyBoard(candidate: Record<string, unknown>): AtlasBoard {
  const members = asRecord(candidate.components);
  const taken = new Set<string>();
  const components: AtlasComponent[] = [];
  let index = 0;
  for (const role of ['plan', 'build', 'review'] as const) {
    const member = asRecord(members[role]);
    const normalized = normalizeComponent(
      { ...member, id: asString(member.id) || role, legacyRole: role },
      index,
      taken,
    );
    components.push(normalized);
    index += 1;
  }
  const [plan, build, review] = components;
  return {
    kind: 'atlas',
    schemaVersion: ATLAS_BOARD_SCHEMA_VERSION,
    instructions: clampedText(candidate.instructions, ATLAS_MAX_INSTRUCTIONS_LENGTH),
    systemPrompts: normalizeSystemPrompts(candidate.systemPrompts),
    components,
    edges: [
      triggerEdge(plan.id, build.id),
      triggerEdge(build.id, review.id),
      feedbackEdge(review.id, build.id),
    ],
    runPolicy: normalizeRunPolicy(candidate.runPolicy),
    viewport: normalizeViewport(candidate.viewport),
    // The migration is a rewrite, so v1-only members are not carried forward as
    // preserved fields where they would be re-emitted into a v2 document.
    preserved: {},
  };
}

/** True when a document declares a schema this build cannot safely write. */
export function isUnsupportedAtlasBoard(value: unknown): boolean {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) return false;
  const version = (value as Record<string, unknown>).schemaVersion;
  if (version === undefined) return false;
  return version !== ATLAS_BOARD_SCHEMA_VERSION && version !== ATLAS_LEGACY_BOARD_SCHEMA_VERSION;
}

/** True when a document was migrated from the record-shaped schema on read. */
export function wasMigratedFromLegacy(value: unknown): boolean {
  return asRecord(value).schemaVersion === ATLAS_LEGACY_BOARD_SCHEMA_VERSION;
}

// ---------------------------------------------------------------------------
// Serialization
// ---------------------------------------------------------------------------

export function serializeAtlasBoard(board: AtlasBoard): Record<string, unknown> {
  return {
    ...board.preserved,
    kind: board.kind,
    schemaVersion: board.schemaVersion,
    instructions: board.instructions,
    systemPrompts: board.systemPrompts,
    components: board.components.map((component) => ({
      ...component.preserved,
      id: component.id,
      title: component.title,
      prompt: component.prompt,
      seat: component.seat,
      seats: component.seats.map((worker) => ({
        ...worker.preserved,
        id: worker.id,
        role: worker.role,
        vendor: worker.vendor,
        model: worker.model,
        effort: worker.effort,
        permission: worker.permission,
      })),
      committee: { consolidationPrompt: component.consolidationPrompt },
      requiredOutputs: component.requiredOutputs,
      box: component.box,
      promptBox: component.promptBox,
      infoBox: component.infoBox,
      legacyRole: component.legacyRole,
    })),
    edges: board.edges.map((edge) => ({
      ...edge.preserved,
      id: edge.id,
      from: edge.from,
      to: edge.to,
      kind: edge.kind,
      mode: edge.mode,
      ...(edge.kind === 'feedback' ? { maxRounds: edge.maxRounds } : {}),
    })),
    ...(board.runPolicy ? { runPolicy: board.runPolicy } : {}),
    viewport: board.viewport,
  };
}

/** Stable identity for change detection, insensitive to key order. */
export function atlasBoardSignature(board: AtlasBoard): string {
  return JSON.stringify(serializeAtlasBoard(board), (_key, value: unknown) => {
    if (value == null || typeof value !== 'object' || Array.isArray(value)) return value;
    const record = value as Record<string, unknown>;
    return Object.fromEntries(
      Object.keys(record)
        .sort()
        .map((key) => [key, record[key]]),
    );
  });
}

// ---------------------------------------------------------------------------
// Runnability
// ---------------------------------------------------------------------------

function hasTriggerEdge(board: AtlasBoard, from: string, to: string): boolean {
  return board.edges.some((edge) => edge.kind === 'trigger' && edge.from === from && edge.to === to);
}

/**
 * Explain why a board cannot start a run, or return an empty string.
 *
 * The message is the product's own: only the plan → build → review starter
 * chain is runnable today. The editor shows it rather than hiding Run, so an
 * operator who drew something else learns what is missing instead of wondering
 * why a button disappeared.
 */
export function runnableBlockedReason(board: AtlasBoard): string {
  const plan = componentByRole(board, 'plan');
  const build = componentByRole(board, 'build');
  const review = componentByRole(board, 'review');
  if (plan === null || build === null || review === null) {
    return 'Custom graph runtime coming — Run only works on the plan → build → review starter chain';
  }
  if (plan.id === build.id || build.id === review.id || plan.id === review.id) {
    return 'Custom graph runtime coming — Run only works on the plan → build → review starter chain';
  }
  if (!hasTriggerEdge(board, plan.id, build.id) || !hasTriggerEdge(board, build.id, review.id)) {
    return 'Custom graph runtime coming — Run only works on the plan → build → review starter chain';
  }
  return '';
}

export function isRunnable(board: AtlasBoard): boolean {
  return runnableBlockedReason(board) === '';
}

// ---------------------------------------------------------------------------
// Editing
// ---------------------------------------------------------------------------

export function withComponent(board: AtlasBoard, id: string, patch: Partial<AtlasComponent>): AtlasBoard {
  return {
    ...board,
    components: board.components.map((component) =>
      component.id === id ? { ...component, ...patch } : component,
    ),
  };
}

/** Add a freeform seat. It is editable immediately and runnable only once wired. */
export function addComponent(board: AtlasBoard): AtlasBoard {
  const taken = new Set(board.components.map((component) => component.id));
  const id = uniqueId(`seat-${board.components.length + 1}`, taken);
  return { ...board, components: [...board.components, newAgentSeat(id, null, board.components.length)] };
}

/** Remove a seat and every edge that referenced it. */
export function removeComponent(board: AtlasBoard, id: string): AtlasBoard {
  return {
    ...board,
    components: board.components.filter((component) => component.id !== id),
    edges: board.edges.filter((edge) => edge.from !== id && edge.to !== id),
  };
}

/** Connect two seats. A duplicate or self edge is refused, not silently added. */
export function connect(
  board: AtlasBoard,
  from: string,
  to: string,
  kind: AtlasEdgeKind = 'trigger',
): AtlasBoard {
  if (from === to) return board;
  if (componentById(board, from) === null || componentById(board, to) === null) return board;
  if (board.edges.some((edge) => edge.kind === kind && edge.from === from && edge.to === to)) {
    return board;
  }
  const edge = kind === 'feedback' ? feedbackEdge(from, to) : triggerEdge(from, to);
  return { ...board, edges: [...board.edges, edge] };
}

export function disconnect(board: AtlasBoard, edgeId: string): AtlasBoard {
  return { ...board, edges: board.edges.filter((edge) => edge.id !== edgeId) };
}

/** Resize a committee, preserving the members that survive. */
export function setCommitteeSize(board: AtlasBoard, id: string, size: number): AtlasBoard {
  const component = componentById(board, id);
  if (component === null) return board;
  const bounded = Math.max(1, Math.min(ATLAS_MAX_WORKERS, Math.floor(size)));
  const seats = component.seats.slice(0, bounded);
  while (seats.length < bounded) {
    seats.push({
      id: workerSeatId(component.id, seats.length),
      role: 'worker',
      ...defaultSeatForRole(component.legacyRole),
      preserved: {},
    });
  }
  const normalized = normalizeWorkers(seats, component.id, component.legacyRole);
  const main = normalized.find((worker) => worker.role === 'main') ?? normalized[0];
  return withComponent(board, id, {
    seats: normalized,
    seat: { vendor: main.vendor, model: main.model, effort: main.effort, permission: main.permission },
  });
}

/** Apply a profile edit to one committee member. */
export function setWorkerSeat(
  board: AtlasBoard,
  componentId: string,
  workerId: string,
  seat: AtlasSeat,
): AtlasBoard {
  const component = componentById(board, componentId);
  if (component === null) return board;
  const seats = component.seats.map((worker) => (worker.id === workerId ? { ...worker, ...seat } : worker));
  const main = seats.find((worker) => worker.role === 'main') ?? seats[0];
  return withComponent(board, componentId, {
    seats,
    seat: { vendor: main.vendor, model: main.model, effort: main.effort, permission: main.permission },
  });
}

/** Apply a batch of layout updates in one pass so a cluster stays rigid. */
export function applyLayoutUpdates(
  board: AtlasBoard,
  updates: ReadonlyArray<readonly [string, (layout: CanvasNodeBox) => CanvasNodeBox]>,
): AtlasBoard {
  if (updates.length === 0) return board;
  const components = board.components.map((component) => ({ ...component }));
  const byId = new Map(components.map((component) => [component.id, component]));
  for (const [nodeId, update] of updates) {
    const { componentId, role } = parseAtlasNodeId(nodeId);
    const component = byId.get(componentId);
    if (component === undefined) continue;
    if (role === 'prompt') component.promptBox = update(component.promptBox);
    else if (role === 'info') component.infoBox = update(component.infoBox);
    else component.box = update(component.box);
  }
  return { ...board, components };
}

export function layoutForNode(board: AtlasBoard, nodeId: string): CanvasNodeBox {
  const { componentId, role } = parseAtlasNodeId(nodeId);
  const component = componentById(board, componentId);
  if (component === null) return defaultTerminalBox(0);
  if (role === 'prompt') return component.promptBox;
  if (role === 'info') return component.infoBox;
  return component.box;
}

/** The smallest rectangle containing every rendered node. */
export function boardContentExtent(board: AtlasBoard): { width: number; height: number } {
  const boxes = atlasNodeIds(board).map((id) => layoutForNode(board, id));
  if (boxes.length === 0) return { width: ATLAS_WORLD.width, height: ATLAS_WORLD.height };
  return {
    width: Math.max(...boxes.map((box) => box.x + box.width)),
    height: Math.max(...boxes.map((box) => box.y + box.height)),
  };
}

export { hasSeat };
