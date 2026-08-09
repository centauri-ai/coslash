import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { CanvasPersistenceError } from '@/plugins/canvas/api/persistence';
import { FROZEN_SESSION_CANVAS_FIXTURES } from '@/plugins/canvas/session/fixtures';
import { SessionCanvas, SessionCanvasWorkbench } from '@/plugins/canvas/session/SessionCanvas';
import { defaultSessionWorkspace } from '@/plugins/canvas/session/workspace';

const persistence = { status: 'saved' as const, dirty: false, error: null };

describe('Session Canvas workbench', () => {
  it('renders all nine useful nodes from a frozen backend fixture', () => {
    const html = renderToStaticMarkup(
      <SessionCanvasWorkbench
        detail={FROZEN_SESSION_CANVAS_FIXTURES.claude}
        workspace={defaultSessionWorkspace()}
        persistence={persistence}
        terminal={null}
        analyses={new Map()}
        onWorkspaceChange={vi.fn()}
        onRename={vi.fn()}
        onLaunchTerminal={vi.fn()}
        onTerminalInput={vi.fn()}
        onTerminalResize={vi.fn()}
        onReconnectTerminal={vi.fn()}
        onStopTerminal={vi.fn()}
        onSendNote={vi.fn()}
        onResolvePersistence={vi.fn()}
        onReloadWorkspace={vi.fn()}
        onLoadFile={vi.fn()}
        onAnalyze={vi.fn()}
        onFork={vi.fn()}
        onPromote={vi.fn()}
      />,
    );
    for (const label of [
      'session',
      'goal &amp; outcome',
      'plan',
      'timeline',
      'context',
      'worktree',
      'next move · live terminal',
      'my note',
      'turn inspector',
    ]) {
      expect(html).toContain(`${label} Canvas component`);
    }
    expect(html).toContain('claude:shared-id');
    expect(html).toContain('Open terminal');
    expect(html).toContain('Analyze');
    expect(html).toContain('aria-label="Rename session"');
    expect(html).not.toContain('Selected session node.');
  });

  it('keeps identical Claude and Codex ids visibly distinct', () => {
    const render = (detail: (typeof FROZEN_SESSION_CANVAS_FIXTURES)['claude']) =>
      renderToStaticMarkup(
        <SessionCanvasWorkbench
          detail={detail}
          workspace={defaultSessionWorkspace()}
          persistence={persistence}
          terminal={null}
          analyses={new Map()}
          onWorkspaceChange={vi.fn()}
          onRename={vi.fn()}
          onLaunchTerminal={vi.fn()}
          onTerminalInput={vi.fn()}
          onTerminalResize={vi.fn()}
          onReconnectTerminal={vi.fn()}
          onStopTerminal={vi.fn()}
          onSendNote={vi.fn()}
          onResolvePersistence={vi.fn()}
          onReloadWorkspace={vi.fn()}
          onLoadFile={vi.fn()}
          onAnalyze={vi.fn()}
          onFork={vi.fn()}
          onPromote={vi.fn()}
        />,
      );
    expect(render(FROZEN_SESSION_CANVAS_FIXTURES.claude)).toContain('data-session-key="claude:shared-id"');
    expect(render(FROZEN_SESSION_CANVAS_FIXTURES.codex)).toContain('data-session-key="codex:shared-id"');
  });

  it('renders explicit empty selection and disabled AI states', () => {
    const empty = renderToStaticMarkup(<SessionCanvas session={null} freshnessVersion={0} />);
    expect(empty).toContain('Select a session for Canvas');
    const disabled = renderToStaticMarkup(
      <SessionCanvasWorkbench
        detail={FROZEN_SESSION_CANVAS_FIXTURES.claude}
        workspace={defaultSessionWorkspace()}
        persistence={persistence}
        terminal={null}
        analyses={new Map([[1, 'disabled']])}
        aiDisabled
        onWorkspaceChange={vi.fn()}
        onRename={vi.fn()}
        onLaunchTerminal={vi.fn()}
        onTerminalInput={vi.fn()}
        onTerminalResize={vi.fn()}
        onReconnectTerminal={vi.fn()}
        onStopTerminal={vi.fn()}
        onSendNote={vi.fn()}
        onResolvePersistence={vi.fn()}
        onReloadWorkspace={vi.fn()}
        onLoadFile={vi.fn()}
        onAnalyze={vi.fn()}
        onFork={vi.fn()}
        onPromote={vi.fn()}
      />,
    );
    expect(disabled).toContain('AI analysis is disabled.');
    expect(disabled).toContain('disabled=""');
  });

  it('surfaces guarded action failures in the loaded workbench', () => {
    const html = renderToStaticMarkup(
      <SessionCanvasWorkbench
        detail={FROZEN_SESSION_CANVAS_FIXTURES.claude}
        workspace={defaultSessionWorkspace()}
        persistence={persistence}
        terminal={null}
        analyses={new Map()}
        actionError="Note delivery failed safely."
        onWorkspaceChange={vi.fn()}
        onRename={vi.fn()}
        onLaunchTerminal={vi.fn()}
        onTerminalInput={vi.fn()}
        onTerminalResize={vi.fn()}
        onReconnectTerminal={vi.fn()}
        onStopTerminal={vi.fn()}
        onSendNote={vi.fn()}
        onResolvePersistence={vi.fn()}
        onReloadWorkspace={vi.fn()}
        onLoadFile={vi.fn()}
        onAnalyze={vi.fn()}
        onFork={vi.fn()}
        onPromote={vi.fn()}
      />,
    );
    expect(html).toContain('role="alert"');
    expect(html).toContain('Note delivery failed safely.');
  });

  it('renders interactive input for an open native terminal', () => {
    const html = renderToStaticMarkup(
      <SessionCanvasWorkbench
        detail={FROZEN_SESSION_CANVAS_FIXTURES.claude}
        workspace={defaultSessionWorkspace()}
        persistence={persistence}
        terminal={{ status: 'open', output: 'ready', attempts: 0 }}
        analyses={new Map()}
        onWorkspaceChange={vi.fn()}
        onRename={vi.fn()}
        onLaunchTerminal={vi.fn()}
        onTerminalInput={vi.fn()}
        onTerminalResize={vi.fn()}
        onReconnectTerminal={vi.fn()}
        onStopTerminal={vi.fn()}
        onSendNote={vi.fn()}
        onResolvePersistence={vi.fn()}
        onReloadWorkspace={vi.fn()}
        onLoadFile={vi.fn()}
        onAnalyze={vi.fn()}
        onFork={vi.fn()}
        onPromote={vi.fn()}
      />,
    );
    expect(html).toContain('aria-label="Terminal input"');
    expect(html).toContain('ready');
    expect(html).toContain('Stop');
  });

  it('offers both safe recovery choices for a workspace revision conflict', () => {
    const conflict = new CanvasPersistenceError('Workspace changed elsewhere.', 409, {
      ok: false,
      code: 'REVISION_CONFLICT',
      error: 'Workspace changed elsewhere.',
      actualRevision: 3,
    });
    const html = renderToStaticMarkup(
      <SessionCanvasWorkbench
        detail={FROZEN_SESSION_CANVAS_FIXTURES.claude}
        workspace={defaultSessionWorkspace()}
        persistence={{ status: 'conflict', dirty: true, error: conflict }}
        terminal={null}
        analyses={new Map()}
        onWorkspaceChange={vi.fn()}
        onRename={vi.fn()}
        onLaunchTerminal={vi.fn()}
        onTerminalInput={vi.fn()}
        onTerminalResize={vi.fn()}
        onReconnectTerminal={vi.fn()}
        onStopTerminal={vi.fn()}
        onSendNote={vi.fn()}
        onResolvePersistence={vi.fn()}
        onReloadWorkspace={vi.fn()}
        onLoadFile={vi.fn()}
        onAnalyze={vi.fn()}
        onFork={vi.fn()}
        onPromote={vi.fn()}
      />,
    );
    expect(html).toContain('Keep local');
    expect(html).toContain('Reload server');
  });
});
