// The DaGama board: a reusable workflow definition, not a node graph.
//
// The pipeline shape is the product, so it is not data — `DAGAMA_COMPONENT_IDS`
// is the order, and normalization guarantees a board always holds exactly those
// six components. That removes every "missing component", "duplicate component",
// and "what order?" case from the runtime.
//
// This module is pure: no fetch, no storage, no DOM. The same validation runs
// again in Go before anything is executed.
//
// ---------------------------------------------------------------------------
// Round-trip preservation
//
// The backend board (`collector/internal/plugins/canvas/dagama/board.go`) holds
// only *executable* configuration — identity, seats, checks, publish — and keeps
// every field it does not recognise in an `extra` map so an older build cannot
// silently delete configuration a newer one wrote. The Canvas presentation
// fields (prompt cards, node boxes, viewport, board instructions) live in that
// preserved space.
//
// Consequently this model must be just as careful in the other direction: a
// board loaded from the server carries identity fields (`id`, `projectId`,
// `projectPath`, `createdAt`, …) that the editor never shows and must never
// drop, because the server needs them to locate and validate the document. Every
// unknown key is captured in `preserved` and re-emitted by `serializeDaGamaBoard`.
// ---------------------------------------------------------------------------

import {
  DAGAMA_COMPONENT_IDS,
  DAGAMA_MAX_ARGV_TOKENS,
  DAGAMA_MAX_CHECKS,
  effortsFor,
  hasSeat,
  modelsFor,
  permissionsFor,
  validArgvToken,
  validBaseBranch,
  validCheckCommand,
  validCheckName,
  type DaGamaComponentId,
  type DaGamaSeatComponentId,
  type DaGamaVendor,
} from '@/plugins/canvas/dagama/vocabulary';
import type { CanvasNodeBox } from '@/plugins/canvas/shared';

export const DAGAMA_BOARD_SCHEMA_VERSION = 1;

export type DaGamaSeat = {
  vendor: DaGamaVendor;
  model: string;
  effort: string;
  permission: string;
};

/** A Verify check. `argv` is a token list, never a shell string. */
export type DaGamaCheck = { name: string; argv: string[] };

export type DaGamaPublishConfig = {
  /** Empty means the selected project's checked-out branch, resolved at preflight. */
  base: string;
  draft: boolean;
};

export type DaGamaComponent = {
  id: DaGamaComponentId;
  /** Run-specific steering for this stage. It can change how the work is done; it cannot change what counts as done. */
  prompt: string;
  seat: DaGamaSeat | null;
  checks: DaGamaCheck[];
  publish: DaGamaPublishConfig | null;
  /** Seat terminal, or the compact card for a non-seat stage. */
  box: CanvasNodeBox;
  /** Seat companions. Null for Intake / Verify / Publish. */
  promptBox: CanvasNodeBox | null;
  infoBox: CanvasNodeBox | null;
  /** Compose draft for send-into-terminal. */
  promptDraft: string;
  /** Fields this build does not understand, re-emitted verbatim on save. */
  preserved: Readonly<Record<string, unknown>>;
};

// Canvas node ids: one per component, plus `-prompt` / `-info` satellites for
// each agent seat. Hyphen suffixes (not colons) so `canvas-node-<id>` stays a
// valid CSS class.
export type DaGamaSeatRole = 'terminal' | 'prompt' | 'info';
export type DaGamaNodeId =
  DaGamaComponentId | `${DaGamaSeatComponentId}-prompt` | `${DaGamaSeatComponentId}-info`;

export function seatPromptNodeId(id: DaGamaSeatComponentId): `${DaGamaSeatComponentId}-prompt` {
  return `${id}-prompt`;
}

export function seatInfoNodeId(id: DaGamaSeatComponentId): `${DaGamaSeatComponentId}-info` {
  return `${id}-info`;
}

export function parseDaGamaNodeId(id: DaGamaNodeId): {
  componentId: DaGamaComponentId;
  role: DaGamaSeatRole;
} {
  if (id.endsWith('-prompt')) {
    return { componentId: id.slice(0, -'-prompt'.length) as DaGamaSeatComponentId, role: 'prompt' };
  }
  if (id.endsWith('-info')) {
    return { componentId: id.slice(0, -'-info'.length) as DaGamaSeatComponentId, role: 'info' };
  }
  return { componentId: id as DaGamaComponentId, role: 'terminal' };
}

export type DaGamaViewport = { zoom: number; panX: number; panY: number };

export type DaGamaBoard = {
  schemaVersion: typeof DAGAMA_BOARD_SCHEMA_VERSION;
  /** Persistent project conventions, applied to every seat in every run. */
  instructions: string;
  components: Record<DaGamaComponentId, DaGamaComponent>;
  viewport: DaGamaViewport;
  /** Board-level fields this build does not understand, including server identity. */
  preserved: Readonly<Record<string, unknown>>;
};

export const DAGAMA_WORLD = { width: 5200, height: 3200 } as const;
export const DAGAMA_ZOOM_BOUNDS = { min: 0.25, max: 2, step: 0.1 } as const;
// Slightly tighter than the Session workbench so a full seat cluster fits the
// first viewport.
export const DAGAMA_DEFAULT_ZOOM = 0.55;
export const DAGAMA_NODE_MIN_WIDTH = 240;
export const DAGAMA_NODE_MIN_HEIGHT = 120;

// Compact stages (Intake / Verify / Publish) stay card-sized. Agent seats are
// full-scale terminals with prompt + info companions stacked beneath.
const COMPACT_WIDTH = 360;
const COMPACT_HEIGHT = 340;
const SEAT_TERM_WIDTH = 780;
const SEAT_TERM_HEIGHT = 520;
const SEAT_PROMPT_WIDTH = 380;
const SEAT_PROMPT_HEIGHT = 220;
const SEAT_INFO_WIDTH = 380;
const SEAT_INFO_HEIGHT = 280;
const COMPANION_GAP = 20;
const CLUSTER_STACK_GAP = 28;
const STAGE_GAP = 90;
const RAIL_X = 120;
const RAIL_Y = 160;

export const DAGAMA_MAX_PROMPT_LENGTH = 8_000;
export const DAGAMA_MAX_INSTRUCTIONS_LENGTH = 8_000;
export const DAGAMA_MAX_PROMPT_DRAFT_LENGTH = 8_000;

function stageWidth(id: DaGamaComponentId): number {
  return hasSeat(id) ? SEAT_TERM_WIDTH : COMPACT_WIDTH;
}

/** Cards are laid out on a rail in pipeline order; a fresh board always reads L→R. */
export function defaultBox(id: DaGamaComponentId): CanvasNodeBox {
  const index = DAGAMA_COMPONENT_IDS.indexOf(id);
  let x = RAIL_X;
  for (let i = 0; i < index; i++) x += stageWidth(DAGAMA_COMPONENT_IDS[i]) + STAGE_GAP;
  return {
    x,
    y: RAIL_Y,
    width: stageWidth(id),
    height: hasSeat(id) ? SEAT_TERM_HEIGHT : COMPACT_HEIGHT,
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

// Review deliberately runs the other vendor: reviewing with a different model
// family is the cheapest available diversity lever, and at one seat per
// component it costs nothing extra.
function defaultSeat(id: DaGamaSeatComponentId): DaGamaSeat {
  return defaultSeatForVendor(id === 'review' ? 'codex' : 'claude');
}

/** A complete, runnable profile for a vendor, so a vendor switch never leaves a partial tuple. */
export function defaultSeatForVendor(vendor: DaGamaVendor): DaGamaSeat {
  return vendor === 'codex'
    ? { vendor: 'codex', model: 'gpt-5.6-terra', effort: 'high', permission: 'workspace-write' }
    : { vendor: 'claude', model: 'opus', effort: 'high', permission: 'acceptEdits' };
}

export function defaultComponent(id: DaGamaComponentId): DaGamaComponent {
  const box = defaultBox(id);
  const seated = hasSeat(id);
  return {
    id,
    prompt: '',
    seat: seated ? defaultSeat(id) : null,
    // No checks by default: we cannot know a project's commands, and inventing
    // one that fails on first run is worse than a Verify that reports 'skipped'.
    checks: [],
    publish: id === 'publish' ? { base: '', draft: true } : null,
    box,
    promptBox: seated ? defaultPromptBox(box) : null,
    infoBox: seated ? defaultInfoBox(box) : null,
    promptDraft: '',
    preserved: {},
  };
}

export function defaultDaGamaBoard(): DaGamaBoard {
  return {
    schemaVersion: DAGAMA_BOARD_SCHEMA_VERSION,
    instructions: '',
    components: Object.fromEntries(DAGAMA_COMPONENT_IDS.map((id) => [id, defaultComponent(id)])) as Record<
      DaGamaComponentId,
      DaGamaComponent
    >,
    viewport: { zoom: DAGAMA_DEFAULT_ZOOM, panX: 0, panY: 0 },
    preserved: {},
  };
}

// ---------------------------------------------------------------------------
// Normalization. Every field is repaired toward the default rather than
// rejected, except values that could reach a command line — an unrecognised
// model, effort, permission, or argv token falls back to the default instead of
// being passed through, because neither CLI validates them for us.
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

/** Everything in `source` that is not a field this build owns. */
function preservedFields(source: Record<string, unknown>, known: readonly string[]): Record<string, unknown> {
  const extra: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(source)) {
    if (!known.includes(key)) extra[key] = value;
  }
  return extra;
}

const BOARD_FIELDS = ['schemaVersion', 'instructions', 'components', 'viewport'] as const;
const COMPONENT_FIELDS = [
  'id',
  'prompt',
  'seat',
  'checks',
  'publish',
  'box',
  'promptBox',
  'infoBox',
  'promptDraft',
] as const;

function normalizeBox(
  value: unknown,
  fallback: CanvasNodeBox,
  minWidth = DAGAMA_NODE_MIN_WIDTH,
  minHeight = DAGAMA_NODE_MIN_HEIGHT,
): CanvasNodeBox {
  const box = asRecord(value) as Partial<CanvasNodeBox>;
  const width = Math.max(minWidth, Math.min(DAGAMA_WORLD.width, finiteNumber(box.width, fallback.width)));
  const height = Math.max(
    minHeight,
    Math.min(DAGAMA_WORLD.height, finiteNumber(box.height, fallback.height)),
  );
  return {
    x: Math.max(0, Math.min(DAGAMA_WORLD.width - width, finiteNumber(box.x, fallback.x))),
    y: Math.max(0, Math.min(DAGAMA_WORLD.height - height, finiteNumber(box.y, fallback.y))),
    width,
    height,
    collapsed: typeof box.collapsed === 'boolean' ? box.collapsed : false,
    locked: typeof box.locked === 'boolean' ? box.locked : false,
  };
}

function migrateSeatTerminalBox(
  component: Record<string, unknown>,
  id: DaGamaSeatComponentId,
): CanvasNodeBox {
  const hasCompanions = component.promptBox != null || component.infoBox != null;
  const fallback = defaultBox(id);
  const box = normalizeBox(component.box, fallback);
  // Upgrade undersized pre-cluster terminals only on first companion synthesis.
  if (!hasCompanions && box.width < SEAT_TERM_WIDTH * 0.85) {
    return { ...fallback, collapsed: box.collapsed, locked: box.locked };
  }
  return box;
}

/** Resolve the persisted box for any canvas node id (component or companion). */
export function layoutForNode(
  components: Record<DaGamaComponentId, DaGamaComponent>,
  nodeId: DaGamaNodeId,
): CanvasNodeBox {
  const { componentId, role } = parseDaGamaNodeId(nodeId);
  const component = components[componentId];
  if (role === 'prompt' && component.promptBox) return component.promptBox;
  if (role === 'info' && component.infoBox) return component.infoBox;
  return component.box;
}

/** Every node id a board currently renders, in pipeline then companion order. */
export function daGamaNodeIds(components: Record<DaGamaComponentId, DaGamaComponent>): DaGamaNodeId[] {
  const ids: DaGamaNodeId[] = [];
  for (const id of DAGAMA_COMPONENT_IDS) {
    ids.push(id);
    if (hasSeat(id) && components[id].promptBox && components[id].infoBox) {
      ids.push(seatPromptNodeId(id), seatInfoNodeId(id));
    }
  }
  return ids;
}

/**
 * Apply a batch of layout updates in one pass.
 *
 * Dragging a seat terminal moves its prompt and info companions with it, and
 * the shared interaction hook reports all three in a single call. Folding them
 * into one board value keeps the cluster rigid: applying them one at a time
 * would let an intermediate render show the companions lagging the terminal.
 */
export function applyLayoutUpdates(
  board: DaGamaBoard,
  updates: ReadonlyArray<readonly [DaGamaNodeId, (layout: CanvasNodeBox) => CanvasNodeBox]>,
): DaGamaBoard {
  if (updates.length === 0) return board;
  const components = { ...board.components };
  for (const [nodeId, update] of updates) {
    const { componentId, role } = parseDaGamaNodeId(nodeId);
    const component = components[componentId];
    if (role === 'prompt' && component.promptBox) {
      components[componentId] = { ...component, promptBox: update(component.promptBox) };
    } else if (role === 'info' && component.infoBox) {
      components[componentId] = { ...component, infoBox: update(component.infoBox) };
    } else {
      components[componentId] = { ...component, box: update(component.box) };
    }
  }
  return { ...board, components };
}

/** Replace one component, leaving the rest of the board untouched. */
export function withComponent(
  board: DaGamaBoard,
  id: DaGamaComponentId,
  patch: Partial<DaGamaComponent>,
): DaGamaBoard {
  return { ...board, components: { ...board.components, [id]: { ...board.components[id], ...patch } } };
}

/** The smallest rectangle containing every rendered node. */
export function boardContentExtent(board: DaGamaBoard): { width: number; height: number } {
  const boxes = daGamaNodeIds(board.components).map((id) => layoutForNode(board.components, id));
  if (boxes.length === 0) return { width: DAGAMA_WORLD.width, height: DAGAMA_WORLD.height };
  const right = Math.max(...boxes.map((box) => box.x + box.width));
  const bottom = Math.max(...boxes.map((box) => box.y + box.height));
  // Measured from the world origin, because the scrolling stage anchors there.
  return { width: right, height: bottom };
}

function normalizeSeat(value: unknown, id: DaGamaSeatComponentId): DaGamaSeat {
  const fallback = defaultSeat(id);
  const seat = asRecord(value);
  const vendor: DaGamaVendor =
    seat.vendor === 'codex' ? 'codex' : seat.vendor === 'claude' ? 'claude' : fallback.vendor;
  // Switching vendor invalidates the whole vocabulary, so a mismatched model or
  // permission resolves against the vendor's default rather than being kept.
  const vendorDefault = vendor === fallback.vendor ? fallback : defaultSeatForVendor(vendor);
  const model = oneOf(seat.model, modelsFor(vendor), vendorDefault.model);
  return {
    vendor,
    model,
    effort: oneOf(seat.effort, effortsFor(vendor, model), vendorDefault.effort),
    permission: oneOf(seat.permission, permissionsFor(vendor), vendorDefault.permission),
  };
}

export function normalizeCheck(value: unknown): DaGamaCheck | null {
  if (value == null || typeof value !== 'object') return null;
  const check = value as Record<string, unknown>;
  const name = asString(check.name).trim();
  if (!validCheckName(name)) return null;
  const argv = check.argv;
  // Over-length argv is REJECTED, not truncated. Truncating would run a
  // different, shorter command than the one configured — the same reason a
  // single unusable token drops the whole check rather than being skipped.
  if (!Array.isArray(argv) || argv.length === 0 || argv.length > DAGAMA_MAX_ARGV_TOKENS) return null;
  // argv[0] must be a known build tool. Checks are exec'd without a shell, so
  // metacharacters are inert — but that is worth nothing if argv[0] is itself a
  // shell, and a board file can be shared or arrive in a pull request.
  if (!validCheckCommand(argv[0])) return null;
  if (!argv.every(validArgvToken)) return null;
  return { name, argv: argv as string[] };
}

function normalizePublish(value: unknown): DaGamaPublishConfig {
  const publish = asRecord(value);
  const base = asString(publish.base).trim();
  return {
    base: validBaseBranch(base) ? base : '',
    // Draft defaults to true; a board must opt out explicitly.
    draft: typeof publish.draft === 'boolean' ? publish.draft : true,
  };
}

function normalizeComponent(value: unknown, id: DaGamaComponentId): DaGamaComponent {
  const component = asRecord(value);
  const seated = hasSeat(id);
  const box = seated ? migrateSeatTerminalBox(component, id) : normalizeBox(component.box, defaultBox(id));
  return {
    id,
    prompt: clampedText(component.prompt, DAGAMA_MAX_PROMPT_LENGTH),
    seat: seated ? normalizeSeat(component.seat, id) : null,
    checks:
      id === 'verify' && Array.isArray(component.checks)
        ? component.checks
            .slice(0, DAGAMA_MAX_CHECKS)
            .map(normalizeCheck)
            .filter((check): check is DaGamaCheck => check != null)
        : [],
    publish: id === 'publish' ? normalizePublish(component.publish) : null,
    box,
    promptBox: seated ? normalizeBox(component.promptBox, defaultPromptBox(box)) : null,
    infoBox: seated ? normalizeBox(component.infoBox, defaultInfoBox(box)) : null,
    promptDraft: seated ? clampedText(component.promptDraft, DAGAMA_MAX_PROMPT_DRAFT_LENGTH) : '',
    preserved: preservedFields(component, COMPONENT_FIELDS),
  };
}

function normalizeViewport(value: unknown): DaGamaViewport {
  const viewport = asRecord(value);
  return {
    zoom: Math.max(
      DAGAMA_ZOOM_BOUNDS.min,
      Math.min(DAGAMA_ZOOM_BOUNDS.max, finiteNumber(viewport.zoom, DAGAMA_DEFAULT_ZOOM)),
    ),
    panX: finiteNumber(viewport.panX, 0),
    panY: finiteNumber(viewport.panY, 0),
  };
}

/**
 * Repair any input into a complete, valid board.
 *
 * A board whose `schemaVersion` this build does not recognise yields the default
 * board rather than a half-adopted one — a document from another Canvas product
 * is rejected, not partially merged. A board that simply omits the version (the
 * backend model does not require the editor's presentation envelope) is treated
 * as the current version, so a server document still opens.
 */
export function normalizeDaGamaBoard(value: unknown): DaGamaBoard {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) return defaultDaGamaBoard();
  const candidate = value as Record<string, unknown>;
  const version = candidate.schemaVersion;
  if (version !== undefined && version !== DAGAMA_BOARD_SCHEMA_VERSION) return defaultDaGamaBoard();
  const components = asRecord(candidate.components);
  return {
    schemaVersion: DAGAMA_BOARD_SCHEMA_VERSION,
    instructions: clampedText(candidate.instructions, DAGAMA_MAX_INSTRUCTIONS_LENGTH),
    components: Object.fromEntries(
      DAGAMA_COMPONENT_IDS.map((id) => [id, normalizeComponent(components[id], id)]),
    ) as Record<DaGamaComponentId, DaGamaComponent>,
    viewport: normalizeViewport(candidate.viewport),
    // `components` keys outside the fixed pipeline are intentionally dropped:
    // the pipeline is closed, and re-emitting a seventh stage would let a board
    // claim a shape the runtime cannot execute.
    preserved: preservedFields(candidate, BOARD_FIELDS),
  };
}

/**
 * Encode a board for the wire, re-emitting preserved fields.
 *
 * A preserved field never overwrites one this build owns: if a newer document
 * carried both, the value the editor actually showed and validated wins.
 */
export function serializeDaGamaBoard(board: DaGamaBoard): Record<string, unknown> {
  const components: Record<string, unknown> = {};
  for (const id of DAGAMA_COMPONENT_IDS) {
    const component = board.components[id];
    components[id] = {
      ...component.preserved,
      id: component.id,
      prompt: component.prompt,
      ...(component.seat ? { seat: component.seat } : {}),
      ...(id === 'verify' ? { checks: component.checks } : {}),
      ...(component.publish ? { publish: component.publish } : {}),
      box: component.box,
      ...(component.promptBox ? { promptBox: component.promptBox } : {}),
      ...(component.infoBox ? { infoBox: component.infoBox } : {}),
      ...(hasSeat(id) ? { promptDraft: component.promptDraft } : {}),
    };
  }
  return {
    ...board.preserved,
    schemaVersion: board.schemaVersion,
    instructions: board.instructions,
    components,
    viewport: board.viewport,
  };
}

/**
 * Stable identity for change detection.
 *
 * Autosave compares boards by value, and `JSON.stringify` on the live object
 * would let a key-order difference read as an edit. Serializing through the
 * wire encoder and sorting keys makes "same board" mean the same bytes.
 */
export function daGamaBoardSignature(board: DaGamaBoard): string {
  return JSON.stringify(serializeDaGamaBoard(board), (_key, value: unknown) => {
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
// Presentation metadata. Kept beside the model so the pipeline's description
// lives in one place rather than being spelled out again in the board.
// ---------------------------------------------------------------------------

export type DaGamaComponentMeta = {
  title: string;
  /** What this component is for, in one line, shown on an unconfigured card. */
  purpose: string;
  /** The artifacts it is contracted to produce. */
  outputs: string[];
};

export const DAGAMA_COMPONENT_META: Record<DaGamaComponentId, DaGamaComponentMeta> = {
  intake: {
    title: 'INTAKE',
    purpose: 'Snapshot the source and form the problem statement',
    outputs: ['SOURCE.md', 'source.json', 'PROBLEM.md'],
  },
  plan: {
    title: 'PLAN',
    purpose: 'Resolve scope and produce the implementation plan',
    outputs: ['PLAN.md'],
  },
  build: {
    title: 'BUILD',
    purpose: 'Implement the plan, or address current findings',
    outputs: ['IMPLEMENTATION.md', 'CHANGESET.patch', 'change.json'],
  },
  verify: {
    title: 'VERIFY',
    purpose: 'Run the project checks against a frozen revision',
    outputs: ['verification.json'],
  },
  review: {
    title: 'REVIEW',
    purpose: 'Review the verified revision and emit a verdict',
    outputs: ['REVIEW.md', 'review.json'],
  },
  publish: {
    title: 'PUBLISH',
    purpose: 'Commit, push, and open one pull request',
    outputs: ['publication.json'],
  },
};

// The pipeline's forward edges, derived rather than stored — the shape is the
// product, so a board cannot describe a different one.
export const DAGAMA_FLOW: ReadonlyArray<readonly [DaGamaComponentId, DaGamaComponentId]> = [
  ['intake', 'plan'],
  ['plan', 'build'],
  ['build', 'verify'],
  ['verify', 'review'],
  ['review', 'publish'],
];

// The repair edges: a failed verification or a changes-requested review returns
// to Build. Drawn differently so the loop is visible.
export const DAGAMA_REPAIR_FLOW: ReadonlyArray<readonly [DaGamaComponentId, DaGamaComponentId]> = [
  ['verify', 'build'],
  ['review', 'build'],
];
