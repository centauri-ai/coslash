import { type ReactNode } from 'react';
import { TriangleAlertIcon } from 'lucide-react';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { getUnpricedModels, type ModelTokens } from '@/pages/coslash/lib/pricing';

// Wraps a cost figure that excludes a model the pricing table doesn't cover yet
export function UnpricedModelWarning({
  tokens,
  children,
}: {
  tokens: Record<string, ModelTokens>[];
  children: ReactNode;
}) {
  const models = [...new Set(tokens.flatMap(getUnpricedModels))];
  if (models.length === 0) return children;

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex items-center gap-1">
            <TriangleAlertIcon className="text-warning-fg size-4" />
            {children}
          </span>
        </TooltipTrigger>
        <TooltipContent>No pricing info for {models.join(', ')}, excluded from this estimate.</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
