import { useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon, ExternalLinkIcon } from 'lucide-react';
import { Dialog, DialogTrigger } from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { SubagentDialogContent, SubagentModelBadge } from '@/pages/coslash/components/SessionCard';
import {
  digestDateKey,
  formatDigestDateDivider,
  formatDigestDateRange,
  formatDigestTime,
} from '@/pages/coslash/lib/format';
import {
  SUBAGENT_STATUSES,
  subagentParentName,
  type DigestEntry,
  type SessionDetail,
} from '@/pages/coslash/lib/session';

type DigestCategory = DigestEntry['category'];

const DIGEST_CATEGORIES: Record<DigestCategory, { label: string; fg: string; dot: string }> = {
  first_prompt: { label: 'FIRST PROMPT', fg: 'text-success-fg', dot: 'bg-success-fg' },
  question: { label: 'QUESTION', fg: 'text-question', dot: 'bg-question' },
  subagent: { label: 'Subagent', fg: 'text-subagent', dot: 'bg-subagent' },
  todos: { label: 'TODOS', fg: 'text-coslash-muted', dot: 'bg-coslash-muted' },
  recap: { label: 'RECAP', fg: 'text-recap', dot: 'bg-recap' },
  plan: { label: 'PLAN', fg: 'text-recap', dot: 'bg-recap' },
  user: { label: 'USER TURN', fg: 'text-brand', dot: 'bg-brand' },
  compaction: { label: 'COMPACTION', fg: 'text-compaction', dot: 'bg-compaction' },
};

function CategoryChip({
  category,
  count,
  active,
  onCycle,
}: {
  category: DigestCategory;
  count: number;
  active: boolean;
  onCycle: () => void;
}) {
  const meta = DIGEST_CATEGORIES[category];
  return (
    <button
      type="button"
      aria-pressed={active}
      title={`Click to show only ${meta.label.toLowerCase()}, click again to exclude, and once more to show all.`}
      className={cn('flex cursor-pointer items-center gap-1 rounded-full border px-2 py-1 select-none', {
        'bg-coslash-soft': active,
        'border-dashed': !active,
      })}
      onClick={onCycle}
    >
      <span className={cn('size-2 rounded-full', active ? meta.dot : 'bg-coslash-neutral-dot')} />
      <span
        className={cn('text-xs font-semibold', {
          'text-coslash-muted': !active,
        })}
      >
        {meta.label}
      </span>
      <span className="text-coslash-muted text-xs">{count}</span>
    </button>
  );
}

function DigestRow({ entry, endsDay }: { entry: DigestEntry; endsDay?: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const meta = DIGEST_CATEGORIES[entry.category];
  const collapsible = entry.description.length > 120;

  return (
    <div className={cn('border-coslash-line flex items-baseline gap-2 py-1', { 'border-b': !endsDay })}>
      <span className={cn('w-24 shrink-0 text-xs font-bold tracking-wide', meta.fg)}>{meta.label}</span>
      <div className="min-w-0 flex-1">
        <div
          className={cn('text-xs wrap-break-word', {
            'line-clamp-1': collapsible && !expanded,
            'whitespace-pre-wrap': entry.category === 'plan',
          })}
        >
          {entry.branchId && (
            <span
              className="text-coslash-muted inline-block max-w-40 truncate align-bottom"
              title={entry.branchId}
            >
              [branch {entry.branchId}]{' '}
            </span>
          )}
          {entry.inherited && <span className="text-coslash-muted">[inherited] </span>}
          {entry.active === true && <span className="text-coslash-muted">[active] </span>}
          {entry.description}
        </div>
        {entry.answer != null && (
          <div className="border-coslash-line text-coslash-muted border-l-2 pl-2 text-xs wrap-break-word">
            {entry.answer}
          </div>
        )}
        {collapsible && (
          <button
            type="button"
            aria-expanded={expanded}
            className="text-brand flex cursor-pointer items-center gap-1 pt-1 text-xs"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? <ChevronDownIcon className="size-3" /> : <ChevronRightIcon className="size-3" />}
            <span>{expanded ? 'collapse' : `expand full ${entry.category}`}</span>
          </button>
        )}
      </div>
      <span className="text-coslash-muted flex shrink-0 flex-col items-end font-mono text-xs whitespace-nowrap">
        <span>turn {entry.turn}</span>
        {entry.time != null && entry.time > 0 && (
          <span className="text-coslash-muted">{formatDigestTime(entry.time)}</span>
        )}
      </span>
    </div>
  );
}

function SubagentDigestRow({ subagentId, detail }: { subagentId: string; detail: SessionDetail }) {
  const subagent = detail.subagents.find((candidate) => candidate.id === subagentId);
  if (!subagent) {
    throw new Error(`digest references subagent ${subagentId}, which is not on the session`);
  }
  const parentName = subagentParentName(subagent, detail.subagents, detail.name);
  const status = SUBAGENT_STATUSES[subagent.status].label;
  return (
    <Dialog>
      <DialogTrigger asChild>
        <div className="bg-subagent-card flex cursor-pointer items-baseline gap-2 rounded-lg border p-2">
          <span className="text-subagent w-24 shrink-0 text-xs font-bold tracking-wide">Subagent</span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="min-w-0 truncate text-sm font-semibold">{subagent.name}</span>
              <SubagentModelBadge model={subagent.model} />
            </div>
            <div className="text-coslash-muted truncate pt-1 text-xs">
              {subagent.result === '' ? status : `${status}: ${subagent.result}`}
            </div>
          </div>
          <div className="text-brand flex shrink-0 items-center gap-1 text-xs">
            <span>open</span>
            <ExternalLinkIcon className="size-3" />
          </div>
        </div>
      </DialogTrigger>
      <SubagentDialogContent subagent={subagent} parentName={parentName} />
    </Dialog>
  );
}

function DateDivider({ label }: { label: string }) {
  return (
    <div className="text-coslash-muted grid grid-cols-[1fr_auto_1fr] items-center gap-2 pt-2 text-xs font-bold tracking-wide">
      <span className="bg-coslash-line h-px" />
      <span>{label}</span>
      <span className="bg-coslash-line h-px" />
    </div>
  );
}

export function DigestSection({ detail }: { detail: SessionDetail }) {
  const [categoryFilter, setCategoryFilter] = useState<{
    category: DigestCategory;
    mode: 'only' | 'exclude';
  } | null>(null);
  const digest = detail.digest;
  if (digest.length === 0)
    return detail.detailsIncomplete ? (
      <p className="text-warning-fg pb-2 text-xs">Some nested tool details are unavailable.</p>
    ) : null;

  const counts = new Map<DigestCategory, number>();
  for (const entry of digest) counts.set(entry.category, (counts.get(entry.category) ?? 0) + 1);

  const cycleCategory = (category: DigestCategory) => {
    setCategoryFilter((current) => {
      if (current?.category !== category) return { category, mode: 'only' };
      return current.mode === 'only' ? { category, mode: 'exclude' } : null;
    });
  };
  const categoryVisible = (category: DigestCategory) => {
    if (categoryFilter == null) return true;
    return categoryFilter.mode === 'only'
      ? category === categoryFilter.category
      : category !== categoryFilter.category;
  };
  const visible = digest.filter((entry) => categoryVisible(entry.category));
  const times = digest.map((e) => e.time ?? 0).filter((t) => t > 0);
  const dateRange = formatDigestDateRange(times);

  const startsDayIndices = new Set<number>();
  const endsDayIndices = new Set<number>();
  let lastDate = '';
  for (let i = 0; i < visible.length; i++) {
    const entryDate = visible[i].time != null && visible[i].time! > 0 ? digestDateKey(visible[i].time!) : '';
    if (entryDate !== '' && entryDate !== lastDate) {
      startsDayIndices.add(i);
    }
    if (i > 0 && entryDate !== '' && lastDate !== '' && entryDate !== lastDate) {
      endsDayIndices.add(i - 1);
    }
    if (entryDate !== '') {
      lastDate = entryDate;
    }
  }

  const rows = visible.map((entry, index) => {
    return (
      <div key={entry.sourceEntryId ? `${entry.sourceEntryId}:${entry.category}` : index}>
        {startsDayIndices.has(index) && <DateDivider label={formatDigestDateDivider(entry.time!)} />}
        {entry.category === 'subagent' ? (
          <SubagentDigestRow subagentId={entry.subagentId!} detail={detail} />
        ) : (
          <DigestRow entry={entry} endsDay={endsDayIndices.has(index) || index === visible.length - 1} />
        )}
      </div>
    );
  });

  return (
    <div>
      <div className="flex items-baseline justify-between gap-2 pb-2">
        <div className="flex items-baseline gap-2">
          <span className="text-coslash-muted text-xs font-semibold tracking-wide">TIMELINE</span>
          <span className="text-coslash-muted text-xs">
            {detail.agent === 'pi' ? 'all branches in time order' : 'key events from this session'}
          </span>
        </div>
        <span className="text-coslash-muted text-xs">{dateRange}</span>
      </div>
      <div className="flex flex-wrap gap-1 pb-2">
        {(Object.keys(DIGEST_CATEGORIES) as DigestCategory[])
          .filter((category) => counts.has(category))
          .map((category) => (
            <CategoryChip
              key={category}
              category={category}
              count={counts.get(category)!}
              active={categoryVisible(category)}
              onCycle={() => cycleCategory(category)}
            />
          ))}
      </div>
      {detail.detailsIncomplete && (
        <p className="text-warning-fg pb-2 text-xs">Some nested tool details are unavailable.</p>
      )}
      <div className="flex flex-col gap-1">{rows}</div>
    </div>
  );
}
