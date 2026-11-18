import { useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon, LoaderCircleIcon } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { SectionLabel } from '@/pages/coslash/components/session-inspector/SectionLabel';
import {
  blocksFromTexts,
  collapseDebriefBlocks,
  parseDebriefText,
  type DebriefBlock,
} from '@/pages/coslash/lib/debrief-text';
import {
  getSessionOutcome,
  goalSourceLabel,
  resolveGoal,
  sessionKey,
  type SessionDetail,
} from '@/pages/coslash/lib/session';

const DEBRIEF_PREVIEW_UNITS = 3;

export function RecapSection({ detail }: { detail: SessionDetail }) {
  const goal = resolveGoal(detail);
  const synthesizing = detail.synthesis == null && detail.synthesisPending;
  const synthesisPlaceholder = (
    <div className="text-coslash-muted flex items-center gap-1 pt-1 text-xs">
      <LoaderCircleIcon className="size-3 animate-spin" />
      <span>Synthesizing…</span>
    </div>
  );
  const outcome = getSessionOutcome(detail);

  return (
    <div>
      <SectionLabel title="DEBRIEF" />
      <div className="rounded-lg border p-3">
        {detail.synthesisError && (
          <div role="alert" className="bg-warning-bg text-warning-fg rounded-lg p-2 text-xs">
            {detail.synthesisError} Using the transcript-derived debrief instead.
          </div>
        )}
        <div className={cn('flex items-center gap-2', { 'pt-3': detail.synthesisError != null })}>
          <span className="text-coslash-muted text-xs">GOAL</span>
          <Badge variant="secondary" className="text-xs">
            {goalSourceLabel(goal.source)}
          </Badge>
        </div>
        <div className="pt-2">
          <DebriefProse key={`${sessionKey(detail)}:goal`} blocks={blocksFromTexts(goal.texts)} tone="goal" />
        </div>
        <div className="border-b pt-3" />
        <div className="text-coslash-muted pt-3 text-xs">OUTCOME</div>
        {synthesizing ? (
          synthesisPlaceholder
        ) : (
          <div className="pt-1">
            <DebriefProse
              key={`${sessionKey(detail)}:outcome`}
              blocks={outcome ? parseDebriefText(outcome) : []}
              empty="—"
              tone="outcome"
            />
          </div>
        )}
        <div className="text-coslash-muted pt-3 text-xs">KEY DECISIONS</div>
        {synthesizing ? (
          synthesisPlaceholder
        ) : detail.synthesis?.keyDecisions.length ? (
          <div className="flex flex-col gap-1 pt-1">
            {detail.synthesis.keyDecisions.map((decision) => (
              <div key={decision} className="flex items-start gap-2 text-xs">
                <span className="bg-coslash-muted mt-1 size-1 shrink-0 rounded-full" />
                <span>{decision}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="pt-1 text-xs">—</div>
        )}
        {detail.synthesis?.nextStep && (
          <>
            <div className="text-coslash-muted pt-3 text-xs">NEXT STEP</div>
            <div className="pt-1 text-xs">{detail.synthesis.nextStep}</div>
          </>
        )}
      </div>
    </div>
  );
}

export function DebriefProse({
  blocks,
  empty = '—',
  tone,
}: {
  blocks: DebriefBlock[];
  empty?: string;
  tone: 'goal' | 'outcome';
}) {
  const [expanded, setExpanded] = useState(false);
  if (blocks.length === 0) return <div className="text-xs">{empty}</div>;

  const preview = collapseDebriefBlocks(blocks, DEBRIEF_PREVIEW_UNITS);
  const canExpand = preview.truncated;
  const shown = expanded ? blocks : preview.blocks;

  return (
    <div className="flex flex-col gap-2">
      <div
        key={expanded ? 'full' : 'preview'}
        className={cn('border-coslash-line border-l-2 pl-3', {
          'border-l-brand': tone === 'goal',
          'border-l-recap': tone === 'outcome',
        })}
      >
        <DebriefBlocks blocks={shown} tone={tone} />
      </div>
      {canExpand && (
        <button
          type="button"
          aria-expanded={expanded}
          className="text-brand flex w-fit cursor-pointer items-center gap-1 text-xs select-none"
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? <ChevronDownIcon className="size-3" /> : <ChevronRightIcon className="size-3" />}
          <span>
            {expanded
              ? 'show less'
              : preview.hiddenCount > 0
                ? `show full · ${preview.hiddenCount} more`
                : 'show full'}
          </span>
        </button>
      )}
    </div>
  );
}

function DebriefBlocks({ blocks, tone }: { blocks: DebriefBlock[]; tone: 'goal' | 'outcome' }) {
  return (
    <div className="flex flex-col gap-2">
      {blocks.map((block, index) => {
        if (block.kind === 'heading') {
          return (
            <div key={`h-${index}`} className="text-xs font-semibold tracking-wide">
              {block.text}
            </div>
          );
        }
        if (block.kind === 'list') {
          if (block.ordered) {
            return (
              <ol
                key={`l-${index}`}
                className={cn('list-decimal space-y-1.5 pl-4 text-xs', {
                  italic: tone === 'goal',
                })}
              >
                {block.items.map((item, itemIndex) => (
                  <li key={`${index}-${itemIndex}`} className="leading-relaxed">
                    {item}
                  </li>
                ))}
              </ol>
            );
          }
          return (
            <ul
              key={`l-${index}`}
              className={cn('flex list-none flex-col gap-1.5 text-xs', {
                italic: tone === 'goal',
              })}
            >
              {block.items.map((item, itemIndex) => (
                <li key={`${index}-${itemIndex}`} className="flex items-start gap-2">
                  <span className="bg-coslash-muted mt-1.5 size-1 shrink-0 rounded-full" />
                  <span className="min-w-0 flex-1 leading-relaxed">{item}</span>
                </li>
              ))}
            </ul>
          );
        }
        return (
          <p
            key={`p-${index}`}
            className={cn('text-xs leading-relaxed', {
              italic: tone === 'goal',
            })}
          >
            {block.text}
          </p>
        );
      })}
    </div>
  );
}
