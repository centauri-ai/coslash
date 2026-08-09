import { describe, expect, it } from 'vitest';
import {
  createTerminalConnection,
  TERMINAL_SUBPROTOCOL,
  TERMINAL_TOKEN_PREFIX,
  terminalProtocols,
  terminalSocketURL,
  type SocketFactory,
} from '@/plugins/canvas/session/terminal';

class FakeSocket {
  binaryType = '';
  readyState = 0;
  protocol = TERMINAL_SUBPROTOCOL;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  sent: string[] = [];
  close() {
    this.readyState = 3;
  }
  send(value: string | ArrayBufferLike | Blob | ArrayBufferView) {
    this.sent.push(String(value));
  }
  open() {
    this.readyState = 1;
    this.onopen?.({} as Event);
  }
  message(data: string) {
    this.onmessage?.({ data } as MessageEvent);
  }
  disconnect() {
    this.readyState = 3;
    this.onclose?.({} as CloseEvent);
  }
}

describe('guarded terminal WebSocket helper', () => {
  it('uses the authenticated same-origin URL and static/token protocols', () => {
    expect(terminalSocketURL('terminal/1', { protocol: 'https:', host: '127.0.0.1:8787' })).toBe(
      'wss://127.0.0.1:8787/api/terminals/terminal%2F1/ws',
    );
    expect(terminalProtocols('secret')).toEqual([TERMINAL_SUBPROTOCOL, `${TERMINAL_TOKEN_PREFIX}secret`]);
    expect(() => terminalProtocols('bad token')).toThrow();
  });

  it('sends bounded JSON frames, retains output, and reconnects after a disconnect', () => {
    const sockets: FakeSocket[] = [];
    const protocols: string[][] = [];
    const timers: (() => void)[] = [];
    const factory: SocketFactory = (_url, offered) => {
      protocols.push(offered);
      const socket = new FakeSocket();
      sockets.push(socket);
      return socket as unknown as WebSocket;
    };
    const connection = createTerminalConnection({
      terminalId: 'term',
      token: 'secret',
      location: { protocol: 'http:', host: 'localhost:8787' },
      socketFactory: factory,
      setTimer: (callback) => {
        timers.push(callback);
        return callback;
      },
      clearTimer: () => undefined,
      maxReconnects: 2,
    });
    sockets[0].open();
    connection.input('hello');
    connection.resize(120, 40);
    expect(() => connection.input('')).toThrow('Terminal input is invalid.');
    expect(() => connection.resize(10, 40)).toThrow('Terminal dimensions are invalid.');
    sockets[0].message('ready');
    expect(sockets[0].sent).toEqual([
      JSON.stringify({ type: 'input', data: 'hello' }),
      JSON.stringify({ type: 'resize', cols: 120, rows: 40 }),
    ]);
    expect(connection.snapshot()).toMatchObject({ status: 'open', output: 'ready' });
    sockets[0].disconnect();
    expect(connection.snapshot().status).toBe('reconnecting');
    timers[0]();
    expect(sockets).toHaveLength(2);
    expect(protocols[1]).toEqual(protocols[0]);
    connection.close();
    expect(connection.snapshot().status).toBe('closed');
  });
});
