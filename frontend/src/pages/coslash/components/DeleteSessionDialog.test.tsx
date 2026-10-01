import { type ComponentProps, type ReactElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { type Session } from '../lib/session';
import { DeleteSessionDialog, DeleteSessionDialogContent } from './DeleteSessionDialog';

const hooks = vi.hoisted(() => {
  let cursor = 0;
  let values: unknown[] = [];
  let dependencies: (readonly unknown[] | undefined)[] = [];
  let effects: (() => void)[] = [];
  const changed = (index: number, next?: readonly unknown[]) => {
    const prior = dependencies[index];
    return (
      prior == null ||
      next == null ||
      prior.length !== next.length ||
      prior.some((value, dependencyIndex) => !Object.is(value, next[dependencyIndex]))
    );
  };
  return {
    reset() {
      values = [];
      dependencies = [];
    },
    useState(initial: unknown) {
      const index = cursor++;
      if (!(index in values)) values[index] = typeof initial === 'function' ? initial() : initial;
      return [
        values[index],
        (next: unknown) => {
          values[index] = typeof next === 'function' ? next(values[index]) : next;
        },
      ];
    },
    useRef(initial: unknown) {
      const index = cursor++;
      if (!(index in values)) values[index] = { current: initial };
      return values[index];
    },
    useMemo(factory: () => unknown, next?: readonly unknown[]) {
      const index = cursor++;
      if (changed(index, next)) values[index] = factory();
      dependencies[index] = next;
      return values[index];
    },
    useEffect(effect: () => void, next?: readonly unknown[]) {
      const index = cursor++;
      if (changed(index, next)) effects.push(effect);
      dependencies[index] = next;
    },
    render<Props>(component: (props: Props) => unknown, props: Props) {
      cursor = 0;
      effects = [];
      const root = component(props);
      for (const effect of effects) effect();
      return root;
    },
  };
});

vi.mock('react', async (original) => ({
  ...(await original<typeof import('react')>()),
  useState: hooks.useState,
  useRef: hooks.useRef,
  useEffect: hooks.useEffect,
}));

const local = { sourceId: 'local', agent: 'codex', id: 'same/id', name: 'Exact session' } as Session;
const props = { session: local, onClose: vi.fn(), onDeleted: vi.fn(), onRefresh: vi.fn() };
function render(overrides: Partial<ComponentProps<typeof DeleteSessionDialog>> = {}) {
  const root = hooks.render(DeleteSessionDialog, { ...props, ...overrides }) as ReactElement<{
    children: ReactElement<{ children: ReactElement<ComponentProps<typeof DeleteSessionDialogContent>> }>;
  }>;
  return root.props.children.props.children.props;
}
function deferred() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
beforeEach(() => {
  hooks.reset();
  vi.clearAllMocks();
  vi.stubGlobal('window', { location: { hash: '' }, sessionStorage: { getItem: () => 'run-token' } });
  vi.stubGlobal('fetch', vi.fn());
});

describe('shared delete confirmation', () => {
  it('names the exact local identity, warns to close first, and keeps errors accessible at narrow widths', () => {
    const markup = renderToStaticMarkup(
      <Dialog>
        <DeleteSessionDialogContent
          session={local}
          pending={false}
          error="Close the session first."
          onCancel={() => {}}
          onConfirm={async () => {}}
        />
      </Dialog>,
    );
    expect(markup).toContain('Exact session');
    expect(markup).toContain('local');
    expect(markup).toContain('codex');
    expect(markup).toContain('same/id');
    expect(markup).toContain('cannot be undone');
    expect(markup).toContain('Close the session');
    expect(markup).toContain('role="alert"');
    expect(markup).toContain('break-all');
    expect(markup).toContain('Cancel');
  });
  it('disables only confirmation while pending and keeps cancel focusable', () => {
    const markup = renderToStaticMarkup(
      <Dialog>
        <DeleteSessionDialogContent
          session={local}
          pending
          error={null}
          onCancel={() => {}}
          onConfirm={async () => {}}
        />
      </Dialog>,
    );
    expect(markup).toMatch(/<button[^>]* disabled=""[^>]*>Deleting session/);
    expect(markup).toMatch(/<button(?![^>]* disabled="")[^>]*>Cancel/);
  });
  it.each([true, false])('restores focus to the connected trigger or search fallback: %s', (isConnected) => {
    const trigger = { isConnected, focus: vi.fn() };
    const search = { focus: vi.fn() };
    vi.stubGlobal('document', { querySelector: () => search });
    const root = hooks.render(DeleteSessionDialog, {
      ...props,
      returnFocusRef: { current: trigger as unknown as HTMLElement },
    }) as ReactElement<ComponentProps<typeof Dialog>>;
    const content = root.props.children as ReactElement<ComponentProps<typeof DialogContent>>;
    const event = new Event('closeAutoFocus', { cancelable: true });
    content.props.onCloseAutoFocus?.(event);
    expect(event.defaultPrevented).toBe(true);
    expect(trigger.focus).toHaveBeenCalledTimes(isConnected ? 1 : 0);
    expect(search.focus).toHaveBeenCalledTimes(isConnected ? 0 : 1);
    root.props.onOpenChange?.(false);
    expect(props.onClose).toHaveBeenCalledOnce();
    expect(fetch).not.toHaveBeenCalled();
  });
  it('cancel makes no request', () => {
    render().onCancel();
    expect(fetch).not.toHaveBeenCalled();
    expect(props.onClose).toHaveBeenCalledOnce();
  });
  it('rejects remote identities even if invoked directly', async () => {
    await render({ session: { ...local, sourceId: 'remote' } }).onConfirm();
    expect(fetch).not.toHaveBeenCalled();
  });
  it('sends exact identity and token once, then reloads and completes on 204', async () => {
    const request = deferred();
    vi.mocked(fetch).mockReturnValue(request.promise);
    const confirm = render().onConfirm;
    const completion = confirm();
    await confirm();
    expect(fetch).toHaveBeenCalledOnce();
    expect(fetch).toHaveBeenCalledWith(
      '/api/sessions?source=local&agent=codex&id=same%2Fid',
      expect.objectContaining({ method: 'DELETE', headers: expect.any(Headers) }),
    );
    const headers = vi.mocked(fetch).mock.calls[0][1]!.headers as Headers;
    expect(headers.get('X-Coslash-Token')).toBe('run-token');
    expect(render().pending).toBe(true);
    request.resolve(new Response(null, { status: 204 }));
    await completion;
    expect(props.onRefresh).toHaveBeenCalledOnce();
    expect(props.onDeleted).toHaveBeenCalledWith(local);
  });
  it.each([400, 404, 409, 500])('preserves the row and safe error on %s, allowing retry', async (status) => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ code: 'session_active', error: 'Close the session first.' }), { status }),
    );
    await render().onConfirm();
    expect(render().error).toBe('Close the session first.');
    expect(render().pending).toBe(false);
    expect(props.onDeleted).not.toHaveBeenCalled();
    expect(props.onRefresh).not.toHaveBeenCalled();
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 204 }));
    await render().onConfirm();
    expect(props.onDeleted).toHaveBeenCalledOnce();
  });
  it('keeps a newer deletion pending when an older request completes', async () => {
    const oldRequest = deferred();
    const newRequest = deferred();
    vi.mocked(fetch).mockReturnValueOnce(oldRequest.promise).mockReturnValueOnce(newRequest.promise);
    const first = render();
    const oldCompletion = first.onConfirm();
    first.onCancel();
    render({ session: null });
    const next = { ...local, agent: 'claude' };
    const newCompletion = render({ session: next }).onConfirm();
    oldRequest.resolve(new Response(null, { status: 204 }));
    await oldCompletion;
    expect(render({ session: next }).pending).toBe(true);
    expect(props.onDeleted).not.toHaveBeenCalled();
    newRequest.resolve(new Response(null, { status: 204 }));
    await newCompletion;
    expect(props.onDeleted).toHaveBeenCalledExactlyOnceWith(next);
  });
  it('treats an unexpected success status as a failure', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('not a safe API error', { status: 200 }));
    await render().onConfirm();
    expect(render().error).toBe('Session could not be deleted (200).');
    expect(props.onDeleted).not.toHaveBeenCalled();
    expect(props.onRefresh).not.toHaveBeenCalled();
  });
  it.each([204, 409])(
    'ignores stale %s completion after cancel and opening another identity',
    async (status) => {
      const request = deferred();
      vi.mocked(fetch).mockReturnValue(request.promise);
      const content = render();
      const completion = content.onConfirm();
      content.onCancel();
      const next = { ...local, agent: 'claude' };
      render({ session: null });
      render({ session: next });
      request.resolve(
        status === 204
          ? new Response(null, { status })
          : new Response(JSON.stringify({ code: 'session_active', error: 'Old error' }), { status }),
      );
      await completion;
      expect(render({ session: next }).error).toBeNull();
      expect(render({ session: next }).pending).toBe(false);
      expect(props.onDeleted).not.toHaveBeenCalled();
    },
  );
  it('does not send a duplicate after cancel and reopening the same identity while pending', async () => {
    const request = deferred();
    vi.mocked(fetch).mockReturnValue(request.promise);
    const content = render();
    const completion = content.onConfirm();
    content.onCancel();
    render({ session: null });
    await render().onConfirm();
    expect(fetch).toHaveBeenCalledOnce();
    request.resolve(new Response(null, { status: 204 }));
    await completion;
  });
});
