import { type ReactNode } from 'react';
import { TriangleAlertIcon } from 'lucide-react';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

// Wraps a cost figure that excludes a model the pricing table doesn't cover yet
export function UnpricedModelWarning({ unpriced, children }: { unpriced: string[]; children: ReactNode }) {
  const models = [...new Set(unpriced)];
  if (models.length === 0) return children;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex items-center gap-1">
          <TriangleAlertIcon className="text-warning-fg size-4" />
          {children}
        </span>
      </TooltipTrigger>
      <TooltipContent>No pricing info for {models.join(', ')}, excluded from this estimate.</TooltipContent>
    </Tooltip>
  );
}
