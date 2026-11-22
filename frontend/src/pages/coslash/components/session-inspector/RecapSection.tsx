import { useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon, FileTextIcon, LoaderCircleIcon } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
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
  isLocalSession,
  resolveGoal,
  sessionKey,
  type DecisionEvidence,
  type SessionDecision,
  type SessionDetail,
} from '@/pages/coslash/lib/session';

const DEBRIEF_PREVIEW_UNITS = 3;

function DecisionRow({
  decision,
  onEvidence,
}: {
  decision: SessionDecision;
  onEvidence?: (evidence: DecisionEvidence) => void;
}) {
  const notes: string[] = [];
  if (decision.why) notes.push(decision.why);
  if (decision.alternative) notes.push(`Instead of ${decision.alternative}`);
  if (!decision.cited) notes.push('The quote was not found in the timeline.');
  const statement = (
    <span className={cn({ 'cursor-help': notes.length > 0 })} tabIndex={notes.length > 0 ? 0 : undefined}>
      {decision.byYou && <span className="text-brand pr-1 font-bold tracking-wide">YOU</span>}
      {decision.text}
    </span>
  );
  return (
    <div className={cn('flex min-w-0 items-start gap-2', { 'text-coslash-muted': !decision.cited })}>
      <span className="flex h-4 w-3 shrink-0 items-center justify-center">
        <span className="bg-coslash-muted size-1 rounded-full" />
      </span>
      <div className="min-w-0 flex-1">
        {notes.length > 0 ? (
          <Tooltip>
            <TooltipTrigger asChild>{statement}</TooltipTrigger>
            <TooltipContent side="bottom" align="start" className="flex-col items-start">
              {notes.map((note) => (
                <div key={note}>{note}</div>
              ))}
            </TooltipContent>
          </Tooltip>
        ) : (
          statement
        )}
      </div>
      {decision.cited && (
        <div className="flex shrink-0 flex-col items-end">
          {decision.evidence.map((evidence) => (
            <button
              key={`${evidence.entry}:${evidence.quote}`}
              type="button"
              aria-label={`Show turn ${evidence.turn} in the timeline`}
              className="text-coslash-muted hover:text-coslash-accent cursor-pointer font-mono whitespace-nowrap hover:underline"
              onClick={() => onEvidence?.(evidence)}
            >
              turn {evidence.turn}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function RecapSection({
  detail,
  onEvidence,
  onOpenDesignDoc,
  onOpenSynthesisSettings,
}: {
  detail: SessionDetail;
  onEvidence?: (evidence: DecisionEvidence) => void;
  onOpenDesignDoc?: (path: string) => void;
  onOpenSynthesisSettings?: () => void;
}) {
  const goal = resolveGoal(detail);
  const synthesizing = detail.synthesis == null && detail.synthesisPending;
  const synthesisPlaceholder = (
    <div className="text-coslash-muted flex items-center gap-1 pt-1 text-xs">
      <LoaderCircleIcon className="size-3 animate-spin" />
      <span>Synthesizing…</span>
    </div>
  );
  const synthesisSettingsPrompt =
    onOpenSynthesisSettings && isLocalSession(detail) ? (
      <div className="flex flex-wrap items-center gap-2 pt-1 text-xs">
        <span>Decisions come from an AI summary using your own CLI account and quota.</span>
        <Button variant="outline" size="sm" onClick={onOpenSynthesisSettings}>
          Choose synthesis settings
        </Button>
      </div>
    ) : null;
  const outcome = getSessionOutcome(detail);
  const designDoc = detail.synthesis?.designDoc;

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
        ) : detail.synthesis?.decisionsVersion ? (
          <div className="flex min-w-0 flex-col gap-1 pt-1 text-xs wrap-break-word">
            {designDoc && (
              <div className="flex min-w-0 items-center gap-2">
                <FileTextIcon className="text-coslash-muted size-3 shrink-0" />
                {onOpenDesignDoc && detail.fileEdits.some(({ path }) => path === designDoc) ? (
                  <button
                    type="button"
                    title={`${designDoc} · open in Files`}
                    className="text-coslash-accent min-w-0 cursor-pointer truncate font-mono hover:underline"
                    onClick={() => onOpenDesignDoc(designDoc)}
                  >
                    {designDoc.split(/[\\/]/).pop()}
                  </button>
                ) : (
                  <span className="truncate font-mono" title={designDoc}>
                    {designDoc.split(/[\\/]/).pop()}
                  </span>
                )}
                <span className="text-coslash-muted shrink-0">· more decisions in the doc</span>
              </div>
            )}
            <TooltipProvider delayDuration={150}>
              {(detail.synthesis.decisions ?? []).map((decision) => (
                <DecisionRow
                  key={`${sessionKey(detail)}:${decision.id}`}
                  decision={decision}
                  onEvidence={onEvidence}
                />
              ))}
            </TooltipProvider>
            {!detail.synthesis.decisions?.length && !designDoc && <div>—</div>}
            {synthesisSettingsPrompt}
          </div>
        ) : detail.synthesis?.keyDecisions.length ? (
          <div className="flex flex-col gap-1 pt-1">
            {detail.synthesis.keyDecisions.map((decision) => (
              <div key={decision} className="flex items-start gap-2 text-xs">
                <span className="bg-coslash-muted mt-1 size-1 shrink-0 rounded-full" />
                <span>{decision}</span>
              </div>
            ))}
            {synthesisSettingsPrompt}
          </div>
        ) : synthesisSettingsPrompt ? (
          synthesisSettingsPrompt
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
