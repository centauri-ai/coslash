import { LoaderCircleIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { FileChange } from '@/pages/coslash/hooks/use-sessions';

type DiffLineKind = 'meta' | 'hunk' | 'addition' | 'deletion' | 'context';

/* oxlint-disable react/only-export-components -- exported for focused rendering tests */
export function diffLineKind(line: string): DiffLineKind {
  if (line.startsWith('+++') || line.startsWith('---')) return 'meta';
  if (line.startsWith('@@')) return 'hunk';
  if (line.startsWith('+')) return 'addition';
  if (line.startsWith('-')) return 'deletion';
  return 'context';
}

export function fileChangeLabel(change: Pick<FileChange, 'operation'>, index: number) {
  return `${change.operation} ${index + 1}`;
}
/* oxlint-enable react/only-export-components */

export function DiffList({
  changes,
  isLoading,
  loadError,
}: {
  changes: FileChange[] | null;
  isLoading: boolean;
  loadError: string | null;
}) {
  if (isLoading) {
    return (
      <div className="text-muted-foreground flex flex-1 items-center justify-center gap-1 text-xs">
        <LoaderCircleIcon className="size-3 animate-spin" />
        Loading file changes…
      </div>
    );
  }
  if (loadError != null) {
    return (
      <div role="alert" className="text-destructive flex flex-1 items-center justify-center text-xs">
        {loadError}
      </div>
    );
  }
  if (changes?.length === 0) {
    return (
      <div className="text-muted-foreground flex flex-1 items-center justify-center text-xs">
        No recorded changes from this session.
      </div>
    );
  }
  if (changes == null) return null;

  return (
    <div className="flex-1 space-y-3 overflow-auto p-3">
      {changes.map((change, changeIndex) => (
        <section key={changeIndex} className="overflow-hidden rounded-lg border">
          <div className="bg-muted flex items-center justify-between gap-2 border-b px-3 py-2 text-xs">
            <span className="font-medium">{fileChangeLabel(change, changeIndex)}</span>
            {change.kind === 'content' ? (
              <span className="text-muted-foreground">Full content</span>
            ) : (
              <span className="flex gap-2">
                <span className="text-success-fg">+{change.additions}</span>
                <span className="text-destructive">−{change.deletions}</span>
              </span>
            )}
          </div>
          {change.kind === 'content' ? (
            <pre className="overflow-auto p-3 font-mono text-xs leading-relaxed">{change.text}</pre>
          ) : (
            <pre className="overflow-auto py-2 font-mono text-xs leading-relaxed">
              {change.text.split('\n').map((line, lineIndex) => {
                const kind = diffLineKind(line);
                return (
                  <span
                    key={lineIndex}
                    className={cn('block min-w-max px-3', {
                      'text-muted-foreground': kind === 'meta',
                      'text-brand': kind === 'hunk',
                      'bg-success-bg text-success-fg': kind === 'addition',
                      'bg-destructive/10 text-destructive': kind === 'deletion',
                    })}
                  >
                    {line || ' '}
                  </span>
                );
              })}
            </pre>
          )}
        </section>
      ))}
    </div>
  );
}
