import { useState } from 'react';
import { Copy, RefreshCw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { DiagnosticsButton } from '@/pages/coslash/components/DiagnosticsButton';
import { DiagnosticsChecklist } from '@/pages/coslash/components/DiagnosticsChecklist';
import { formatDiagnosticsForCopy, worstStatus, type Diagnostics } from '@/pages/coslash/lib/diagnostics';

export function DiagnosticsDialog({
  open,
  onOpenChange,
  diagnostics,
  isLoading,
  loadFailed,
  onRefresh,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  diagnostics: Diagnostics | null;
  isLoading: boolean;
  loadFailed: boolean;
  onRefresh: () => void;
}) {
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const copyDiagnostics = () => {
    if (!diagnostics) return;
    navigator.clipboard
      .writeText(formatDiagnosticsForCopy(diagnostics))
      .then(() => setCopyState('copied'))
      .catch(() => setCopyState('failed'));
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <DiagnosticsButton status={diagnostics ? worstStatus(diagnostics.checks) : 'ok'} />
      </DialogTrigger>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>coSlash diagnostics</DialogTitle>
          <DialogDescription>
            Detected session sources, command-line tools, storage, and platform support.
          </DialogDescription>
        </DialogHeader>
        {loadFailed ? (
          <div role="alert" className="text-destructive py-4 text-sm">
            Diagnostics could not be loaded. Re-run the checks to try again.
          </div>
        ) : diagnostics ? (
          <div className="flex flex-col gap-5">
            <DiagnosticsChecklist checks={diagnostics.checks} />
            <div className="border-t pt-4">
              <div className="pb-2 text-sm font-semibold">Facts</div>
              <div className="text-muted-foreground flex flex-col gap-2 text-xs">
                <div>
                  coSlash {diagnostics.version} · {diagnostics.platform.os}/{diagnostics.platform.arch}
                </div>
                {diagnostics.sources.map((source) => (
                  <div key={source.agent}>
                    {source.label}: {source.sessions} sessions from {source.transcripts} transcripts in{' '}
                    <code>{source.root}</code>
                    <br />
                    CLI: {source.cli.found ? source.cli.version || source.cli.path : 'not found'}
                  </div>
                ))}
                <div>
                  Storage: <code>{diagnostics.storage.home}</code> · {diagnostics.storage.summaries} summaries
                </div>
              </div>
            </div>
          </div>
        ) : (
          <div className="text-muted-foreground py-6 text-sm">Checking local session sources…</div>
        )}
        <DialogFooter className="sm:justify-between">
          <Button variant="outline" onClick={onRefresh} disabled={isLoading}>
            <RefreshCw className={isLoading ? 'animate-spin' : undefined} aria-hidden="true" />
            Re-run checks
          </Button>
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button onClick={copyDiagnostics} disabled={!diagnostics}>
                  <Copy aria-hidden="true" />
                  {copyState === 'copied'
                    ? 'Copied'
                    : copyState === 'failed'
                      ? 'Copy failed'
                      : 'Copy diagnostics'}
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                Copies paths, counts, versions, and checks — never transcript content or session names.
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
