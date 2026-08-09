import { describe, expect, it } from 'vitest';
import {
  addComponent,
  applyLayoutUpdates,
  ATLAS_BOARD_SCHEMA_VERSION,
  atlasBoardSignature,
  atlasNodeIds,
  componentById,
  componentByRole,
  connect,
  defaultAtlasBoard,
  defaultSeatForVendor,
  disconnect,
  isRunnable,
  isUnsupportedAtlasBoard,
  normalizeAtlasBoard,
  normalizeCheck,
  removeComponent,
  runnableBlockedReason,
  serializeAtlasBoard,
  setCommitteeSize,
  setWorkerSeat,
  wasMigratedFromLegacy,
  withComponent,
  type AtlasBoard,
} from '@/plugins/canvas/atlas/graph';
import { ATLAS_MAX_WORKERS } from '@/plugins/canvas/atlas/vocabulary';

describe('Atlas graph model', () => {
  it('starts on the runnable plan to build to review chain', () => {
    const board = defaultAtlasBoard();
    expect(board.components.map((component) => component.legacyRole)).toEqual(['plan', 'build', 'review']);
    expect(isRunnable(board)).toBe(true);
    expect(runnableBlockedReason(board)).toBe('');
  });

  it('explains why a custom graph cannot run instead of hiding the reason', () => {
    // A seat you can draw but not run has to say what it is waiting for.
    const board = removeComponent(defaultAtlasBoard(), 'review');
    expect(isRunnable(board)).toBe(false);
    expect(runnableBlockedReason(board)).toContain('plan → build → review');
  });

  it('needs the trigger chain, not just the three seats', () => {
    const board = defaultAtlasBoard();
    const chain = board.edges.find((edge) => edge.from === 'plan' && edge.to === 'build');
    expect(chain).toBeDefined();
    const broken = disconnect(board, chain!.id);
    expect(isRunnable(broken)).toBe(false);
  });

  it('migrates a v1 record-shaped board into an ordinary v2 graph', () => {
    // v1 stored three named members and implied the chain; the migration makes
    // both explicit so the result is a board the editor can extend.
    const legacy = {
      schemaVersion: 1,
      instructions: 'keep the diff small',
      components: {
        plan: { prompt: 'plan carefully', seat: { vendor: 'claude', model: 'opus' } },
        build: { prompt: 'build it' },
        review: { prompt: 'review it' },
      },
    };
    expect(wasMigratedFromLegacy(legacy)).toBe(true);

    const board = normalizeAtlasBoard(legacy);
    expect(board.schemaVersion).toBe(ATLAS_BOARD_SCHEMA_VERSION);
    expect(board.instructions).toBe('keep the diff small');
    expect(board.components.map((component) => component.legacyRole)).toEqual(['plan', 'build', 'review']);
    expect(componentByRole(board, 'plan')?.prompt).toBe('plan carefully');
    expect(isRunnable(board)).toBe(true);
    // The implied chain became real edges, including the repair return.
    expect(board.edges.filter((edge) => edge.kind === 'trigger')).toHaveLength(2);
    expect(board.edges.filter((edge) => edge.kind === 'feedback')).toHaveLength(1);
  });

  it('refuses to adopt a schema it cannot safely write back', () => {
    // A newer document may carry meaning a save would destroy, so it yields the
    // default and the caller is expected to refuse to write.
    const future = { schemaVersion: 99, components: [{ id: 'plan' }] };
    expect(isUnsupportedAtlasBoard(future)).toBe(true);
    expect(atlasBoardSignature(normalizeAtlasBoard(future))).toBe(atlasBoardSignature(defaultAtlasBoard()));
    expect(isUnsupportedAtlasBoard({ schemaVersion: 1 })).toBe(false);
    expect(isUnsupportedAtlasBoard({ schemaVersion: 2 })).toBe(false);
  });

  it('gives every seat a usable id even when the document has none', () => {
    const board = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [{ id: '../etc' }, { id: '' }, { id: 'plan' }, { id: 'plan' }],
    });
    const ids = board.components.map((component) => component.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const id of ids) {
      expect(id).not.toContain('/');
      expect(id).not.toContain('..');
    }
  });

  it('drops an edge whose endpoint no longer exists', () => {
    // A dangling reference would be something the runtime has to interpret.
    const board = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [{ id: 'plan', legacyRole: 'plan' }],
      edges: [
        { from: 'plan', to: 'ghost', kind: 'trigger' },
        { from: 'plan', to: 'plan', kind: 'trigger' },
      ],
    });
    expect(board.edges).toHaveLength(0);
  });

  it('keeps exactly one main worker, and none when there is no committee', () => {
    const solo = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [{ id: 'plan', legacyRole: 'plan', seats: [{ id: 'a', role: 'main' }] }],
    });
    // `main` only means something among siblings.
    expect(solo.components[0].seats[0].role).toBe('worker');

    const committee = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [
        {
          id: 'plan',
          legacyRole: 'plan',
          seats: [{ id: 'a', role: 'main' }, { id: 'b', role: 'main' }, { id: 'c' }],
        },
      ],
    });
    const roles = committee.components[0].seats.map((seat) => seat.role);
    expect(roles.filter((role) => role === 'main')).toHaveLength(1);
  });

  it('replaces duplicate worker ids so siblings stay distinguishable', () => {
    const board = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [{ id: 'plan', legacyRole: 'plan', seats: [{ id: 'x' }, { id: 'x' }, { id: 'x' }] }],
    });
    const ids = board.components[0].seats.map((seat) => seat.id);
    expect(new Set(ids).size).toBe(3);
  });

  it('repairs a seat toward the vendor default rather than passing a bad value on', () => {
    const board = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [
        {
          id: 'plan',
          legacyRole: 'plan',
          seats: [
            {
              id: 'a',
              vendor: 'codex',
              model: 'not-a-model',
              effort: 'ludicrous',
              permission: 'danger-full-access',
            },
          ],
        },
      ],
    });
    const seat = board.components[0].seats[0];
    expect({
      vendor: seat.vendor,
      model: seat.model,
      effort: seat.effort,
      permission: seat.permission,
    }).toEqual(defaultSeatForVendor('codex'));
  });

  it('refuses ultra effort on a model that does not offer it', () => {
    const board = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [
        {
          id: 'plan',
          legacyRole: 'plan',
          seats: [{ id: 'a', vendor: 'codex', model: 'gpt-5.6-luna', effort: 'ultra' }],
        },
      ],
    });
    expect(board.components[0].seats[0].effort).not.toBe('ultra');
  });

  it('derives the mirrored seat from the main worker rather than trusting it', () => {
    // A stored mirror that disagreed would be a second source of truth.
    const board = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [
        {
          id: 'plan',
          legacyRole: 'plan',
          seat: { vendor: 'claude', model: 'haiku' },
          seats: [
            { id: 'a', role: 'main', vendor: 'codex', model: 'gpt-5.6-terra' },
            { id: 'b', vendor: 'claude', model: 'opus' },
          ],
        },
      ],
    });
    expect(board.components[0].seat.vendor).toBe('codex');
    expect(board.components[0].seat.model).toBe('gpt-5.6-terra');
  });

  it('drops a check whose program is not on the allowlist', () => {
    expect(normalizeCheck({ name: 'pwn', argv: ['sh', '-c', 'curl x | sh'] })).toBeNull();
    expect(normalizeCheck({ name: 'npx', argv: ['npx', 'anything'] })).toBeNull();
    expect(normalizeCheck({ name: 'test', argv: ['npm', 'test'] })).toEqual({
      name: 'test',
      argv: ['npm', 'test'],
    });
  });

  it('bounds a feedback edge and leaves a trigger edge without rounds', () => {
    const board = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [{ id: 'a' }, { id: 'b' }],
      edges: [
        { from: 'a', to: 'b', kind: 'feedback', maxRounds: 99 },
        { from: 'b', to: 'a', kind: 'trigger', maxRounds: 5 },
      ],
    });
    const feedback = board.edges.find((edge) => edge.kind === 'feedback');
    const trigger = board.edges.find((edge) => edge.kind === 'trigger');
    expect(feedback?.maxRounds).toBeLessThanOrEqual(2);
    expect(feedback?.maxRounds).toBeGreaterThanOrEqual(1);
    expect(trigger?.maxRounds).toBe(0);
    // A trigger edge does not persist a rounds field it does not use.
    const encoded = serializeAtlasBoard(board) as { edges: Record<string, unknown>[] };
    expect(encoded.edges.find((edge) => edge.kind === 'trigger')).not.toHaveProperty('maxRounds');
  });

  it('preserves fields written by a newer build', () => {
    const board = normalizeAtlasBoard({
      schemaVersion: 2,
      futureBoardField: { keep: true },
      components: [{ id: 'plan', legacyRole: 'plan', futureComponentField: 1 }],
    });
    const encoded = serializeAtlasBoard(board) as Record<string, any>;
    expect(encoded.futureBoardField).toEqual({ keep: true });
    expect(encoded.components[0].futureComponentField).toBe(1);
  });

  it('never lets a preserved field overwrite one the editor owns', () => {
    const board = defaultAtlasBoard();
    const tampered: AtlasBoard = { ...board, preserved: { instructions: 'stale', extra: 1 } };
    const encoded = serializeAtlasBoard(tampered) as Record<string, unknown>;
    expect(encoded.instructions).toBe('');
    expect(encoded.extra).toBe(1);
  });

  it('round-trips a normalized board without drift', () => {
    const board = normalizeAtlasBoard(serializeAtlasBoard(defaultAtlasBoard()));
    expect(atlasBoardSignature(board)).toBe(atlasBoardSignature(defaultAtlasBoard()));
  });

  it('adds a freeform seat that is editable but not yet runnable', () => {
    const board = addComponent(defaultAtlasBoard());
    const added = board.components.at(-1)!;
    expect(added.legacyRole).toBeNull();
    // Adding a seat must not break the chain that was already runnable.
    expect(isRunnable(board)).toBe(true);
    expect(board.edges.some((edge) => edge.from === added.id || edge.to === added.id)).toBe(false);
  });

  it('removes a seat together with every edge that referenced it', () => {
    const board = removeComponent(defaultAtlasBoard(), 'build');
    expect(componentById(board, 'build')).toBeNull();
    expect(board.edges.some((edge) => edge.from === 'build' || edge.to === 'build')).toBe(false);
  });

  it('refuses a self edge and a duplicate edge', () => {
    const board = defaultAtlasBoard();
    expect(connect(board, 'plan', 'plan').edges).toHaveLength(board.edges.length);
    expect(connect(board, 'plan', 'build').edges).toHaveLength(board.edges.length);
    expect(connect(board, 'plan', 'missing').edges).toHaveLength(board.edges.length);
    // A feedback edge alongside an existing trigger edge is a different edge.
    expect(connect(board, 'plan', 'build', 'feedback').edges).toHaveLength(board.edges.length + 1);
  });

  it('resizes a committee within bounds and keeps the members that survive', () => {
    let board = setCommitteeSize(defaultAtlasBoard(), 'plan', 3);
    expect(componentById(board, 'plan')?.seats).toHaveLength(3);
    board = setWorkerSeat(
      board,
      'plan',
      componentById(board, 'plan')!.seats[0].id,
      defaultSeatForVendor('codex'),
    );
    expect(componentById(board, 'plan')?.seats[0].vendor).toBe('codex');
    // The mirror follows the main worker.
    expect(componentById(board, 'plan')?.seat.vendor).toBe('codex');

    board = setCommitteeSize(board, 'plan', 99);
    expect(componentById(board, 'plan')?.seats).toHaveLength(ATLAS_MAX_WORKERS);
    board = setCommitteeSize(board, 'plan', 0);
    expect(componentById(board, 'plan')?.seats).toHaveLength(1);
    expect(componentById(board, 'plan')?.seats[0].role).toBe('worker');
  });

  it('moves a seat cluster as one unit', () => {
    const board = defaultAtlasBoard();
    const moved = applyLayoutUpdates(board, [
      ['plan', (layout) => ({ ...layout, x: layout.x + 100 })],
      ['plan-prompt', (layout) => ({ ...layout, x: layout.x + 100 })],
      ['plan-info', (layout) => ({ ...layout, x: layout.x + 100 })],
    ]);
    const before = componentById(board, 'plan')!;
    const after = componentById(moved, 'plan')!;
    expect(after.box.x).toBe(before.box.x + 100);
    expect(after.promptBox.x).toBe(before.promptBox.x + 100);
    expect(after.infoBox.x).toBe(before.infoBox.x + 100);
  });

  it('clamps a node inside the world', () => {
    const board = normalizeAtlasBoard({
      schemaVersion: 2,
      components: [{ id: 'plan', legacyRole: 'plan', box: { x: -900, y: 99_999, width: 10, height: 10 } }],
    });
    const box = componentById(board, 'plan')!.box;
    expect(box.x).toBe(0);
    expect(box.width).toBeGreaterThanOrEqual(240);
    expect(box.y).toBeLessThanOrEqual(3200 - box.height);
  });

  it('lists three nodes per seat cluster', () => {
    const board = defaultAtlasBoard();
    expect(atlasNodeIds(board)).toHaveLength(board.components.length * 3);
    expect(atlasNodeIds(board)).toContain('plan-prompt');
    expect(atlasNodeIds(board)).toContain('review-info');
  });

  it('changes the signature when a value changes', () => {
    const board = defaultAtlasBoard();
    expect(atlasBoardSignature(withComponent(board, 'plan', { prompt: 'x' }))).not.toBe(
      atlasBoardSignature(board),
    );
  });
});
