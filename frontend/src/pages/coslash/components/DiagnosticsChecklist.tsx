import { CircleCheck, CircleX, TriangleAlert } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { DiagnosticsCheck } from '@/pages/coslash/lib/diagnostics';

const CHECK_STYLE = {
  ok: { icon: CircleCheck, label: 'OK', className: 'text-success-fg' },
  warn: { icon: TriangleAlert, label: 'Warning', className: 'text-warning-fg' },
  fail: { icon: CircleX, label: 'Failed', className: 'text-destructive' },
} as const;

export function DiagnosticsChecklist({ checks }: { checks: DiagnosticsCheck[] }) {
  return (
    <div className="flex flex-col gap-3">
      {checks.map((check) => {
        const style = CHECK_STYLE[check.status];
        const Icon = style.icon;
        return (
          <div key={check.id} role={check.status === 'fail' ? 'alert' : undefined} className="flex gap-3">
            <Icon className={cn('mt-0.5 size-4 shrink-0', style.className)} aria-hidden="true" />
            <div className="min-w-0">
              <div className="flex flex-wrap items-baseline gap-2">
                <span className="text-sm font-medium">{check.title}</span>
                <span className={cn('text-xs font-semibold', style.className)}>{style.label}</span>
              </div>
              <div className="text-muted-foreground pt-1 text-xs">{check.detail}</div>
              {check.fix && <div className="pt-1 text-xs">Fix: {check.fix}</div>}
            </div>
          </div>
        );
      })}
    </div>
  );
}
