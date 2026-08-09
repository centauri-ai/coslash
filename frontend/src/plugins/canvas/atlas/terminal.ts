// The single point where Atlas binds to the native terminal transport.
//
// The guarded PTY/WebSocket client lives in `@/plugins/canvas/session/terminal`.
// It is product-agnostic — nothing in it knows about Session Canvas — so its
// architectural home is the shared Canvas layer, alongside geometry and chrome.
// Moving it there is a shared-file change outside this task's ownership, so it
// is imported read-only here, through one chokepoint that makes the eventual
// promotion a one-line change. DaGama does the same.

export {
  createTerminalConnection,
  terminalKeyData,
  TERMINAL_SUBPROTOCOL,
  type TerminalConnection,
  type TerminalConnectionOptions,
  type TerminalConnectionSnapshot,
  type TerminalKeyDescriptor,
} from '@/plugins/canvas/session/terminal';

export { bracketedPaste } from '@/plugins/canvas/session/api';
