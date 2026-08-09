import type { TerminalClientFrame } from '@/plugins/canvas/contracts';

export const TERMINAL_SUBPROTOCOL = 'coslash.terminal.v1';
export const TERMINAL_TOKEN_PREFIX = 'coslash.token.';
const TOKEN_STORAGE_KEY = 'coslash-api-token';

type SocketLike = Pick<
  WebSocket,
  'binaryType' | 'close' | 'send' | 'readyState' | 'protocol' | 'onopen' | 'onmessage' | 'onclose' | 'onerror'
>;
export type SocketFactory = (url: string, protocols: string[]) => SocketLike;

export function terminalSocketURL(terminalId: string, location: Pick<Location, 'protocol' | 'host'>): string {
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${scheme}//${location.host}/api/terminals/${encodeURIComponent(terminalId)}/ws`;
}

export function terminalProtocols(token: string): string[] {
  if (!/^[A-Za-z0-9._~-]+$/.test(token)) throw new Error('A valid API token is required.');
  return [TERMINAL_SUBPROTOCOL, `${TERMINAL_TOKEN_PREFIX}${token}`];
}

export type TerminalConnectionSnapshot = {
  status: 'connecting' | 'open' | 'reconnecting' | 'closed' | 'error';
  output: string;
  attempts: number;
};

export type TerminalConnection = {
  snapshot(): TerminalConnectionSnapshot;
  subscribe(listener: () => void): () => void;
  input(data: string): void;
  resize(cols: number, rows: number): void;
  close(): void;
};

export type TerminalConnectionOptions = {
  terminalId: string;
  token?: string;
  location?: Pick<Location, 'protocol' | 'host'>;
  socketFactory?: SocketFactory;
  setTimer?: (callback: () => void, delay: number) => unknown;
  clearTimer?: (handle: unknown) => void;
  maxOutput?: number;
  maxReconnects?: number;
};

export function createTerminalConnection(options: TerminalConnectionOptions): TerminalConnection {
  const location = options.location ?? window.location;
  const token = options.token ?? window.sessionStorage.getItem(TOKEN_STORAGE_KEY) ?? '';
  const factory = options.socketFactory ?? ((url, protocols) => new WebSocket(url, protocols));
  const setTimer = options.setTimer ?? ((callback, delay) => window.setTimeout(callback, delay));
  const clearTimer = options.clearTimer ?? ((handle) => window.clearTimeout(handle as number));
  const maxOutput = options.maxOutput ?? 512_000;
  const maxReconnects = options.maxReconnects ?? 5;
  const listeners = new Set<() => void>();
  const decoder = new TextDecoder();
  let socket: SocketLike | null = null;
  let timer: unknown = null;
  let closed = false;
  let state: TerminalConnectionSnapshot = { status: 'connecting', output: '', attempts: 0 };

  const publish = (patch: Partial<TerminalConnectionSnapshot>) => {
    state = { ...state, ...patch };
    for (const listener of listeners) listener();
  };

  const append = (data: unknown) => {
    let chunk = '';
    if (typeof data === 'string') chunk = data;
    else if (data instanceof ArrayBuffer) chunk = decoder.decode(new Uint8Array(data));
    else if (ArrayBuffer.isView(data)) chunk = decoder.decode(data as ArrayBufferView<ArrayBuffer>);
    if (chunk === '') return;
    const output = `${state.output}${chunk}`;
    publish({ output: output.slice(Math.max(0, output.length - maxOutput)) });
  };

  const connect = () => {
    if (closed) return;
    publish({ status: state.attempts === 0 ? 'connecting' : 'reconnecting' });
    try {
      socket = factory(terminalSocketURL(options.terminalId, location), terminalProtocols(token));
      socket.binaryType = 'arraybuffer';
      socket.onopen = () => {
        if (socket?.protocol !== TERMINAL_SUBPROTOCOL) {
          socket?.close();
          socket = null;
          publish({ status: 'error' });
          return;
        }
        publish({ status: 'open' });
      };
      socket.onmessage = (event) => append(event.data);
      socket.onerror = () => publish({ status: 'error' });
      socket.onclose = () => {
        socket = null;
        if (closed) return publish({ status: 'closed' });
        const attempts = state.attempts + 1;
        if (attempts > maxReconnects) return publish({ attempts, status: 'error' });
        publish({ attempts, status: 'reconnecting' });
        timer = setTimer(connect, Math.min(250 * 2 ** (attempts - 1), 4_000));
      };
    } catch {
      publish({ status: 'error' });
    }
  };

  const send = (frame: TerminalClientFrame) => {
    if (socket === null || socket.readyState !== 1) throw new Error('Terminal is not connected.');
    if (frame.type === 'input') {
      if (
        frame.data === '' ||
        frame.data.includes('\u0000') ||
        new TextEncoder().encode(frame.data).length > 32 << 10
      )
        throw new Error('Terminal input is invalid.');
    } else if (
      !Number.isInteger(frame.cols) ||
      !Number.isInteger(frame.rows) ||
      frame.cols < 20 ||
      frame.cols > 500 ||
      frame.rows < 5 ||
      frame.rows > 200
    ) {
      throw new Error('Terminal dimensions are invalid.');
    }
    socket.send(JSON.stringify(frame));
  };

  connect();
  return {
    snapshot: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    input: (data) => send({ type: 'input', data }),
    resize: (cols, rows) => send({ type: 'resize', cols, rows }),
    close() {
      closed = true;
      if (timer !== null) clearTimer(timer);
      timer = null;
      socket?.close();
      socket = null;
      publish({ status: 'closed' });
    },
  };
}
