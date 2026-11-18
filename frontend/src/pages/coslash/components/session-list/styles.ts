import {
  sessionTotalTokens,
  STATUSES,
  sumKnown,
  type Session,
  type StatusKey,
} from '@/pages/coslash/lib/session';

export const styles = {
  shell:
    'coslash-shell flex h-svh flex-col overflow-hidden bg-coslash-bg text-ui leading-[1.45] text-coslash-ink antialiased',
  header:
    'flex min-h-15 shrink-0 flex-wrap items-center justify-between gap-3.5 border-b border-coslash-line bg-coslash-surface px-5 py-2 max-compact:px-3',
  banner:
    'flex items-center gap-2.5 border-b border-danger-fg bg-danger-bg px-5 py-2.5 text-xs text-danger-fg [&>svg]:size-4',
  sidebar:
    'coslash-sidebar w-[214px] shrink-0 no-scrollbar overflow-y-auto border-r border-coslash-line bg-coslash-surface px-3 py-5 max-sidebar:hidden',
  sideHeading:
    'flex min-h-[30px] w-full cursor-pointer items-center gap-[7px] rounded-[7px] px-2.5 py-1.5 text-left text-meta font-[650] tracking-[.09em] text-coslash-muted uppercase transition-colors hover:bg-coslash-soft hover:text-coslash-ink',
  facet:
    'flex min-h-8 w-full cursor-pointer items-center gap-[9px] rounded-[7px] px-2.5 py-[7px] text-left text-xs leading-[1.45] text-coslash-muted transition-colors hover:bg-coslash-soft [&>svg]:size-3.5',
  main: 'coslash-main flex min-h-0 min-w-0 flex-1 flex-col px-6 pt-5 pb-5 max-sidebar:w-full max-sidebar:p-4',
  search:
    'flex h-12 w-full min-w-0 items-center gap-1.5 rounded-[9px] border border-coslash-line bg-coslash-surface px-3.5 focus-within:border-coslash-accent focus-within:shadow-[0_0_0_3px_var(--coslash-tint)]',
  chip: 'inline-flex h-6 shrink-0 items-center gap-1 rounded-md border border-coslash-tint-line bg-coslash-tint pr-1 pl-2 text-meta font-[550] whitespace-nowrap text-coslash-accent-ink',
  segmented: 'inline-flex rounded-[10px] bg-coslash-soft p-0.5',
  tableWrap: 'min-h-0 flex-1 overflow-auto rounded-[10px] border border-coslash-line bg-coslash-surface',
  head: 'sticky top-0 z-12 border-b border-coslash-line bg-coslash-surface text-left text-meta font-[650] tracking-[.07em] text-coslash-muted uppercase',
  headButton:
    'flex min-h-[34px] w-full cursor-pointer items-center gap-[5px] px-2.5 py-[9px] text-left font-[inherit] tracking-[inherit] uppercase transition-colors hover:bg-coslash-soft hover:text-coslash-ink [&>svg]:size-3',
  cell: 'overflow-hidden px-2.5 py-2 align-middle text-cell',
  empty: 'flex min-h-60 flex-col items-center justify-center gap-2 px-6 py-11 text-center',
};

export function statusDot(status: StatusKey): string {
  return status === 'inactive' ? 'bg-coslash-neutral-dot' : STATUSES[status].dot;
}

export function tokenTotal(sessions: Session[]): number | null {
  return sumKnown(sessions.map(sessionTotalTokens));
}
