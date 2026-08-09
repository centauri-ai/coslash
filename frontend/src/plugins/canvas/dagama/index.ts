// Public surface of DaGama Canvas.
//
// The destination is not registered with the plugin shell here: shared lazy
// registration and destination readiness are Task 19 integration work, and an
// incomplete destination must stay hidden until then.

export {
  DaGamaBoardView,
  DaGamaCanvas,
  type DaGamaBoardViewProps,
} from '@/plugins/canvas/dagama/DaGamaCanvas';
export * from '@/plugins/canvas/dagama/api';
export * from '@/plugins/canvas/dagama/board';
export * from '@/plugins/canvas/dagama/fixtures';
export * from '@/plugins/canvas/dagama/preferences';
export * from '@/plugins/canvas/dagama/run-session';
export * from '@/plugins/canvas/dagama/runs';
export * from '@/plugins/canvas/dagama/session';
export * from '@/plugins/canvas/dagama/types';
export * from '@/plugins/canvas/dagama/vocabulary';
