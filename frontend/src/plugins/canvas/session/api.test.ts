import { describe, expect, it, vi } from 'vitest';
import {
  analyzeTurn,
  bracketedPaste,
  forkSession,
  loadSessionDetail,
  loadSessionFile,
  renameSession,
  sendTerminalInput,
  sessionDetailPath,
  stopTerminal,
} from '@/plugins/canvas/session/api';
import { sessionCanvasFixture } from '@/plugins/canvas/session/fixtures';

const identity = { agent: 'claude/code', id: 'session one' };

describe('Session Canvas guarded API client', () => {
  it('encodes the composite identity in every session route', () => {
    expect(sessionDetailPath(identity)).toBe('/api/canvas/sessions/claude%2Fcode/session%20one');
  });

  it('loads detail through the injected guarded client', async () => {
    const detail = sessionCanvasFixture();
    const fetch = vi.fn(async () => new Response(JSON.stringify(detail), { status: 200 }));
    await expect(loadSessionDetail(identity, fetch)).resolves.toEqual(detail);
    expect(fetch).toHaveBeenCalledWith('/api/canvas/sessions/claude%2Fcode/session%20one');
  });

  it('loads a server-scoped file preview with an encoded path', async () => {
    const fetch = vi.fn(
      async () =>
        new Response('preview', {
          status: 200,
          headers: { 'Content-Type': 'text/plain; charset=utf-8' },
        }),
    );
    await expect(loadSessionFile(identity, 'src/a file.ts', fetch)).resolves.toEqual({
      content: 'preview',
      contentType: 'text/plain; charset=utf-8',
    });
    expect(fetch).toHaveBeenCalledWith(
      '/api/canvas/sessions/claude%2Fcode/session%20one/files?path=src%2Fa+file.ts',
    );
  });

  it('uses bounded JSON actions for rename, fork, analysis, and terminal input', async () => {
    const fetch = vi.fn(async (path: string) => {
      if (path.endsWith('/fork'))
        return new Response(
          JSON.stringify({ ok: true, terminal: { terminalId: 'term', state: 'running', writable: true } }),
          { status: 200 },
        );
      if (path.includes('/analysis'))
        return new Response(
          JSON.stringify({
            ok: true,
            cacheKey: 'key',
            cached: false,
            analysis: {
              intention: 'verify',
              planSummary: 'run tests',
              status: 'active',
              findings: [],
              issues: [],
            },
          }),
          { status: 200 },
        );
      return new Response(JSON.stringify({ ok: true, name: 'Renamed' }), { status: 200 });
    });
    await renameSession(identity, 'Renamed', fetch);
    await forkSession(identity, { prompt: 'Try this' }, fetch);
    await analyzeTurn(identity, 3, fetch);
    await sendTerminalInput('terminal/1', 'note', fetch);
    await stopTerminal('terminal/1', fetch);
    expect(fetch.mock.calls.map(([path]) => path)).toEqual([
      '/api/canvas/sessions/claude%2Fcode/session%20one/name',
      '/api/canvas/sessions/claude%2Fcode/session%20one/fork',
      '/api/canvas/sessions/claude%2Fcode/session%20one/turns/3/analysis',
      '/api/terminals/terminal%2F1/input',
      '/api/terminals/terminal%2F1/stop',
    ]);
  });

  it('surfaces stable safe error codes', async () => {
    const fetch = async () =>
      new Response(
        JSON.stringify({ ok: false, code: 'ANALYSIS_DISABLED', error: 'turn analysis is disabled' }),
        {
          status: 409,
        },
      );
    await expect(analyzeTurn(identity, 1, fetch)).rejects.toMatchObject({
      code: 'ANALYSIS_DISABLED',
      status: 409,
    });
  });

  it('wraps notes in bracketed paste without retaining null bytes', () => {
    expect(bracketedPaste('hello\u0000 world')).toBe('\u001b[200~hello world\u001b[201~');
  });
});
