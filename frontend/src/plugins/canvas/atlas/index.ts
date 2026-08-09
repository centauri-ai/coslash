// Public surface of Atlas Canvas.
//
// The destination is not registered with the plugin shell here: shared lazy
// registration and destination readiness are Task 19 integration work, and an
// incomplete destination must stay hidden until then. DaGama does the same.

export { AtlasBoardView, AtlasCanvas, type AtlasBoardViewProps } from '@/plugins/canvas/atlas/AtlasCanvas';
export * from '@/plugins/canvas/atlas/api';
export * from '@/plugins/canvas/atlas/fixtures';
export * from '@/plugins/canvas/atlas/graph';
export * from '@/plugins/canvas/atlas/run-session';
export * from '@/plugins/canvas/atlas/runs';
export * from '@/plugins/canvas/atlas/session';
export * from '@/plugins/canvas/atlas/types';
export * from '@/plugins/canvas/atlas/vocabulary';
