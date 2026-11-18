import { useLayoutEffect, useRef, useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon, SquareCheckIcon, SquareIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { highlightedRowClass } from '@/pages/coslash/components/session-inspector/DigestSection';
import { FieldLabel, SectionLabel } from '@/pages/coslash/components/session-inspector/SectionLabel';
import type { SessionDetail } from '@/pages/coslash/lib/session';

function StatCell({ value, label }: { value: string; label: string }) {
  return (
    <div className="bg-coslash-surface p-2">
      <div className="text-base font-bold">{value}</div>
      <div className="text-coslash-muted text-xs">{label}</div>
    </div>
  );
}

export function ArtifactStats({ detail }: { detail: SessionDetail }) {
  const newFiles = detail.fileEdits.filter((fileEdit) => fileEdit.isNew).length;
  const cells: [string, string][] = [
    [String(detail.fileEdits.length), newFiles ? `files (${newFiles} new)` : 'files'],
    [String(detail.prs), 'PRs'],
    [String(detail.subagents.length), 'subagents'],
  ];

  return (
    <div>
      <SectionLabel title="ARTIFACTS" note="what this session produced" />
      <div className="bg-coslash-line grid grid-cols-3 gap-px overflow-hidden rounded-lg border">
        {cells.map(([value, label]) => (
          <StatCell key={label} value={value} label={label} />
        ))}
      </div>
    </div>
  );
}

export function FilesChangedList({
  detail,
  onSelectFile,
  focusRequest,
}: {
  detail: SessionDetail;
  onSelectFile: ((fileEdit: SessionDetail['fileEdits'][number], trigger: HTMLButtonElement) => void) | null;
  focusRequest?: { path: string } | null;
}) {
  const focusedRowRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (focusRequest == null) return;
    const row = focusedRowRef.current;
    row?.scrollIntoView?.({ block: 'center' });
    (row?.querySelector<HTMLElement>('button') ?? row)?.focus({ preventScroll: true });
  }, [focusRequest, detail.fileEdits]);
  if (detail.fileEdits.length === 0) return null;
  const newFiles = detail.fileEdits.filter((fileEdit) => fileEdit.isNew).length;
  return (
    <div>
      <FieldLabel>{`FILES CHANGED · ${detail.fileEdits.length} TOTAL${newFiles ? `, ${newFiles} NEW` : ''}`}</FieldLabel>
      <div className="rounded-sm border p-2">
        {detail.fileEdits.map((fileEdit) => (
          <div
            key={fileEdit.path}
            ref={focusRequest?.path === fileEdit.path ? focusedRowRef : undefined}
            tabIndex={focusRequest?.path === fileEdit.path ? -1 : undefined}
            className={cn('flex items-center justify-between gap-2 py-1 font-mono text-xs', {
              [highlightedRowClass]: focusRequest?.path === fileEdit.path,
            })}
          >
            {onSelectFile == null ? (
              <span
                title={fileEdit.path}
                className={cn('text-coslash-muted min-w-0 truncate text-left', {
                  'text-success-fg': fileEdit.isNew,
                })}
              >
                {fileEdit.path.split('/').pop()}
              </span>
            ) : (
              <button
                type="button"
                title={fileEdit.path}
                className={cn(
                  'text-coslash-muted min-w-0 cursor-pointer truncate text-left hover:underline',
                  {
                    'text-success-fg': fileEdit.isNew,
                  },
                )}
                onClick={(event) => onSelectFile(fileEdit, event.currentTarget)}
              >
                {fileEdit.path.split('/').pop()}
              </button>
            )}
            <span className="whitespace-nowrap">
              <span className="text-success-fg">+{fileEdit.adds}</span>{' '}
              <span className="text-danger-fg">−{fileEdit.dels}</span>{' '}
              <span className="text-coslash-muted">
                · {fileEdit.edits} {fileEdit.edits === 1 ? 'edit' : 'edits'}
              </span>
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

export function CommitsAndTodos({ detail }: { detail: SessionDetail }) {
  return (
    <div className="flex gap-4 pt-4">
      <div className="min-w-0 flex-1">
        <FieldLabel>COMMITS</FieldLabel>
        {detail.commits.length === 0 ? (
          <div className="text-coslash-muted text-xs">—</div>
        ) : (
          detail.commits.map((commit, index) => (
            <div key={index} className="truncate py-1 font-mono text-xs">
              {commit}
            </div>
          ))
        )}
      </div>
      <div className="min-w-0 flex-1">
        <FieldLabel>TODOS</FieldLabel>
        {detail.todos.length === 0 ? (
          <div className="text-coslash-muted text-xs">none</div>
        ) : (
          detail.todos.map((todo, index) => (
            <div
              key={index}
              className={cn('flex items-start gap-1 py-1 text-xs', {
                'text-coslash-muted': todo.done,
              })}
            >
              {todo.done ? (
                <SquareCheckIcon className="size-4 shrink-0" />
              ) : (
                <SquareIcon className="size-4 shrink-0" />
              )}
              <span className="min-w-0">{todo.text}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

export function CommandsSection({ detail }: { detail: SessionDetail }) {
  const [open, setOpen] = useState(false);
  if (detail.commands.length === 0) return null;

  return (
    <div className="pt-4">
      <button
        type="button"
        aria-expanded={open}
        className="flex w-full cursor-pointer items-center gap-2"
        onClick={() => setOpen(!open)}
      >
        {open ? (
          <ChevronDownIcon className="text-coslash-muted size-3" />
        ) : (
          <ChevronRightIcon className="text-coslash-muted size-3" />
        )}
        <span className="text-coslash-muted pb-1 text-xs font-semibold tracking-wide">
          {`${detail.commands.length} ${detail.commands.length === 1 ? 'COMMAND' : 'COMMANDS'}`}
        </span>
        <span className="flex-1 border-b" />
      </button>
      {open && (
        <div className="bg-coslash-soft max-h-44 overflow-x-hidden overflow-y-auto rounded-lg p-3">
          <pre className="text-coslash-ink font-mono text-xs leading-relaxed wrap-break-word whitespace-pre-wrap">
            {detail.commands.join('\n')}
          </pre>
        </div>
      )}
    </div>
  );
}
