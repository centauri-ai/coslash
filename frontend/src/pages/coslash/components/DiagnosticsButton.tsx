import type { ComponentProps } from 'react';
import { Activity } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { DiagnosticStatus } from '@/pages/coslash/lib/diagnostics';

export function DiagnosticsButton({
  status,
  className,
  ...props
}: ComponentProps<typeof Button> & { status: DiagnosticStatus }) {
  return (
    <Button
      {...props}
      variant="ghost"
      size="icon-sm"
      className={cn('relative size-7! cursor-pointer rounded-[7px]! p-0!', className)}
      type="button"
      aria-label="Diagnostics"
      title="Diagnostics"
    >
      <Activity aria-hidden="true" />
      {status !== 'ok' && <span className="sr-only">{status} status</span>}
      <span
        className={cn('absolute -top-1 -right-1 size-2 rounded-full', {
          'hidden': status === 'ok',
          'bg-warning': status === 'warn',
          'bg-destructive': status === 'fail',
        })}
        aria-hidden="true"
      />
    </Button>
  );
}
