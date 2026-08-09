// The single point where DaGama binds to the native terminal transport.
//
// The guarded PTY/WebSocket client (subprotocol negotiation, bounded frames,
// reconnect backoff, output cap) currently lives in
// `@/plugins/canvas/session/terminal`. It is product-agnostic — nothing in it
// knows about Session Canvas — so its architectural home is the shared Canvas
// layer, alongside geometry, wires, and chrome. Moving it there is a shared-file
// change outside this task's ownership, so it is imported read-only here.
//
// Every DaGama import of the transport goes through this module, which makes the
// eventual promotion to `@/plugins/canvas/shared` a one-line change rather than
// a sweep. See the Task 13 report for the follow-up.

export {
  createTerminalConnection,
  terminalKeyData,
  TERMINAL_SUBPROTOCOL,
  type TerminalConnection,
  type TerminalConnectionOptions,
  type TerminalConnectionSnapshot,
  type TerminalKeyDescriptor,
} from '@/plugins/canvas/session/terminal';

/**
 * Wrap text as a bracketed paste.
 *
 * A seat terminal hosts an interactive agent CLI, and pasting a multi-line
 * message without the brackets makes the CLI treat every newline as a submit —
 * which sends the first line and strands the rest. The implementation is shared
 * with Session Canvas through the same read-only import as the transport.
 */
export { bracketedPaste } from '@/plugins/canvas/session/api';
