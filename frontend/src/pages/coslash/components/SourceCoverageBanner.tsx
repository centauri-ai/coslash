import { useState } from 'react';
import { Info, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { sourceCoverageMessage, type DiagnosticSource } from '@/pages/coslash/lib/diagnostics';

export function SourceCoverageBanner({
  sources,
  onDetails,
}: {
  sources: DiagnosticSource[];
  onDetails: () => void;
}) {
  const [dismissed, setDismissed] = useState(false);
  if (dismissed || sources.length === 0) return null;
  const coverage = sourceCoverageMessage(sources);
  return (
    <div className="bg-warning-bg text-warning-fg flex items-center justify-between gap-3 border-b px-4 py-2 text-sm">
      <div className="flex items-center gap-2">
        <Info className="size-4 shrink-0" aria-hidden="true" />
        <span>{coverage} — sessions from other agents are still available.</span>
        <Button variant="link" size="sm" className="text-warning-fg" onClick={onDetails}>
          Details
        </Button>
      </div>
      <Button variant="ghost" size="icon-sm" onClick={() => setDismissed(true)} aria-label="Dismiss warning">
        <X aria-hidden="true" />
      </Button>
    </div>
  );
}
