import { useState, type ReactNode } from 'react';
import { ExternalLinkIcon, PlayIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { DirectedHandoffDialog } from '@/pages/coslash/components/DirectedHandoffDialog';
import { DirectedHandoffStatus } from '@/pages/coslash/components/DirectedHandoffStatus';
import {
  branchDriftReadiness,
  contextFillReadiness,
  treeStaleReadiness,
} from '@/pages/coslash/components/session-inspector/readiness';
import { PromptCacheCell, ReadinessCell } from '@/pages/coslash/components/session-inspector/ReadinessCells';
import { SectionLabel } from '@/pages/coslash/components/session-inspector/SectionLabel';
import { useLaunchTerminal } from '@/pages/coslash/hooks/use-launch-terminal';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';
import { copyHandoffText, handoffBrief } from '@/pages/coslash/lib/handoff';
import {
  canResumeSession,
  isLocalSession,
  resumeDisabled,
  sessionReadiness,
  type SessionDetail,
} from '@/pages/coslash/lib/session';

/* Brand-filled CTA. Replaces the Button default variant's fill, text, and hover as
   one set: overriding only the fill leaves the variant's hover:bg-primary in place,
   which drops the button to the shadcn neutral on hover. Hover mixes toward
   --foreground so it gains contrast against the surface in either theme. */
const brandCta =
  'bg-brand text-brand-foreground hover:bg-[color-mix(in_oklch,var(--brand),var(--foreground)_10%)]';

function LaunchError({ message }: { message: string | null }) {
  if (message == null) return null;
  return <span className="text-danger-fg text-xs">{message}</span>;
}

function DisabledLaunchTooltip({ hint, children }: { hint?: string; children: ReactNode }) {
  if (hint == null) return children;
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span tabIndex={0}>{children}</span>
        </TooltipTrigger>
        <TooltipContent className="coslash-shell">{hint}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

export function GrokFreshSessionControl({
  launching,
  launchError,
  onLaunch,
}: {
  launching: boolean;
  launchError: string | null;
  onLaunch: () => void;
}) {
  return (
    <div className="flex w-fit max-w-full flex-wrap items-center gap-2">
      <Button
        className="bg-brand text-brand-foreground w-fit p-2 text-xs hover:bg-[color-mix(in_oklch,var(--brand),var(--foreground)_10%)]"
        disabled={launching}
        aria-busy={launching}
        onClick={onLaunch}
      >
        <PlayIcon />
        <span>Start fresh</span>
      </Button>
      {launchError && (
        <span role="alert" className="text-danger-fg text-xs">
          {launchError}
        </span>
      )}
    </div>
  );
}

function GrokFreshSessionButton({ detail }: { detail: SessionDetail }) {
  const { launch, launchError, launching } = useLaunchTerminal(detail);
  return (
    <GrokFreshSessionControl launching={launching} launchError={launchError} onLaunch={() => launch('new')} />
  );
}

export function ResumeSessionButton({
  detail,
  disabledHint,
}: {
  detail: SessionDetail;
  disabledHint?: string;
}) {
  const { launch, launchError } = useLaunchTerminal(detail);
  const disabled = resumeDisabled(detail, disabledHint);
  const opensCursor =
    isLocalSession(detail) && detail.agent === 'cursor' && detail.entrypoint === 'cursor-ide';
  const recommended = canResumeSession(detail) && sessionReadiness(detail).key === 'resume';

  return (
    <div className="flex flex-col gap-1">
      <DisabledLaunchTooltip hint={disabledHint}>
        <Button
          variant={recommended ? 'default' : 'outline'}
          className={cn('w-fit p-2 text-xs', { [brandCta]: recommended })}
          onClick={() => launch(opensCursor ? 'open' : 'resume')}
          disabled={disabled}
        >
          {opensCursor ? <ExternalLinkIcon /> : <PlayIcon />}
          <span>{opensCursor ? 'Open Cursor' : 'Resume'}</span>
        </Button>
      </DisabledLaunchTooltip>
      <LaunchError message={launchError} />
    </div>
  );
}

export function HandoffSection({
  detail,
  exactDetailsAvailable,
  remoteLaunchable,
  remoteLaunchHint,
  handoff,
  onHandoffStarted,
  onOpenTarget,
}: {
  detail: SessionDetail;
  exactDetailsAvailable: boolean;
  remoteLaunchable: boolean;
  remoteLaunchHint?: string;
  handoff?: DirectedHandoff;
  onHandoffStarted: () => void;
  onOpenTarget: (handoff: DirectedHandoff) => void;
}) {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState<string | null>(null);
  const contextFill = contextFillReadiness(detail);
  const branchDrift = branchDriftReadiness(detail.git);
  const treeStale = treeStaleReadiness(detail.lastEditAt);

  const brief = handoffBrief(detail);

  const copyBrief = async (text = brief) => {
    setCopyError(null);
    try {
      await copyHandoffText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
      return true;
    } catch {
      setCopied(false);
      setCopyError('Could not copy the handoff. Allow clipboard access and try again.');
      return false;
    }
  };

  return (
    <div className="@container flex flex-col gap-2">
      <SectionLabel title={detail.agent === 'grok' ? 'RESUME OR START FRESH' : 'RESUME OR HAND OFF'} />
      <div className="bg-coslash-line grid grid-cols-3 gap-px overflow-hidden rounded-lg border @[560px]:grid-cols-5">
        <ReadinessCell label="Context used" value={contextFill?.value} tone={contextFill?.tone} />
        <ReadinessCell label="Compactions" value={String(detail.compactions)} />
        <ReadinessCell label="Branch" value={branchDrift?.value} tone={branchDrift?.tone} />
        <ReadinessCell label="Working tree" value={treeStale?.value} tone={treeStale?.tone} />
        <PromptCacheCell lastAccessAt={detail.mtime} className="col-span-2 @[560px]:col-span-1" />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {detail.agent === 'grok' ? (
          <GrokFreshSessionButton detail={detail} />
        ) : (
          <DirectedHandoffDialog
            source={detail}
            disabledHint={
              !isLocalSession(detail) && detail.agent === 'pi'
                ? 'Pi launch is available locally only'
                : !isLocalSession(detail) && !remoteLaunchable
                  ? remoteLaunchHint
                  : undefined
            }
            onStarted={onHandoffStarted}
          />
        )}
        <Button variant="outline" className="w-fit p-2 text-xs" onClick={() => void copyBrief()}>
          <span>Copy handoff</span>
        </Button>
        {copied && <span className="text-coslash-muted text-xs">copied to clipboard</span>}
        {copyError && (
          <span role="alert" className="text-danger-fg text-xs">
            {copyError}
          </span>
        )}
      </div>
      {handoff && (
        <div className="rounded-lg border p-3">
          <DirectedHandoffStatus handoff={handoff} onOpenTarget={onOpenTarget} />
        </div>
      )}
      {!isLocalSession(detail) && (
        <div className="text-coslash-muted text-xs">
          {!exactDetailsAvailable
            ? 'This inspector uses the bounded session-library summary. Complete commands and exact file diffs are unavailable for this session.'
            : detail.agent === 'pi'
              ? 'Pi terminal actions are available locally only. You can still copy this handoff.'
              : remoteLaunchable
                ? 'Remote terminal actions open through SSH. Exact cached details, commands, and file diffs stay available locally; synthesis and Hub sharing remain local-only.'
                : 'Remote terminal actions are available when SSH reconnects. Exact cached details, commands, and file diffs stay available locally; synthesis and Hub sharing remain local-only.'}
        </div>
      )}
    </div>
  );
}
