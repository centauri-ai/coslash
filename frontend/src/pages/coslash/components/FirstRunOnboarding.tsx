import { RefreshCw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { DiagnosticsChecklist } from '@/pages/coslash/components/DiagnosticsChecklist';
import type { Diagnostics } from '@/pages/coslash/lib/diagnostics';

export function FirstRunOnboarding({
  diagnostics,
  isLoading,
  loadFailed,
  onRefresh,
}: {
  diagnostics: Diagnostics | null;
  isLoading: boolean;
  loadFailed: boolean;
  onRefresh: () => void;
}) {
  return (
    <div role="status" className="h-full overflow-y-auto bg-background px-4 py-8">
      <div className="bg-background mx-auto flex max-w-2xl flex-col gap-5 rounded-xl border p-6 text-left shadow-sm">
        <div>
          <div className="text-lg font-semibold">No agent sessions found on this machine.</div>
          <div className="text-muted-foreground pt-2 text-sm">
            coSlash reads Claude Code and Codex transcripts from your home directory. Nothing to read yet.
          </div>
        </div>
        {loadFailed ? (
          <div role="alert" className="text-destructive text-sm">
            Diagnostics could not be loaded. Re-run the checks to try again.
          </div>
        ) : diagnostics ? (
          <DiagnosticsChecklist checks={diagnostics.checks} />
        ) : (
          <div className="text-muted-foreground text-sm">Checking local session sources…</div>
        )}
        <div className="border-t pt-4 text-sm">
          <span className="font-semibold">Next:</span> run <code>claude</code> or <code>codex</code> in a
          repo, do one turn, then re-run these checks.
        </div>
        <div>
          <Button variant="outline" size="sm" onClick={onRefresh} disabled={isLoading}>
            <RefreshCw className={isLoading ? 'animate-spin' : undefined} aria-hidden="true" />
            Re-run checks
          </Button>
        </div>
      </div>
    </div>
  );
}
